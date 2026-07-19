package application

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"time"

	"go.uber.org/zap"

	"github.com/ZheglY/vpn-platform/services/provisioning/internal/domain"
)

type OperationWorker struct {
	store        domain.Store
	access       domain.AccessClient
	subscription domain.SubscriptionClient
	agents       domain.AgentClient
	logger       *zap.Logger
	pollInterval time.Duration
	retryDelay   time.Duration
	lease        time.Duration
}

func NewOperationWorker(store domain.Store, access domain.AccessClient, subscription domain.SubscriptionClient, agents domain.AgentClient, logger *zap.Logger, pollInterval, retryDelay, lease time.Duration) *OperationWorker {
	return &OperationWorker{store: store, access: access, subscription: subscription, agents: agents, logger: logger, pollInterval: pollInterval, retryDelay: retryDelay, lease: lease}
}

func (w *OperationWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()
	for {
		if err := w.workOnce(ctx); err != nil && ctx.Err() == nil {
			w.logger.Warn("provisioning operation attempt failed", zap.String("error_type", fmt.Sprintf("%T", err)))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *OperationWorker) workOnce(ctx context.Context) error {
	operation, ok, err := w.store.ClaimOperation(ctx, w.lease)
	if err != nil || !ok {
		return err
	}
	if operation.Kind == "provision" {
		return w.provision(ctx, operation)
	}
	return w.revoke(ctx, operation)
}

func (w *OperationWorker) provision(ctx context.Context, operation domain.Operation) error {
	material, err := w.access.GetCredentialMaterial(ctx, operation.CredentialID)
	if err != nil || !validMaterial(material, operation) {
		return w.retryOrFailProvision(ctx, operation, "all", "credential_material_unavailable", nil)
	}
	placement, err := w.subscription.GetPlacement(ctx, material.SubscriptionID)
	if err != nil || !validPlacement(placement, material.SubscriptionID) {
		return w.retryOrFailProvision(ctx, operation, "all", "placement_unavailable", nil)
	}
	allocations, err := w.store.EnsureAllocations(ctx, operation, placement, material.Protocol)
	if errors.Is(err, domain.ErrCapacity) {
		return w.retryOrFailProvision(ctx, operation, "all", "capacity_exhausted", nil)
	}
	if err != nil {
		return w.retryOrFailProvision(ctx, operation, "all", "allocation_failed", nil)
	}
	for _, allocation := range allocations {
		if allocation.State == "active" {
			continue
		}
		result, applyErr := w.agents.Apply(ctx, allocation.Node, domain.AgentDesiredState{
			OperationID: operation.ID, CredentialID: operation.CredentialID, DesiredRevision: operation.DesiredRevision,
			State: "present", Protocol: material.Protocol, VLESSClientUUID: material.VLESSClientUUID,
		})
		if applyErr != nil || !validAgentResult(result, operation, "present") {
			_ = w.store.MarkAllocationFailed(ctx, operation.ID, allocation.Node.ID, "agent_apply_failed")
			continue
		}
		if err := w.store.MarkAllocationApplied(ctx, operation.ID, allocation.Node.ID, result.ConfigRevision); err != nil {
			return err
		}
	}
	allocations, err = w.store.ListAllocations(ctx, operation.CredentialID)
	if err != nil {
		return w.retryOrFailProvision(ctx, operation, "all", "allocation_read_failed", nil)
	}
	primaryActive, failoverActive := roleActive(allocations, "primary"), roleActive(allocations, "failover")
	if !primaryActive {
		return w.retryOrFailProvision(ctx, operation, "primary", "primary_apply_failed", pendingNodeIDs(allocations))
	}
	if !failoverActive {
		if operation.Attempts >= operation.MaxAttempts {
			return w.store.CompleteProvision(ctx, operation, "degraded", allocations)
		}
		return w.store.RetryOperation(ctx, operation.ID, "failover_apply_failed", retryBackoff(w.retryDelay, operation.Attempts, operation.ID))
	}
	return w.store.CompleteProvision(ctx, operation, "active", allocations)
}

func (w *OperationWorker) revoke(ctx context.Context, operation domain.Operation) error {
	if err := w.store.PrepareRevoke(ctx, operation); err != nil && !errors.Is(err, domain.ErrNotFound) {
		return w.retryOrFailRevoke(ctx, operation, "revoke_prepare_failed", nil)
	}
	allocations, err := w.store.ListAllocations(ctx, operation.CredentialID)
	if err != nil {
		return w.retryOrFailRevoke(ctx, operation, "allocation_read_failed", nil)
	}
	for _, allocation := range allocations {
		if allocation.State == "revoked" {
			continue
		}
		result, applyErr := w.agents.Apply(ctx, allocation.Node, domain.AgentDesiredState{
			OperationID: operation.ID, CredentialID: operation.CredentialID, DesiredRevision: operation.DesiredRevision, State: "absent",
		})
		if applyErr != nil || !validAgentResult(result, operation, "absent") {
			_ = w.store.MarkAllocationFailed(ctx, operation.ID, allocation.Node.ID, "agent_revoke_failed")
			continue
		}
		if err := w.store.MarkAllocationRevoked(ctx, operation.ID, allocation.Node.ID, result.ConfigRevision); err != nil {
			return err
		}
	}
	allocations, err = w.store.ListAllocations(ctx, operation.CredentialID)
	if err != nil {
		return w.retryOrFailRevoke(ctx, operation, "allocation_read_failed", nil)
	}
	if pending := unrevokedNodeIDs(allocations); len(pending) > 0 {
		return w.retryOrFailRevoke(ctx, operation, "revoke_incomplete", pending)
	}
	return w.store.CompleteRevoke(ctx, operation, allocations)
}

type ReconciliationWorker struct {
	store    domain.Store
	access   domain.AccessClient
	agents   domain.AgentClient
	logger   *zap.Logger
	interval time.Duration
	batch    int
}

func NewReconciliationWorker(store domain.Store, access domain.AccessClient, agents domain.AgentClient, logger *zap.Logger, interval time.Duration, batch int) *ReconciliationWorker {
	return &ReconciliationWorker{store: store, access: access, agents: agents, logger: logger, interval: interval, batch: batch}
}

func (w *ReconciliationWorker) Run(ctx context.Context) {
	w.reconcile(ctx)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.reconcile(ctx)
		}
	}
}

func (w *ReconciliationWorker) reconcile(ctx context.Context) {
	candidates, err := w.store.ListReconciliationCandidates(ctx, w.batch)
	if err != nil {
		return
	}
	for _, candidate := range candidates {
		if err := w.reconcileOne(ctx, candidate.Allocation); err != nil && ctx.Err() == nil {
			w.logger.Warn("node allocation reconciliation failed", zap.String("node_id", candidate.Allocation.Node.ID), zap.String("error_type", fmt.Sprintf("%T", err)))
		}
	}
}

func (w *ReconciliationWorker) reconcileOne(ctx context.Context, allocation domain.Allocation) error {
	actual, err := w.agents.CredentialState(ctx, allocation.Node, allocation.CredentialID)
	if err != nil {
		return err
	}
	if actual.DesiredRevision > allocation.DesiredRevision {
		return domain.ErrConflict
	}
	if actual.DesiredRevision == allocation.DesiredRevision && actual.State == allocation.DesiredState {
		return w.recordConvergence(ctx, allocation, actual.ConfigRevision)
	}
	desired := domain.AgentDesiredState{OperationID: allocation.DesiredOperationID, CredentialID: allocation.CredentialID, DesiredRevision: allocation.DesiredRevision, State: allocation.DesiredState}
	if allocation.DesiredState == "present" {
		material, err := w.access.GetCredentialMaterial(ctx, allocation.CredentialID)
		if err != nil || material.CredentialID != allocation.CredentialID || material.Revision != allocation.DesiredRevision || material.Protocol != allocation.Protocol {
			return domain.ErrConflict
		}
		desired.Protocol, desired.VLESSClientUUID = material.Protocol, material.VLESSClientUUID
	}
	result, err := w.agents.Apply(ctx, allocation.Node, desired)
	if err != nil {
		return err
	}
	if result.OperationID != desired.OperationID || result.CredentialID != desired.CredentialID || result.DesiredRevision != desired.DesiredRevision || result.State != desired.State || result.ConfigRevision < 1 || result.AppliedAt.IsZero() {
		return domain.ErrConflict
	}
	return w.recordConvergence(ctx, allocation, result.ConfigRevision)
}

func (w *ReconciliationWorker) recordConvergence(ctx context.Context, allocation domain.Allocation, configRevision int64) error {
	if configRevision < 1 {
		return domain.ErrConflict
	}
	if allocation.DesiredState == "present" {
		if allocation.State == "active" {
			return nil
		}
		return w.store.MarkAllocationApplied(ctx, allocation.DesiredOperationID, allocation.Node.ID, configRevision)
	}
	if allocation.State == "revoked" {
		return nil
	}
	return w.store.MarkAllocationRevoked(ctx, allocation.DesiredOperationID, allocation.Node.ID, configRevision)
}

func (w *OperationWorker) retryOrFailProvision(ctx context.Context, operation domain.Operation, scope, reason string, pending []string) error {
	if operation.Attempts >= operation.MaxAttempts {
		return w.store.CompleteProvisionFailure(ctx, operation, scope, reason, pending)
	}
	return w.store.RetryOperation(ctx, operation.ID, reason, retryBackoff(w.retryDelay, operation.Attempts, operation.ID))
}

func (w *OperationWorker) retryOrFailRevoke(ctx context.Context, operation domain.Operation, reason string, pending []string) error {
	if operation.Attempts >= operation.MaxAttempts {
		return w.store.CompleteRevokeFailure(ctx, operation, reason, pending)
	}
	return w.store.RetryOperation(ctx, operation.ID, reason, retryBackoff(w.retryDelay, operation.Attempts, operation.ID))
}

func validAgentResult(result domain.AgentResult, operation domain.Operation, state string) bool {
	return result.OperationID == operation.ID && result.CredentialID == operation.CredentialID && result.DesiredRevision == operation.DesiredRevision && result.State == state && result.ConfigRevision > 0 && !result.AppliedAt.IsZero()
}

func roleActive(allocations []domain.Allocation, role string) bool {
	for _, allocation := range allocations {
		if allocation.Role == role && allocation.State == "active" {
			return true
		}
	}
	return false
}

func pendingNodeIDs(allocations []domain.Allocation) []string {
	ids := make([]string, 0, len(allocations))
	for _, allocation := range allocations {
		if allocation.State != "active" && allocation.State != "revoked" {
			ids = append(ids, allocation.Node.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

func unrevokedNodeIDs(allocations []domain.Allocation) []string {
	ids := make([]string, 0, len(allocations))
	for _, allocation := range allocations {
		if allocation.State != "revoked" {
			ids = append(ids, allocation.Node.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

type Publisher interface {
	Publish(context.Context, string, string, []byte) error
}

type OutboxWorker struct {
	store        domain.Store
	publisher    Publisher
	logger       *zap.Logger
	pollInterval time.Duration
	retryDelay   time.Duration
	lease        time.Duration
}

func NewOutboxWorker(store domain.Store, publisher Publisher, logger *zap.Logger, pollInterval, retryDelay, lease time.Duration) *OutboxWorker {
	return &OutboxWorker{store: store, publisher: publisher, logger: logger, pollInterval: pollInterval, retryDelay: retryDelay, lease: lease}
}

func (w *OutboxWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()
	for {
		if err := w.workOnce(ctx); err != nil && ctx.Err() == nil {
			w.logger.Warn("provisioning outbox publish failed", zap.String("error_type", fmt.Sprintf("%T", err)))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *OutboxWorker) workOnce(ctx context.Context) error {
	message, ok, err := w.store.ClaimOutbox(ctx, w.lease)
	if err != nil || !ok {
		return err
	}
	publishCtx, cancel := context.WithTimeout(ctx, w.lease/2)
	defer cancel()
	if err := w.publisher.Publish(publishCtx, message.Topic, message.PartitionKey, message.Payload); err != nil {
		return w.store.RetryOutbox(ctx, message.EventID, retryBackoff(w.retryDelay, message.Attempts, message.EventID))
	}
	return w.store.CompleteOutbox(ctx, message.EventID)
}

type HealthWorker struct {
	store      domain.Store
	agents     domain.AgentClient
	logger     *zap.Logger
	interval   time.Duration
	staleAfter time.Duration
}

func NewHealthWorker(store domain.Store, agents domain.AgentClient, logger *zap.Logger, interval, staleAfter time.Duration) *HealthWorker {
	return &HealthWorker{store: store, agents: agents, logger: logger, interval: interval, staleAfter: staleAfter}
}

func (w *HealthWorker) Run(ctx context.Context) {
	w.poll(ctx)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.poll(ctx)
		}
	}
}

func (w *HealthWorker) poll(ctx context.Context) {
	nodes, err := w.store.ListNodes(ctx)
	if err != nil {
		return
	}
	for _, node := range nodes {
		status, err := w.agents.Status(ctx, node)
		if err != nil || status.NodeID != node.ID {
			_ = w.store.MarkNodeOffline(ctx, node.ID)
			continue
		}
		if err := w.store.RecordNodeHealth(ctx, status, w.staleAfter); err != nil {
			w.logger.Warn("node health persistence failed", zap.String("node_id", node.ID), zap.String("error_type", fmt.Sprintf("%T", err)))
		}
	}
}

func retryBackoff(base time.Duration, attempts int, key string) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	exponent := attempts - 1
	if exponent > 8 {
		exponent = 8
	}
	delay := base * time.Duration(1<<exponent)
	sum := sha256.Sum256([]byte(key))
	jitter := time.Duration(binary.BigEndian.Uint16(sum[:2])) * delay / 655350
	return delay + jitter
}
