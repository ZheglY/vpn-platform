package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ZheglY/vpn-platform/internal/platform/cryptoutil"
	platformkafka "github.com/ZheglY/vpn-platform/internal/platform/kafka"
	platformpostgres "github.com/ZheglY/vpn-platform/internal/platform/postgres"
	"github.com/ZheglY/vpn-platform/services/provisioning/internal/domain"
)

type Store struct{ pool *pgxpool.Pool }

func Open(ctx context.Context, databaseURL string, options ...platformpostgres.Option) (*Store, error) {
	pool, err := platformpostgres.OpenPool(ctx, databaseURL, options...)
	if err != nil {
		return nil, err
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close()                         { s.pool.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func (s *Store) RecordCommand(ctx context.Context, meta domain.EventMeta, command domain.Command, kind string, maxAttempts int) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin provisioning command: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, command.CredentialID); err != nil {
		return fmt.Errorf("lock provisioning credential: %w", err)
	}
	var existing domain.EventMeta
	var existingOperation, existingCredential string
	var existingRevision int
	err = tx.QueryRow(ctx, `
SELECT event_type, credential_id, operation_id, aggregate_sequence, desired_revision,
       correlation_id, source_topic, source_partition, source_offset, payload_sha256
FROM command_inbox WHERE event_id = $1`, meta.EventID).Scan(
		&existing.EventType, &existingCredential, &existingOperation, &existing.AggregateSequence,
		&existingRevision, &existing.CorrelationID, &existing.SourceTopic, &existing.SourcePartition,
		&existing.SourceOffset, &existing.PayloadSHA256,
	)
	if err == nil {
		if existing.EventType != meta.EventType || existingCredential != command.CredentialID || existingOperation != command.OperationID || existing.AggregateSequence != meta.AggregateSequence || existingRevision != command.DesiredRevision || existing.CorrelationID != meta.CorrelationID || existing.SourceTopic != meta.SourceTopic || existing.SourcePartition != meta.SourcePartition || existing.SourceOffset != meta.SourceOffset || existing.PayloadSHA256 != meta.PayloadSHA256 {
			return domain.ErrConflict
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("check provisioning command replay: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO credential_command_cursors (credential_id) VALUES ($1) ON CONFLICT DO NOTHING`, command.CredentialID); err != nil {
		return fmt.Errorf("create provisioning command cursor: %w", err)
	}
	var lastSequence int64
	var lastRevision int
	if err := tx.QueryRow(ctx, `SELECT last_sequence, last_desired_revision FROM credential_command_cursors WHERE credential_id=$1 FOR UPDATE`, command.CredentialID).Scan(&lastSequence, &lastRevision); err != nil {
		return fmt.Errorf("lock provisioning command cursor: %w", err)
	}
	if meta.AggregateSequence > lastSequence+1 {
		return domain.ErrSequenceGap
	}
	if meta.AggregateSequence <= lastSequence || command.DesiredRevision <= lastRevision {
		return domain.ErrConflict
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO command_inbox (
    event_id,event_type,credential_id,operation_id,aggregate_sequence,desired_revision,correlation_id,
    source_topic,source_partition,source_offset,payload_sha256
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		meta.EventID, meta.EventType, command.CredentialID, command.OperationID, meta.AggregateSequence,
		command.DesiredRevision, meta.CorrelationID, meta.SourceTopic, meta.SourcePartition, meta.SourceOffset, meta.PayloadSHA256); err != nil {
		if isUniqueViolation(err) {
			return domain.ErrConflict
		}
		return fmt.Errorf("insert provisioning command inbox: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO operations (id,credential_id,kind,desired_revision,command_sequence,max_attempts,correlation_id,causation_event_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, command.OperationID, command.CredentialID, kind, command.DesiredRevision, meta.AggregateSequence, maxAttempts, meta.CorrelationID, meta.EventID); err != nil {
		if isUniqueViolation(err) {
			return domain.ErrConflict
		}
		return fmt.Errorf("insert provisioning operation: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE operations
SET state='superseded',lease_until=NULL,last_error_code='superseded_by_higher_revision',completed_at=clock_timestamp()
WHERE credential_id=$1 AND id<>$2 AND desired_revision<$3
  AND state IN ('pending','processing','retry')`, command.CredentialID, command.OperationID, command.DesiredRevision); err != nil {
		return fmt.Errorf("supersede older provisioning operations: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE allocations
SET desired_operation_id=$2,
    desired_revision=$3,
    desired_state=CASE WHEN $4='provision' THEN 'present' ELSE 'absent' END,
    allocation_revision=CASE WHEN $4='provision' THEN $3 ELSE allocation_revision END,
    state=CASE WHEN $4='provision' AND state<>'revoked' THEN 'pending' ELSE state END,
    last_error_code=NULL,
    next_reconcile_at=clock_timestamp(),
    reconcile_lease_until=NULL
WHERE credential_id=$1 AND desired_revision<$3`, command.CredentialID, command.OperationID, command.DesiredRevision, kind); err != nil {
		return fmt.Errorf("advance allocation generation: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE credential_command_cursors SET last_sequence=$2,last_desired_revision=$3,updated_at=clock_timestamp() WHERE credential_id=$1`, command.CredentialID, meta.AggregateSequence, command.DesiredRevision); err != nil {
		return fmt.Errorf("advance provisioning command cursor: %w", err)
	}
	return commit(ctx, tx, "provisioning command")
}

func (s *Store) RecordDeadLetter(ctx context.Context, topic string, partition int32, offset int64, payloadHash, reason string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin provisioning dead letter: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
INSERT INTO consumer_dead_letters (source_topic,source_partition,source_offset,payload_sha256,reason_code)
VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, topic, partition, offset, payloadHash, reason)
	if err != nil {
		return fmt.Errorf("insert provisioning dead letter: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}
	eventID, err := cryptoutil.RandomUUID()
	if err != nil {
		return err
	}
	dlqTopic := topic + ".dlq"
	data := map[string]any{"source_topic": topic, "source_partition": partition, "source_offset": offset, "payload_sha256": payloadHash, "reason_code": reason}
	payload, err := eventPayload(dlqTopic, eventID, eventID, 0, "consumer_record", "source:"+topic, eventID, nil, time.Now().UTC(), data)
	if err != nil {
		return err
	}
	if err := insertOutbox(ctx, tx, eventID, dlqTopic, "source:"+topic, eventID, 0, fmt.Sprintf("dlq:%s:%d:%d", topic, partition, offset), payload); err != nil {
		return err
	}
	return commit(ctx, tx, "provisioning dead letter")
}

func (s *Store) ClaimOperation(ctx context.Context, lease time.Duration) (domain.Operation, bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.Operation{}, false, fmt.Errorf("begin claim provisioning operation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var operation domain.Operation
	err = tx.QueryRow(ctx, `
SELECT id,credential_id,kind,desired_revision,command_sequence,attempts,max_attempts,allocation_revision,correlation_id,causation_event_id
FROM operations AS candidate
WHERE (
        (candidate.state IN ('pending','retry') AND candidate.next_attempt_at <= clock_timestamp())
        OR (candidate.state='processing' AND candidate.lease_until < clock_timestamp())
      )
  AND NOT EXISTS (
        SELECT 1 FROM operations AS predecessor
        WHERE predecessor.credential_id=candidate.credential_id
          AND predecessor.command_sequence < candidate.command_sequence
          AND predecessor.state NOT IN ('succeeded','failed','superseded')
      )
ORDER BY next_attempt_at,created_at,id
FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(
		&operation.ID, &operation.CredentialID, &operation.Kind, &operation.DesiredRevision,
		&operation.CommandSequence, &operation.Attempts, &operation.MaxAttempts,
		&operation.AllocationRevision, &operation.CorrelationID, &operation.CausationEventID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Operation{}, false, tx.Commit(ctx)
	}
	if err != nil {
		return domain.Operation{}, false, fmt.Errorf("select provisioning operation: %w", err)
	}
	operation.Attempts++
	if _, err := tx.Exec(ctx, `UPDATE operations SET state='processing',attempts=$2,lease_until=clock_timestamp()+$3::interval,last_error_code=NULL WHERE id=$1`, operation.ID, operation.Attempts, lease); err != nil {
		return domain.Operation{}, false, fmt.Errorf("lease provisioning operation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Operation{}, false, fmt.Errorf("commit provisioning operation claim: %w", err)
	}
	return operation, true, nil
}

func (s *Store) EnsureAllocations(ctx context.Context, operation domain.Operation, placement domain.Placement, protocol string) ([]domain.Allocation, error) {
	if placement.PrimaryNodes != 1 || placement.FailoverNodes != 1 || protocol != "vless_reality" {
		return nil, domain.ErrConflict
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin provisioning allocation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var state string
	if err := tx.QueryRow(ctx, `SELECT state FROM operations WHERE id=$1 AND credential_id=$2 AND desired_revision=$3 FOR UPDATE`, operation.ID, operation.CredentialID, operation.DesiredRevision).Scan(&state); err != nil {
		return nil, fmt.Errorf("lock provisioning allocation operation: %w", err)
	}
	if state != "processing" {
		return nil, domain.ErrConflict
	}
	var databaseNow time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&databaseNow); err != nil {
		return nil, fmt.Errorf("read allocation time: %w", err)
	}
	if !placement.ValidUntil.After(databaseNow) {
		return nil, domain.ErrConflict
	}
	allocations, err := listAllocationsTx(ctx, tx, operation.CredentialID)
	if err != nil {
		return nil, err
	}
	if len(allocations) > 0 {
		if len(allocations) != 2 {
			return nil, domain.ErrConflict
		}
		roles := make(map[string]struct{}, len(allocations))
		for _, allocation := range allocations {
			if allocation.DesiredOperationID != operation.ID || allocation.DesiredRevision != operation.DesiredRevision || allocation.DesiredState != "present" || allocation.Protocol != protocol {
				return nil, domain.ErrConflict
			}
			roles[allocation.Role] = struct{}{}
			if allocation.State != "revoked" {
				continue
			}
			var status, region string
			var capacityLimit, reservePercent, allocatedClients int
			var lastSeen time.Time
			if err := tx.QueryRow(ctx, `
SELECT status,region,capacity_limit,reserve_percent,allocated_clients,last_seen_at
FROM nodes WHERE id=$1 FOR UPDATE`, allocation.Node.ID).Scan(&status, &region, &capacityLimit, &reservePercent, &allocatedClients, &lastSeen); err != nil {
				return nil, fmt.Errorf("lock reactivation node capacity: %w", err)
			}
			capacityCeiling := capacityLimit * (100 - reservePercent) / 100
			if status != "active" || region != placement.Region || lastSeen.Before(databaseNow.Add(-45*time.Second)) || allocatedClients >= capacityCeiling {
				return nil, domain.ErrCapacity
			}
			if _, err := tx.Exec(ctx, `UPDATE nodes SET allocated_clients=allocated_clients+1,updated_at=clock_timestamp() WHERE id=$1`, allocation.Node.ID); err != nil {
				return nil, fmt.Errorf("reserve reactivation node capacity: %w", err)
			}
		}
		if _, primary := roles["primary"]; !primary {
			return nil, domain.ErrConflict
		}
		if _, failover := roles["failover"]; !failover || len(roles) != 2 {
			return nil, domain.ErrConflict
		}
		tag, err := tx.Exec(ctx, `
UPDATE allocations
SET desired_operation_id=$2,desired_revision=$3,desired_state='present',allocation_revision=$3,
    state='pending',applied_config_revision=NULL,applied_at=NULL,revoked_at=NULL,last_error_code=NULL,
    next_reconcile_at=clock_timestamp(),reconcile_lease_until=NULL
WHERE credential_id=$1 AND desired_operation_id=$2 AND desired_revision=$3`, operation.CredentialID, operation.ID, operation.DesiredRevision)
		if err != nil {
			return nil, fmt.Errorf("rebind retained allocations: %w", err)
		}
		if tag.RowsAffected() != int64(len(allocations)) {
			return nil, domain.ErrConflict
		}
		if _, err := tx.Exec(ctx, `UPDATE operations SET allocation_revision=$2 WHERE id=$1 AND state='processing'`, operation.ID, operation.DesiredRevision); err != nil {
			return nil, fmt.Errorf("bind retained allocation revision: %w", err)
		}
		allocations, err = listAllocationsTx(ctx, tx, operation.CredentialID)
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit retained provisioning allocation: %w", err)
		}
		return allocations, nil
	}
	rows, err := tx.Query(ctx, `
SELECT id,region,management_url,management_spiffe_id,capacity_limit,reserve_percent,allocated_clients,
       public_address,public_port,server_name,reality_public_key,short_id,spider_x,label
FROM nodes
WHERE region=$1 AND status='active'
  AND last_seen_at >= clock_timestamp() - interval '45 seconds'
  AND allocated_clients < floor(capacity_limit * (100-reserve_percent) / 100.0)
ORDER BY (allocated_clients::numeric / capacity_limit), id
FOR UPDATE SKIP LOCKED LIMIT 2`, placement.Region)
	if err != nil {
		return nil, fmt.Errorf("select provisioning nodes: %w", err)
	}
	defer rows.Close()
	var nodes []domain.Node
	for rows.Next() {
		var node domain.Node
		if err := rows.Scan(&node.ID, &node.Region, &node.ManagementURL, &node.ManagementSPIFFEID, &node.CapacityLimit, &node.ReservePercent, &node.AllocatedClients, &node.PublicAddress, &node.PublicPort, &node.ServerName, &node.RealityPublicKey, &node.ShortID, &node.SpiderX, &node.Label); err != nil {
			return nil, fmt.Errorf("scan provisioning node: %w", err)
		}
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate provisioning nodes: %w", err)
	}
	if len(nodes) != 2 {
		return nil, domain.ErrCapacity
	}
	roles := []string{"primary", "failover"}
	for index, node := range nodes {
		allocationID, err := cryptoutil.RandomUUID()
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO allocations (id,credential_id,operation_id,desired_operation_id,node_id,role,protocol,desired_revision,allocation_revision)
VALUES ($1,$2,$3,$3,$4,$5,$6,$7,$7)`, allocationID, operation.CredentialID, operation.ID, node.ID, roles[index], protocol, operation.DesiredRevision); err != nil {
			return nil, fmt.Errorf("insert provisioning allocation: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE nodes SET allocated_clients=allocated_clients+1,updated_at=clock_timestamp() WHERE id=$1`, node.ID); err != nil {
			return nil, fmt.Errorf("reserve provisioning node capacity: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE operations SET allocation_revision=$2 WHERE id=$1`, operation.ID, operation.DesiredRevision); err != nil {
		return nil, fmt.Errorf("bind allocation revision: %w", err)
	}
	allocations, err = listAllocationsTx(ctx, tx, operation.CredentialID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit provisioning allocation: %w", err)
	}
	return allocations, nil
}

func (s *Store) ListAllocations(ctx context.Context, credentialID string) ([]domain.Allocation, error) {
	return listAllocationsQuery(ctx, s.pool, credentialID)
}

func (s *Store) GetSupportSnapshot(ctx context.Context, credentialID string) (domain.SupportSnapshot, error) {
	var snapshot domain.SupportSnapshot
	err := s.pool.QueryRow(ctx, `
SELECT credential_id, id, kind, desired_revision, state, attempts, max_attempts,
       last_error_code, created_at, completed_at
FROM operations WHERE credential_id = $1
ORDER BY desired_revision DESC, created_at DESC LIMIT 1`, credentialID).Scan(
		&snapshot.CredentialID, &snapshot.OperationID, &snapshot.Kind, &snapshot.DesiredRevision,
		&snapshot.State, &snapshot.Attempts, &snapshot.MaxAttempts, &snapshot.LastErrorCode,
		&snapshot.CreatedAt, &snapshot.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.SupportSnapshot{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.SupportSnapshot{}, fmt.Errorf("get provisioning support operation: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
SELECT a.node_id, n.label, n.region, n.status, a.role, a.desired_revision,
       a.desired_state, a.allocation_revision, a.state, a.last_error_code
FROM allocations a JOIN nodes n ON n.id = a.node_id
WHERE a.credential_id = $1
ORDER BY CASE a.role WHEN 'primary' THEN 0 ELSE 1 END, a.node_id`, credentialID)
	if err != nil {
		return domain.SupportSnapshot{}, fmt.Errorf("list provisioning support allocations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var allocation domain.SupportAllocation
		if err := rows.Scan(&allocation.NodeID, &allocation.NodeLabel, &allocation.Region, &allocation.NodeStatus,
			&allocation.Role, &allocation.DesiredRevision, &allocation.DesiredState,
			&allocation.AllocationRevision, &allocation.State, &allocation.LastErrorCode); err != nil {
			return domain.SupportSnapshot{}, fmt.Errorf("scan provisioning support allocation: %w", err)
		}
		snapshot.Allocations = append(snapshot.Allocations, allocation)
	}
	if err := rows.Err(); err != nil {
		return domain.SupportSnapshot{}, fmt.Errorf("iterate provisioning support allocations: %w", err)
	}
	return snapshot, nil
}

func (s *Store) PrepareRevoke(ctx context.Context, operation domain.Operation) error {
	tag, err := s.pool.Exec(ctx, `
UPDATE allocations AS allocation
SET desired_state='absent',last_error_code=NULL,next_reconcile_at=clock_timestamp(),reconcile_lease_until=NULL
FROM operations AS operation
WHERE operation.id=$1 AND operation.credential_id=$2 AND operation.desired_revision=$3 AND operation.state='processing'
  AND allocation.credential_id=operation.credential_id
  AND allocation.desired_operation_id=operation.id
  AND allocation.desired_revision=operation.desired_revision
  AND allocation.desired_state='absent'`, operation.ID, operation.CredentialID, operation.DesiredRevision)
	if err != nil {
		return fmt.Errorf("prepare allocation revoke: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) MarkAllocationApplied(ctx context.Context, operationID, nodeID string, configRevision int64) error {
	tag, err := s.pool.Exec(ctx, `
UPDATE allocations AS allocation
SET state='active',applied_config_revision=$3,applied_at=clock_timestamp(),revoked_at=NULL,last_error_code=NULL,
    next_reconcile_at=clock_timestamp(),reconcile_lease_until=NULL
FROM operations AS operation
WHERE operation.id=$1 AND operation.state IN ('processing','succeeded')
  AND allocation.desired_operation_id=operation.id
  AND allocation.credential_id=operation.credential_id
  AND allocation.desired_revision=operation.desired_revision
  AND allocation.desired_state='present'
  AND allocation.node_id=$2
  AND allocation.state IN ('pending','failed','active')`, operationID, nodeID, configRevision)
	if err != nil {
		return fmt.Errorf("mark allocation applied: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrConflict
	}
	return nil
}

func (s *Store) MarkAllocationRevoked(ctx context.Context, operationID, nodeID string, configRevision int64) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin allocation revoke: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var allocationID string
	var previousState string
	err = tx.QueryRow(ctx, `
SELECT allocation.id,allocation.state
FROM allocations AS allocation
JOIN operations AS operation ON operation.id=allocation.desired_operation_id
WHERE operation.id=$1 AND operation.state IN ('processing','succeeded','failed')
  AND allocation.credential_id=operation.credential_id
  AND allocation.desired_revision=operation.desired_revision
  AND allocation.desired_state='absent'
  AND allocation.node_id=$2
FOR UPDATE OF allocation`, operationID, nodeID).Scan(&allocationID, &previousState)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	if err != nil {
		return fmt.Errorf("lock allocation revoke: %w", err)
	}
	if previousState != "revoked" {
		if _, err := tx.Exec(ctx, `UPDATE allocations SET state='revoked',applied_config_revision=$2,revoked_at=clock_timestamp(),last_error_code=NULL,next_reconcile_at=clock_timestamp(),reconcile_lease_until=NULL WHERE id=$1`, allocationID, configRevision); err != nil {
			return fmt.Errorf("mark allocation revoked: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE nodes SET allocated_clients=GREATEST(allocated_clients-1,0),updated_at=clock_timestamp() WHERE id=$1`, nodeID); err != nil {
			return fmt.Errorf("release node capacity: %w", err)
		}
	}
	return commit(ctx, tx, "allocation revoke")
}

func (s *Store) MarkAllocationFailed(ctx context.Context, operationID, nodeID, reason string) error {
	tag, err := s.pool.Exec(ctx, `
UPDATE allocations AS allocation
SET state='failed',last_error_code=$3
FROM operations AS operation
WHERE operation.id=$1 AND operation.state='processing'
  AND operation.credential_id=allocation.credential_id
  AND allocation.desired_operation_id=operation.id
  AND allocation.desired_revision=operation.desired_revision
  AND allocation.node_id=$2
  AND allocation.state <> 'revoked'`, operationID, nodeID, reason)
	if err != nil {
		return fmt.Errorf("mark allocation failed: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrConflict
	}
	return nil
}

func (s *Store) RetryOperation(ctx context.Context, operationID, reason string, delay time.Duration) error {
	tag, err := s.pool.Exec(ctx, `
UPDATE operations SET state='retry',lease_until=NULL,next_attempt_at=clock_timestamp()+$3::interval,last_error_code=$2
WHERE id=$1 AND state='processing'`, operationID, reason, delay)
	if err != nil {
		return fmt.Errorf("retry provisioning operation: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrConflict
	}
	return nil
}

func (s *Store) CompleteProvision(ctx context.Context, operation domain.Operation, status string, allocations []domain.Allocation) error {
	if status != "active" && status != "degraded" {
		return domain.ErrConflict
	}
	var endpoints []domain.Endpoint
	assignedNodeIDs := make([]string, 0, len(allocations))
	primaryActive := false
	for _, allocation := range allocations {
		if allocation.DesiredOperationID != operation.ID || allocation.DesiredRevision != operation.DesiredRevision || allocation.DesiredState != "present" || allocation.AllocationRevision != operation.DesiredRevision {
			return domain.ErrConflict
		}
		assignedNodeIDs = append(assignedNodeIDs, allocation.Node.ID)
		if allocation.State != "active" {
			continue
		}
		if allocation.Role == "primary" {
			primaryActive = true
		}
		endpoints = append(endpoints, endpointFromAllocation(allocation))
	}
	if len(assignedNodeIDs) != 2 || assignedNodeIDs[0] == assignedNodeIDs[1] || !primaryActive || (status == "active" && len(endpoints) != 2) || (status == "degraded" && len(endpoints) != 1) {
		return domain.ErrConflict
	}
	sort.Slice(endpoints, func(i, j int) bool {
		if endpoints[i].Role == endpoints[j].Role {
			return endpoints[i].NodeID < endpoints[j].NodeID
		}
		return endpoints[i].Role == "primary"
	})
	sort.Strings(assignedNodeIDs)
	data := map[string]any{"operation_id": operation.ID, "credential_id": operation.CredentialID, "applied_revision": operation.DesiredRevision, "status": status, "assigned_node_ids": assignedNodeIDs, "endpoints": endpoints, "applied_at": time.Now().UTC()}
	return s.completeOperationWithEvent(ctx, operation, "access.provision.succeeded.v1", "provision-succeeded:"+operation.ID, data)
}

func (s *Store) CompleteProvisionFailure(ctx context.Context, operation domain.Operation, scope, reason string, pending []string) error {
	data := map[string]any{"operation_id": operation.ID, "credential_id": operation.CredentialID, "failed_revision": operation.DesiredRevision, "failure_scope": scope, "terminal": true, "reason_code": reason, "failed_at": time.Now().UTC()}
	if len(pending) > 0 {
		data["pending_node_ids"] = pending
	}
	return s.completeOperationWithEvent(ctx, operation, "access.provision.failed.v1", "provision-failed:"+operation.ID, data)
}

func (s *Store) CompleteRevoke(ctx context.Context, operation domain.Operation, allocations []domain.Allocation) error {
	nodeIDs := make([]string, 0, len(allocations))
	allocationRevision := 0
	for _, allocation := range allocations {
		if allocation.DesiredOperationID != operation.ID || allocation.DesiredRevision != operation.DesiredRevision || allocation.DesiredState != "absent" || allocation.State != "revoked" {
			return domain.ErrConflict
		}
		nodeIDs = append(nodeIDs, allocation.Node.ID)
		if allocation.AllocationRevision > allocationRevision {
			allocationRevision = allocation.AllocationRevision
		}
	}
	sort.Strings(nodeIDs)
	data := map[string]any{"operation_id": operation.ID, "credential_id": operation.CredentialID, "desired_revision": operation.DesiredRevision, "allocation_revision": allocationRevision, "all_assigned_nodes_removed": true, "node_ids": nodeIDs, "revoked_at": time.Now().UTC()}
	return s.completeOperationWithEvent(ctx, operation, "access.revoke.succeeded.v1", "revoke-succeeded:"+operation.ID, data)
}

func (s *Store) CompleteRevokeFailure(ctx context.Context, operation domain.Operation, reason string, pending []string) error {
	data := map[string]any{"operation_id": operation.ID, "credential_id": operation.CredentialID, "failed_revision": operation.DesiredRevision, "failure_scope": "all", "terminal": true, "reason_code": reason, "failed_at": time.Now().UTC()}
	if len(pending) > 0 {
		data["pending_node_ids"] = pending
	}
	return s.completeOperationWithEvent(ctx, operation, "access.revoke.failed.v1", "revoke-failed:"+operation.ID, data)
}

func (s *Store) completeOperationWithEvent(ctx context.Context, operation domain.Operation, topic, dedupe string, data any) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin complete provisioning operation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var state string
	if err := tx.QueryRow(ctx, `SELECT state FROM operations WHERE id=$1 FOR UPDATE`, operation.ID).Scan(&state); err != nil {
		return fmt.Errorf("lock completed provisioning operation: %w", err)
	}
	if state == "succeeded" || state == "failed" {
		return tx.Commit(ctx)
	}
	if state != "processing" {
		return domain.ErrConflict
	}
	var currentRevision int
	if err := tx.QueryRow(ctx, `SELECT last_desired_revision FROM credential_command_cursors WHERE credential_id=$1 FOR UPDATE`, operation.CredentialID).Scan(&currentRevision); err != nil {
		return fmt.Errorf("lock provisioning generation cursor: %w", err)
	}
	if currentRevision != operation.DesiredRevision {
		return domain.ErrConflict
	}
	var aggregateSequence int64
	if err := tx.QueryRow(ctx, `
INSERT INTO credential_outcome_cursors (credential_id,last_sequence)
VALUES ($1,1)
ON CONFLICT (credential_id) DO UPDATE
SET last_sequence=credential_outcome_cursors.last_sequence+1,updated_at=clock_timestamp()
RETURNING last_sequence`, operation.CredentialID).Scan(&aggregateSequence); err != nil {
		return fmt.Errorf("advance provisioning outcome sequence: %w", err)
	}
	eventID, err := cryptoutil.RandomUUID()
	if err != nil {
		return err
	}
	causationID := operation.CausationEventID
	payload, err := eventPayload(topic, eventID, operation.CredentialID, aggregateSequence, "credential", "credential:"+operation.CredentialID, operation.CorrelationID, &causationID, time.Now().UTC(), data)
	if err != nil {
		return err
	}
	if err := insertOutbox(ctx, tx, eventID, topic, "credential:"+operation.CredentialID, operation.CredentialID, aggregateSequence, dedupe, payload); err != nil {
		return err
	}
	terminalState := "succeeded"
	if topic == "access.provision.failed.v1" || topic == "access.revoke.failed.v1" {
		terminalState = "failed"
	}
	if _, err := tx.Exec(ctx, `UPDATE operations SET state=$2,lease_until=NULL,last_error_code=NULL,completed_at=clock_timestamp() WHERE id=$1`, operation.ID, terminalState); err != nil {
		return fmt.Errorf("complete provisioning operation state: %w", err)
	}
	return commit(ctx, tx, "complete provisioning operation")
}

func (s *Store) ClaimOutbox(ctx context.Context, lease time.Duration) (domain.OutboxMessage, bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.OutboxMessage{}, false, fmt.Errorf("begin provisioning outbox claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var message domain.OutboxMessage
	err = tx.QueryRow(ctx, `
SELECT event_id,topic,partition_key,payload,attempts FROM outbox
WHERE ((state='pending' AND next_attempt_at <= clock_timestamp()) OR (state='processing' AND lease_until < clock_timestamp()))
  AND (aggregate_sequence IS NULL OR NOT EXISTS (
      SELECT 1 FROM outbox AS predecessor
      WHERE predecessor.aggregate_id=outbox.aggregate_id
        AND predecessor.aggregate_sequence<outbox.aggregate_sequence
        AND predecessor.state<>'published'
  ))
ORDER BY created_at,aggregate_id,aggregate_sequence NULLS FIRST,event_id
FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&message.EventID, &message.Topic, &message.PartitionKey, &message.Payload, &message.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.OutboxMessage{}, false, tx.Commit(ctx)
	}
	if err != nil {
		return domain.OutboxMessage{}, false, fmt.Errorf("select provisioning outbox: %w", err)
	}
	message.Attempts++
	if _, err := tx.Exec(ctx, `UPDATE outbox SET state='processing',attempts=$2,lease_until=clock_timestamp()+$3::interval WHERE event_id=$1`, message.EventID, message.Attempts, lease); err != nil {
		return domain.OutboxMessage{}, false, fmt.Errorf("lease provisioning outbox: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.OutboxMessage{}, false, fmt.Errorf("commit provisioning outbox claim: %w", err)
	}
	return message, true, nil
}

func (s *Store) CompleteOutbox(ctx context.Context, eventID string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE outbox SET state='published',lease_until=NULL,published_at=clock_timestamp() WHERE event_id=$1 AND state='processing'`, eventID)
	if err != nil {
		return fmt.Errorf("complete provisioning outbox: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrConflict
	}
	return nil
}

func (s *Store) RetryOutbox(ctx context.Context, eventID string, delay time.Duration) error {
	tag, err := s.pool.Exec(ctx, `UPDATE outbox SET state='pending',lease_until=NULL,next_attempt_at=clock_timestamp()+$2::interval WHERE event_id=$1 AND state='processing'`, eventID, delay)
	if err != nil {
		return fmt.Errorf("retry provisioning outbox: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrConflict
	}
	return nil
}

func (s *Store) SeedNodes(ctx context.Context, seeds []domain.NodeSeed) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin seed provisioning nodes: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, seed := range seeds {
		_, err := tx.Exec(ctx, `
INSERT INTO nodes (id,region,management_url,management_spiffe_id,status,capacity_limit,reserve_percent,public_address,public_port,server_name,reality_public_key,short_id,spider_x,label)
VALUES ($1,$2,$3,$4,'active',$5,$6,$7,$8,$9,$10,$11,$12,$13)
ON CONFLICT (id) DO UPDATE SET
 region=EXCLUDED.region,management_url=EXCLUDED.management_url,management_spiffe_id=EXCLUDED.management_spiffe_id,
 capacity_limit=EXCLUDED.capacity_limit,reserve_percent=EXCLUDED.reserve_percent,public_address=EXCLUDED.public_address,
 public_port=EXCLUDED.public_port,server_name=EXCLUDED.server_name,reality_public_key=EXCLUDED.reality_public_key,
 short_id=EXCLUDED.short_id,spider_x=EXCLUDED.spider_x,label=EXCLUDED.label,updated_at=clock_timestamp()
WHERE nodes.allocated_clients=0`, seed.ID, seed.Region, seed.ManagementURL, seed.ManagementSPIFFEID, seed.CapacityLimit, seed.ReservePercent, seed.PublicAddress, seed.PublicPort, seed.ServerName, seed.RealityPublicKey, seed.ShortID, seed.SpiderX, seed.Label)
		if err != nil {
			return fmt.Errorf("seed provisioning node: %w", err)
		}
	}
	return commit(ctx, tx, "seed provisioning nodes")
}

func (s *Store) ListNodes(ctx context.Context) ([]domain.Node, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id,region,management_url,management_spiffe_id,capacity_limit,reserve_percent,allocated_clients,
       public_address,public_port,server_name,reality_public_key,short_id,spider_x,label
FROM nodes WHERE status <> 'draining' ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list provisioning nodes: %w", err)
	}
	defer rows.Close()
	var nodes []domain.Node
	for rows.Next() {
		var node domain.Node
		if err := rows.Scan(&node.ID, &node.Region, &node.ManagementURL, &node.ManagementSPIFFEID, &node.CapacityLimit, &node.ReservePercent, &node.AllocatedClients, &node.PublicAddress, &node.PublicPort, &node.ServerName, &node.RealityPublicKey, &node.ShortID, &node.SpiderX, &node.Label); err != nil {
			return nil, fmt.Errorf("scan provisioning node: %w", err)
		}
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}

func (s *Store) RecordNodeHealth(ctx context.Context, status domain.AgentStatus, staleAfter time.Duration) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin node health: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
UPDATE nodes SET status=CASE WHEN $2 THEN 'active' ELSE 'offline' END,agent_version=$3,xray_version=$4,
 config_revision=$5,last_seen_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1`, status.NodeID, status.XrayHealthy, status.AgentVersion, status.XrayVersion, status.ConfigRevision)
	if err != nil {
		return fmt.Errorf("update node health: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrNotFound
	}
	if _, err := tx.Exec(ctx, `INSERT INTO node_health_snapshots (node_id,config_revision,active_clients,xray_healthy,agent_version,xray_version) VALUES ($1,$2,$3,$4,$5,$6)`, status.NodeID, status.ConfigRevision, status.ActiveClients, status.XrayHealthy, status.AgentVersion, status.XrayVersion); err != nil {
		return fmt.Errorf("insert node health snapshot: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE nodes SET status='offline',updated_at=clock_timestamp() WHERE status='active' AND last_seen_at < clock_timestamp()-$1::interval`, staleAfter); err != nil {
		return fmt.Errorf("expire stale nodes: %w", err)
	}
	return commit(ctx, tx, "node health")
}

func (s *Store) MarkNodeOffline(ctx context.Context, nodeID string) error {
	_, err := s.pool.Exec(ctx, `UPDATE nodes SET status='offline',updated_at=clock_timestamp() WHERE id=$1`, nodeID)
	if err != nil {
		return fmt.Errorf("mark node offline: %w", err)
	}
	return nil
}

func (s *Store) ClaimReconciliationCandidates(ctx context.Context, limit int, lease time.Duration) ([]domain.ReconciliationCandidate, error) {
	if limit < 1 || limit > 1000 || lease <= 0 {
		return nil, domain.ErrConflict
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin reconciliation claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
SELECT a.id,a.credential_id,a.desired_operation_id,a.role,a.protocol,a.desired_revision,a.desired_state,a.allocation_revision,a.state,COALESCE(a.applied_config_revision,0),
       n.id,n.region,n.management_url,n.management_spiffe_id,n.capacity_limit,n.reserve_percent,n.allocated_clients,
       n.public_address,n.public_port,n.server_name,n.reality_public_key,n.short_id,n.spider_x,n.label
FROM allocations a
JOIN nodes n ON n.id=a.node_id
JOIN operations o ON o.id=a.desired_operation_id
WHERE n.status IN ('active','draining')
  AND (a.desired_state='absent' OR (a.desired_state='present' AND o.state='succeeded'))
	AND a.next_reconcile_at <= clock_timestamp()
	AND (a.reconcile_lease_until IS NULL OR a.reconcile_lease_until < clock_timestamp())
ORDER BY a.next_reconcile_at,a.created_at,a.id
FOR UPDATE OF a SKIP LOCKED
LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("select reconciliation candidates: %w", err)
	}
	var candidates []domain.ReconciliationCandidate
	for rows.Next() {
		var candidate domain.ReconciliationCandidate
		if err := scanAllocation(rows, &candidate.Allocation); err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for index := range candidates {
		claimID, err := cryptoutil.RandomUUID()
		if err != nil {
			return nil, err
		}
		candidates[index].ClaimID = claimID
		tag, err := tx.Exec(ctx, `
UPDATE allocations
SET reconcile_claim_id=$2,reconcile_lease_until=clock_timestamp()+$3::interval,reconcile_attempts=reconcile_attempts+1
WHERE id=$1`, candidates[index].Allocation.ID, claimID, lease.String())
		if err != nil {
			return nil, fmt.Errorf("lease reconciliation candidate: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return nil, domain.ErrConflict
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit reconciliation claim: %w", err)
	}
	return candidates, nil
}

func (s *Store) RescheduleReconciliation(ctx context.Context, allocationID, claimID string, delay time.Duration) error {
	if delay < 0 {
		return domain.ErrConflict
	}
	tag, err := s.pool.Exec(ctx, `
UPDATE allocations
SET next_reconcile_at=clock_timestamp()+$3::interval,reconcile_lease_until=NULL,reconcile_claim_id=NULL
WHERE id=$1 AND reconcile_claim_id=$2`, allocationID, claimID, delay.String())
	if err != nil {
		return fmt.Errorf("reschedule reconciliation candidate: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrConflict
	}
	return nil
}

func (s *Store) RequestDeadLetterReplay(ctx context.Context, topic string, partition int32, offset int64) (string, error) {
	var payloadHash string
	err := s.pool.QueryRow(ctx, `
UPDATE consumer_dead_letters SET replay_state='requested'
WHERE source_topic=$1 AND source_partition=$2 AND source_offset=$3 AND replay_state IN ('available','requested')
RETURNING payload_sha256`, topic, partition, offset).Scan(&payloadHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("request dead-letter replay: %w", err)
	}
	return payloadHash, nil
}

func (s *Store) CompleteDeadLetterReplay(ctx context.Context, topic string, partition int32, offset int64) error {
	tag, err := s.pool.Exec(ctx, `
UPDATE consumer_dead_letters SET replay_state='replayed',replayed_at=clock_timestamp()
WHERE source_topic=$1 AND source_partition=$2 AND source_offset=$3 AND replay_state='requested'`, topic, partition, offset)
	if err != nil {
		return fmt.Errorf("complete dead-letter replay: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrConflict
	}
	return nil
}

type queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func listAllocationsQuery(ctx context.Context, query queryer, credentialID string) ([]domain.Allocation, error) {
	rows, err := query.Query(ctx, `
SELECT a.id,a.credential_id,a.desired_operation_id,a.role,a.protocol,a.desired_revision,a.desired_state,a.allocation_revision,a.state,COALESCE(a.applied_config_revision,0),
       n.id,n.region,n.management_url,n.management_spiffe_id,n.capacity_limit,n.reserve_percent,n.allocated_clients,
       n.public_address,n.public_port,n.server_name,n.reality_public_key,n.short_id,n.spider_x,n.label
FROM allocations a JOIN nodes n ON n.id=a.node_id WHERE a.credential_id=$1
ORDER BY CASE a.role WHEN 'primary' THEN 0 ELSE 1 END,n.id`, credentialID)
	if err != nil {
		return nil, fmt.Errorf("list provisioning allocations: %w", err)
	}
	defer rows.Close()
	var allocations []domain.Allocation
	for rows.Next() {
		var allocation domain.Allocation
		if err := scanAllocation(rows, &allocation); err != nil {
			return nil, err
		}
		allocations = append(allocations, allocation)
	}
	return allocations, rows.Err()
}

func scanAllocation(row interface{ Scan(...any) error }, allocation *domain.Allocation) error {
	if err := row.Scan(&allocation.ID, &allocation.CredentialID, &allocation.DesiredOperationID, &allocation.Role, &allocation.Protocol, &allocation.DesiredRevision, &allocation.DesiredState, &allocation.AllocationRevision, &allocation.State, &allocation.AppliedConfigRevision, &allocation.Node.ID, &allocation.Node.Region, &allocation.Node.ManagementURL, &allocation.Node.ManagementSPIFFEID, &allocation.Node.CapacityLimit, &allocation.Node.ReservePercent, &allocation.Node.AllocatedClients, &allocation.Node.PublicAddress, &allocation.Node.PublicPort, &allocation.Node.ServerName, &allocation.Node.RealityPublicKey, &allocation.Node.ShortID, &allocation.Node.SpiderX, &allocation.Node.Label); err != nil {
		return fmt.Errorf("scan provisioning allocation: %w", err)
	}
	return nil
}

func listAllocationsTx(ctx context.Context, tx pgx.Tx, credentialID string) ([]domain.Allocation, error) {
	return listAllocationsQuery(ctx, tx, credentialID)
}

func endpointFromAllocation(allocation domain.Allocation) domain.Endpoint {
	return domain.Endpoint{NodeID: allocation.Node.ID, Role: allocation.Role, Address: allocation.Node.PublicAddress, Port: allocation.Node.PublicPort, ServerName: allocation.Node.ServerName, RealityPublicKey: allocation.Node.RealityPublicKey, ShortID: allocation.Node.ShortID, SpiderX: allocation.Node.SpiderX, Label: allocation.Node.Label}
}

func insertOutbox(ctx context.Context, tx pgx.Tx, eventID, topic, key, aggregateID string, aggregateSequence int64, dedupe string, payload []byte) error {
	tag, err := tx.Exec(ctx, `INSERT INTO outbox (event_id,topic,partition_key,aggregate_id,aggregate_sequence,dedupe_key,payload) VALUES ($1,$2,$3,$4,NULLIF($5,0),$6,$7) ON CONFLICT (dedupe_key) DO NOTHING`, eventID, topic, key, aggregateID, aggregateSequence, dedupe, payload)
	if err != nil {
		return fmt.Errorf("insert provisioning outbox: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrConflict
	}
	return nil
}

func eventPayload(topic, eventID, aggregateID string, aggregateSequence int64, aggregateType, partitionKey, correlationID string, causationID *string, occurredAt time.Time, data any) ([]byte, error) {
	dataJSON, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("encode provisioning event data: %w", err)
	}
	envelope := platformkafka.Envelope{EventID: eventID, EventType: topic, SchemaVersion: 1, OccurredAt: occurredAt.UTC(), Producer: "provisioning-service", CorrelationID: correlationID, CausationID: causationID, AggregateType: aggregateType, AggregateID: aggregateID, AggregateSequence: aggregateSequence, PartitionKey: partitionKey, Data: dataJSON}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("encode provisioning event envelope: %w", err)
	}
	return payload, nil
}

func commit(ctx context.Context, tx pgx.Tx, operation string) error {
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit %s: %w", operation, err)
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "23505"
}
