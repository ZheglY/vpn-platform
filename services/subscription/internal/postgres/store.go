package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ZheglY/vpn-platform/internal/platform/cryptoutil"
	platformkafka "github.com/ZheglY/vpn-platform/internal/platform/kafka"
	platformpostgres "github.com/ZheglY/vpn-platform/internal/platform/postgres"
	"github.com/ZheglY/vpn-platform/services/subscription/internal/domain"
)

type Store struct{ pool *pgxpool.Pool }

func Open(ctx context.Context, dsn string) (*Store, error) {
	pool, err := platformpostgres.OpenPool(ctx, dsn)
	if err != nil {
		return nil, err
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close()                         { s.pool.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func (s *Store) RecordPaymentReplay(ctx context.Context, meta domain.EventMeta, payment domain.PaymentSucceeded, now time.Time) (bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin payment replay: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockUser(ctx, tx, payment.UserID); err != nil {
		return false, err
	}
	var userID, orderID string
	err = tx.QueryRow(ctx, `
SELECT s.user_id, p.source_order_id
FROM subscription_periods p JOIN subscriptions s ON s.id = p.subscription_id
WHERE p.source_payment_id = $1`, payment.PaymentID).Scan(&userID, &orderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check subscription payment replay: %w", err)
	}
	if userID != payment.UserID || orderID != payment.OrderID {
		return false, domain.ErrConflict
	}
	inserted, err := insertInbox(ctx, tx, meta, payment.PaymentID, payment.UserID, nil, "processed")
	if err != nil {
		return false, err
	}
	if inserted {
		if _, err := tx.Exec(ctx, `UPDATE inbox SET processed_at = $2 WHERE event_id = $1`, meta.EventID, now); err != nil {
			return false, fmt.Errorf("complete payment replay inbox: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit payment replay: %w", err)
	}
	return true, nil
}

func (s *Store) ApplyPayment(ctx context.Context, meta domain.EventMeta, payment domain.PaymentSucceeded, order domain.Order, now time.Time) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin apply payment: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockUser(ctx, tx, payment.UserID); err != nil {
		return err
	}
	inserted, err := insertInbox(ctx, tx, meta, payment.PaymentID, payment.UserID, nil, "processing")
	if err != nil {
		return err
	}
	if !inserted {
		return tx.Commit(ctx)
	}
	var existingUser, existingOrder string
	err = tx.QueryRow(ctx, `
SELECT s.user_id, p.source_order_id
FROM subscription_periods p JOIN subscriptions s ON s.id = p.subscription_id
WHERE p.source_payment_id = $1`, payment.PaymentID).Scan(&existingUser, &existingOrder)
	if err == nil {
		if existingUser != payment.UserID || existingOrder != payment.OrderID {
			return domain.ErrConflict
		}
		if err := markInboxProcessed(ctx, tx, meta.EventID, now); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("check existing subscription period: %w", err)
	}

	subscription, created, err := getOrCreateSubscription(ctx, tx, payment.UserID, now)
	if err != nil {
		return err
	}
	eventType := "subscription.activated.v1"
	periodStart := payment.PaidAt.UTC()
	if !created && (subscription.Status == domain.StatusActive || subscription.Status == domain.StatusGrace) && subscription.CurrentPeriodEnd != nil && subscription.GraceEndsAt != nil && now.Before(*subscription.GraceEndsAt) {
		eventType = "subscription.extended.v1"
		periodStart = subscription.CurrentPeriodEnd.UTC()
	}
	periodEnd := periodStart.Add(time.Duration(order.PlanSnapshot.DurationDays) * 24 * time.Hour)
	graceEndsAt := periodEnd.Add(time.Duration(order.PlanSnapshot.GracePeriodHours) * time.Hour)
	periodID, err := cryptoutil.RandomUUID()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO subscription_periods (
    id, subscription_id, source_order_id, source_payment_id, plan_id, region,
    amount_minor, currency, duration_days, grace_period_hours, paid_at,
    period_start, period_end, grace_ends_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		periodID, subscription.SubscriptionID, payment.OrderID, payment.PaymentID,
		payment.PlanID, order.PlanSnapshot.Region, payment.AmountMinor, payment.Currency,
		order.PlanSnapshot.DurationDays, order.PlanSnapshot.GracePeriodHours, payment.PaidAt,
		periodStart, periodEnd, graceEndsAt); err != nil {
		return fmt.Errorf("insert subscription period: %w", err)
	}

	start := periodStart
	if eventType == "subscription.extended.v1" && subscription.CurrentPeriodStart != nil {
		start = *subscription.CurrentPeriodStart
	}
	status, next := statusAt(now, start, periodEnd, graceEndsAt)
	if status == domain.StatusExpired {
		// Preserve event order for a payment delivered after its grace boundary.
		// The scheduler immediately follows the activation fact with expiry.
		status, next = domain.StatusActive, timePtr(now)
	}
	if err := updateSubscription(ctx, tx, subscription.SubscriptionID, status, start, periodEnd, graceEndsAt, next, now); err != nil {
		return err
	}
	data := periodEventData{SubscriptionID: subscription.SubscriptionID, UserID: payment.UserID, PeriodID: periodID, SourceOrderID: payment.OrderID, SourcePaymentID: payment.PaymentID, PeriodStart: periodStart, PeriodEnd: periodEnd, GraceEndsAt: graceEndsAt}
	if err := insertOutbox(ctx, tx, eventType, subscription.SubscriptionID, payment.UserID, "payment:"+payment.PaymentID+":"+eventType, meta.CorrelationID, &meta.EventID, now, data); err != nil {
		return err
	}
	if err := markInboxProcessed(ctx, tx, meta.EventID, now); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit apply payment: %w", err)
	}
	return nil
}

func statusAt(now, start, end, grace time.Time) (string, *time.Time) {
	if now.Before(start) {
		return domain.StatusPending, timePtr(start)
	}
	if now.Before(end) {
		return domain.StatusActive, timePtr(end)
	}
	if now.Before(grace) {
		return domain.StatusGrace, timePtr(grace)
	}
	return domain.StatusExpired, nil
}

func (s *Store) StoreRefund(ctx context.Context, meta domain.EventMeta, refund domain.RefundSucceeded, now time.Time) error {
	payload, err := json.Marshal(refund)
	if err != nil {
		return fmt.Errorf("encode normalized refund: %w", err)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin store refund: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockUser(ctx, tx, refund.UserID); err != nil {
		return err
	}
	inserted, err := insertInbox(ctx, tx, meta, refund.PaymentID, refund.UserID, payload, "pending")
	if err != nil || !inserted {
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	applied, err := applyRefund(ctx, tx, meta, refund, now)
	if err != nil {
		return err
	}
	if applied {
		if err := markInboxProcessed(ctx, tx, meta.EventID, now); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit store refund: %w", err)
	}
	return nil
}

func (s *Store) ClaimRefund(ctx context.Context, lease time.Duration) (domain.RefundWork, bool, error) {
	var work domain.RefundWork
	var payload []byte
	err := s.pool.QueryRow(ctx, `
WITH candidate AS (
    SELECT event_id FROM inbox
    WHERE event_type = 'billing.refund.succeeded.v1'
      AND state IN ('pending', 'processing')
      AND next_attempt_at <= now()
      AND (lease_until IS NULL OR lease_until <= now())
    ORDER BY received_at
    FOR UPDATE SKIP LOCKED LIMIT 1
)
UPDATE inbox i SET state = 'processing', lease_until = now() + $1::interval, attempts = attempts + 1
FROM candidate c WHERE i.event_id = c.event_id
RETURNING i.event_id, i.correlation_id, i.aggregate_id, i.event_type, i.payload, i.attempts`, lease).Scan(
		&work.InboxID, &work.Meta.CorrelationID, &work.Meta.AggregateID, &work.Meta.EventType, &payload, &work.Attempts,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RefundWork{}, false, nil
	}
	if err != nil {
		return domain.RefundWork{}, false, fmt.Errorf("claim refund inbox: %w", err)
	}
	work.Meta.EventID = work.InboxID
	if err := json.Unmarshal(payload, &work.Refund); err != nil {
		return domain.RefundWork{}, false, fmt.Errorf("decode normalized refund: %w", err)
	}
	return work, true, nil
}

func (s *Store) ApplyClaimedRefund(ctx context.Context, work domain.RefundWork, now time.Time, retryDelay time.Duration) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin reconcile refund: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockUser(ctx, tx, work.Refund.UserID); err != nil {
		return err
	}
	applied, err := applyRefund(ctx, tx, work.Meta, work.Refund, now)
	if err != nil {
		return err
	}
	if applied {
		if err := markInboxProcessed(ctx, tx, work.InboxID, now); err != nil {
			return err
		}
	} else if _, err := tx.Exec(ctx, `UPDATE inbox SET state = 'pending', lease_until = NULL, next_attempt_at = $2, last_error_code = 'payment_not_received' WHERE event_id = $1`, work.InboxID, now.Add(retryDelay)); err != nil {
		return fmt.Errorf("reschedule refund inbox: %w", err)
	}
	return tx.Commit(ctx)
}

func applyRefund(ctx context.Context, tx pgx.Tx, meta domain.EventMeta, refund domain.RefundSucceeded, now time.Time) (bool, error) {
	var periodID, subscriptionID, userID, orderID, status, currency string
	var amount int64
	err := tx.QueryRow(ctx, `
SELECT p.id, p.subscription_id, s.user_id, p.source_order_id, p.status, p.amount_minor, p.currency
FROM subscription_periods p JOIN subscriptions s ON s.id = p.subscription_id
WHERE p.source_payment_id = $1 FOR UPDATE OF p, s`, refund.PaymentID).Scan(&periodID, &subscriptionID, &userID, &orderID, &status, &amount, &currency)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load refunded period: %w", err)
	}
	if userID != refund.UserID || orderID != refund.OrderID || amount != refund.AmountMinor || currency != refund.Currency {
		return false, domain.ErrConflict
	}
	if status == "refunded" {
		var storedRefund string
		if err := tx.QueryRow(ctx, `SELECT refund_id FROM subscription_periods WHERE id = $1`, periodID).Scan(&storedRefund); err != nil {
			return false, fmt.Errorf("load existing refund: %w", err)
		}
		if storedRefund != refund.RefundID {
			return false, domain.ErrConflict
		}
		return true, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE subscription_periods SET status = 'refunded', refund_id = $2, refunded_at = $3 WHERE id = $1`, periodID, refund.RefundID, refund.RefundedAt); err != nil {
		return false, fmt.Errorf("mark period refunded: %w", err)
	}
	state, found, err := calculateEntitlement(ctx, tx, subscriptionID, now)
	if err != nil {
		return false, err
	}
	if !found {
		if _, err := tx.Exec(ctx, `UPDATE subscriptions SET status = 'revoked', current_period_start = NULL, current_period_end = NULL, grace_ends_at = NULL, next_transition_at = NULL, transition_lease_until = NULL, updated_at = $2 WHERE id = $1`, subscriptionID, now); err != nil {
			return false, fmt.Errorf("revoke subscription: %w", err)
		}
		data := terminalEventData{SubscriptionID: subscriptionID, UserID: userID, Reason: "refund", AffectedPeriodIDs: []string{periodID}, EffectiveAt: refund.RefundedAt}
		if err := insertOutbox(ctx, tx, "subscription.revoked.v1", subscriptionID, userID, "refund:"+refund.RefundID, meta.CorrelationID, &meta.EventID, now, data); err != nil {
			return false, err
		}
	} else if err := updateSubscription(ctx, tx, subscriptionID, state.Status, state.Start, state.End, state.Grace, state.Next, now); err != nil {
		return false, err
	}
	return true, nil
}

type entitlementState struct {
	Status string
	Start  time.Time
	End    time.Time
	Grace  time.Time
	Next   *time.Time
	Period periodRow
}

type periodRow struct {
	ID, OrderID, PaymentID string
	Start, End, Grace      time.Time
}

func calculateEntitlement(ctx context.Context, tx pgx.Tx, subscriptionID string, now time.Time) (entitlementState, bool, error) {
	rows, err := tx.Query(ctx, `
SELECT id, source_order_id, source_payment_id, period_start, period_end, grace_ends_at
FROM subscription_periods
WHERE subscription_id = $1 AND status = 'paid' AND grace_ends_at > $2
ORDER BY period_start, id`, subscriptionID, now)
	if err != nil {
		return entitlementState{}, false, fmt.Errorf("list valid periods: %w", err)
	}
	defer rows.Close()
	var periods []periodRow
	for rows.Next() {
		var period periodRow
		if err := rows.Scan(&period.ID, &period.OrderID, &period.PaymentID, &period.Start, &period.End, &period.Grace); err != nil {
			return entitlementState{}, false, fmt.Errorf("scan valid period: %w", err)
		}
		periods = append(periods, period)
	}
	if err := rows.Err(); err != nil {
		return entitlementState{}, false, fmt.Errorf("iterate valid periods: %w", err)
	}
	if len(periods) == 0 {
		return entitlementState{}, false, nil
	}
	base := 0
	for i := range periods {
		if !now.Before(periods[i].Start) && now.Before(periods[i].End) {
			base = i
		}
	}
	if now.Before(periods[base].Start) {
		base = 0
	} else if !now.Before(periods[base].End) {
		for i := range periods {
			if !now.Before(periods[i].End) && now.Before(periods[i].Grace) {
				base = i
			}
		}
	}
	selected := periods[base]
	chainEnd, chainGrace := selected.End, selected.Grace
	for i := base + 1; i < len(periods); i++ {
		if periods[i].Start.After(chainEnd) {
			break
		}
		if periods[i].End.After(chainEnd) {
			chainEnd, chainGrace = periods[i].End, periods[i].Grace
		}
	}
	status, next := statusAt(now, selected.Start, chainEnd, chainGrace)
	return entitlementState{Status: status, Start: selected.Start, End: chainEnd, Grace: chainGrace, Next: next, Period: selected}, true, nil
}

func (s *Store) ClaimDue(ctx context.Context, now time.Time, lease time.Duration) (domain.Subscription, bool, error) {
	var subscription domain.Subscription
	err := s.pool.QueryRow(ctx, `
WITH candidate AS (
    SELECT id FROM subscriptions
    WHERE status IN ('pending', 'active', 'grace') AND next_transition_at <= $1
      AND (transition_lease_until IS NULL OR transition_lease_until <= $1)
    ORDER BY next_transition_at, id FOR UPDATE SKIP LOCKED LIMIT 1
)
UPDATE subscriptions s SET transition_lease_until = $1 + $2::interval
FROM candidate c WHERE s.id = c.id
RETURNING s.id, s.user_id, s.status, s.current_period_start, s.current_period_end, s.grace_ends_at`, now, lease).Scan(
		&subscription.SubscriptionID, &subscription.UserID, &subscription.Status, &subscription.CurrentPeriodStart, &subscription.CurrentPeriodEnd, &subscription.GraceEndsAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Subscription{}, false, nil
	}
	if err != nil {
		return domain.Subscription{}, false, fmt.Errorf("claim due subscription: %w", err)
	}
	return subscription, true, nil
}

func (s *Store) CompleteDue(ctx context.Context, subscriptionID string, now time.Time) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin complete transition: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var userID, oldStatus string
	var leaseUntil *time.Time
	var oldGraceEndsAt *time.Time
	if err := tx.QueryRow(ctx, `SELECT user_id, status, transition_lease_until, grace_ends_at FROM subscriptions WHERE id = $1 FOR UPDATE`, subscriptionID).Scan(&userID, &oldStatus, &leaseUntil, &oldGraceEndsAt); err != nil {
		return fmt.Errorf("lock due subscription: %w", err)
	}
	if leaseUntil == nil {
		return nil
	}
	state, found, err := calculateEntitlement(ctx, tx, subscriptionID, now)
	if err != nil {
		return err
	}
	if !found {
		if oldStatus != domain.StatusExpired && oldStatus != domain.StatusRevoked {
			effectiveAt := now
			dedupeBoundary := now.UTC().Format(time.RFC3339Nano)
			if oldGraceEndsAt != nil && !oldGraceEndsAt.After(now) {
				effectiveAt = *oldGraceEndsAt
				dedupeBoundary = oldGraceEndsAt.UTC().Format(time.RFC3339Nano)
			}
			data := terminalEventData{SubscriptionID: subscriptionID, UserID: userID, Reason: "expired", EffectiveAt: effectiveAt}
			if err := insertOutbox(ctx, tx, "subscription.expired.v1", subscriptionID, userID, "expired:"+subscriptionID+":"+dedupeBoundary, "", nil, now, data); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE subscriptions SET status = 'expired', next_transition_at = NULL, transition_lease_until = NULL, updated_at = $2 WHERE id = $1`, subscriptionID, now); err != nil {
			return fmt.Errorf("expire subscription: %w", err)
		}
		return tx.Commit(ctx)
	}
	if oldStatus != domain.StatusPending && state.Status == domain.StatusPending {
		data := terminalEventData{SubscriptionID: subscriptionID, UserID: userID, Reason: "expired", EffectiveAt: now}
		if err := insertOutbox(ctx, tx, "subscription.expired.v1", subscriptionID, userID, "gap-expired:"+state.Period.ID, "", nil, now, data); err != nil {
			return err
		}
	}
	if oldStatus == domain.StatusPending && state.Status == domain.StatusActive {
		var data periodEventData
		if err := tx.QueryRow(ctx, `SELECT $1, $2, id, source_order_id, source_payment_id, period_start, period_end, grace_ends_at FROM subscription_periods WHERE id = $3`, subscriptionID, userID, state.Period.ID).Scan(&data.SubscriptionID, &data.UserID, &data.PeriodID, &data.SourceOrderID, &data.SourcePaymentID, &data.PeriodStart, &data.PeriodEnd, &data.GraceEndsAt); err != nil {
			return fmt.Errorf("load activation period: %w", err)
		}
		if err := insertOutbox(ctx, tx, "subscription.activated.v1", subscriptionID, userID, "transition-activated:"+state.Period.ID, "", nil, now, data); err != nil {
			return err
		}
	}
	if err := updateSubscription(ctx, tx, subscriptionID, state.Status, state.Start, state.End, state.Grace, state.Next, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) GetSubscription(ctx context.Context, userID string) (domain.Subscription, error) {
	var subscription domain.Subscription
	err := s.pool.QueryRow(ctx, `SELECT id, user_id, status, current_period_start, current_period_end, grace_ends_at FROM subscriptions WHERE user_id = $1`, userID).Scan(
		&subscription.SubscriptionID, &subscription.UserID, &subscription.Status, &subscription.CurrentPeriodStart, &subscription.CurrentPeriodEnd, &subscription.GraceEndsAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Subscription{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Subscription{}, fmt.Errorf("get subscription: %w", err)
	}
	return subscription, nil
}

func (s *Store) RecordDeadLetter(ctx context.Context, topic string, partition int32, offset int64, payloadHash, reason string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO consumer_dead_letters (topic, partition, record_offset, payload_sha256, reason_code) VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, topic, partition, offset, payloadHash, reason)
	if err != nil {
		return fmt.Errorf("record consumer dead letter: %w", err)
	}
	return nil
}

func (s *Store) ClaimOutbox(ctx context.Context, lease time.Duration) (domain.OutboxMessage, bool, error) {
	var message domain.OutboxMessage
	err := s.pool.QueryRow(ctx, `
WITH candidate AS (
    SELECT event_id FROM outbox WHERE state IN ('pending','processing') AND next_attempt_at <= now()
      AND (lease_until IS NULL OR lease_until <= now())
    ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1
)
UPDATE outbox o SET state = 'processing', lease_until = now() + $1::interval, attempts = attempts + 1
FROM candidate c WHERE o.event_id = c.event_id
RETURNING o.event_id, o.topic, o.partition_key, o.payload, o.attempts`, lease).Scan(&message.EventID, &message.Topic, &message.PartitionKey, &message.Payload, &message.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.OutboxMessage{}, false, nil
	}
	if err != nil {
		return domain.OutboxMessage{}, false, fmt.Errorf("claim subscription outbox: %w", err)
	}
	return message, true, nil
}

func (s *Store) CompleteOutbox(ctx context.Context, eventID string) error {
	_, err := s.pool.Exec(ctx, `UPDATE outbox SET state = 'published', lease_until = NULL, published_at = now() WHERE event_id = $1 AND state = 'processing'`, eventID)
	if err != nil {
		return fmt.Errorf("complete subscription outbox: %w", err)
	}
	return nil
}

func (s *Store) RetryOutbox(ctx context.Context, eventID string, delay time.Duration) error {
	_, err := s.pool.Exec(ctx, `UPDATE outbox SET state = 'pending', lease_until = NULL, next_attempt_at = now() + $2::interval WHERE event_id = $1 AND state = 'processing'`, eventID, delay)
	if err != nil {
		return fmt.Errorf("retry subscription outbox: %w", err)
	}
	return nil
}

func getOrCreateSubscription(ctx context.Context, tx pgx.Tx, userID string, now time.Time) (domain.Subscription, bool, error) {
	id, err := cryptoutil.RandomUUID()
	if err != nil {
		return domain.Subscription{}, false, err
	}
	command, err := tx.Exec(ctx, `INSERT INTO subscriptions (id, user_id, status, created_at, updated_at) VALUES ($1,$2,'pending',$3,$3) ON CONFLICT (user_id) DO NOTHING`, id, userID, now)
	if err != nil {
		return domain.Subscription{}, false, fmt.Errorf("create subscription: %w", err)
	}
	created := command.RowsAffected() == 1
	var subscription domain.Subscription
	if err := tx.QueryRow(ctx, `SELECT id, user_id, status, current_period_start, current_period_end, grace_ends_at FROM subscriptions WHERE user_id = $1 FOR UPDATE`, userID).Scan(
		&subscription.SubscriptionID, &subscription.UserID, &subscription.Status, &subscription.CurrentPeriodStart, &subscription.CurrentPeriodEnd, &subscription.GraceEndsAt,
	); err != nil {
		return domain.Subscription{}, false, fmt.Errorf("lock subscription: %w", err)
	}
	return subscription, created, nil
}

func insertInbox(ctx context.Context, tx pgx.Tx, meta domain.EventMeta, paymentID, userID string, payload []byte, state string) (bool, error) {
	command, err := tx.Exec(ctx, `
INSERT INTO inbox (event_id, event_type, aggregate_id, source_payment_id, user_id, correlation_id, payload, state)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (event_id) DO NOTHING`, meta.EventID, meta.EventType, meta.AggregateID, paymentID, userID, meta.CorrelationID, payload, state)
	if err != nil {
		return false, fmt.Errorf("insert subscription inbox: %w", err)
	}
	return command.RowsAffected() == 1, nil
}

func markInboxProcessed(ctx context.Context, tx pgx.Tx, eventID string, now time.Time) error {
	if _, err := tx.Exec(ctx, `UPDATE inbox SET state = 'processed', payload = NULL, lease_until = NULL, processed_at = $2, last_error_code = NULL WHERE event_id = $1`, eventID, now); err != nil {
		return fmt.Errorf("complete subscription inbox: %w", err)
	}
	return nil
}

func lockUser(ctx context.Context, tx pgx.Tx, userID string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, userID); err != nil {
		return fmt.Errorf("lock subscription user: %w", err)
	}
	return nil
}

func updateSubscription(ctx context.Context, tx pgx.Tx, id, status string, start, end, grace time.Time, next *time.Time, now time.Time) error {
	_, err := tx.Exec(ctx, `UPDATE subscriptions SET status=$2, current_period_start=$3, current_period_end=$4, grace_ends_at=$5, next_transition_at=$6, transition_lease_until=NULL, updated_at=$7 WHERE id=$1`, id, status, start, end, grace, next, now)
	if err != nil {
		return fmt.Errorf("update subscription state: %w", err)
	}
	return nil
}

type periodEventData struct {
	SubscriptionID  string    `json:"subscription_id"`
	UserID          string    `json:"user_id"`
	PeriodID        string    `json:"period_id"`
	SourceOrderID   string    `json:"source_order_id"`
	SourcePaymentID string    `json:"source_payment_id"`
	PeriodStart     time.Time `json:"period_start"`
	PeriodEnd       time.Time `json:"period_end"`
	GraceEndsAt     time.Time `json:"grace_ends_at"`
}

type terminalEventData struct {
	SubscriptionID    string    `json:"subscription_id"`
	UserID            string    `json:"user_id"`
	Reason            string    `json:"reason"`
	AffectedPeriodIDs []string  `json:"affected_period_ids,omitempty"`
	EffectiveAt       time.Time `json:"effective_at"`
}

func insertOutbox(ctx context.Context, tx pgx.Tx, topic, subscriptionID, userID, dedupeKey, correlationID string, causationID *string, occurredAt time.Time, data any) error {
	eventID, err := cryptoutil.RandomUUID()
	if err != nil {
		return err
	}
	payload, err := buildEventPayload(topic, eventID, subscriptionID, userID, correlationID, causationID, occurredAt, data)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO outbox (event_id, topic, partition_key, aggregate_id, dedupe_key, payload) VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (dedupe_key) DO NOTHING`, eventID, topic, "user:"+userID, subscriptionID, dedupeKey, payload); err != nil {
		return fmt.Errorf("insert subscription outbox: %w", err)
	}
	return nil
}

func buildEventPayload(topic, eventID, subscriptionID, userID, correlationID string, causationID *string, occurredAt time.Time, data any) ([]byte, error) {
	if correlationID == "" {
		correlationID = eventID
	}
	dataJSON, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("encode subscription event data: %w", err)
	}
	envelope := platformkafka.Envelope{EventID: eventID, EventType: topic, SchemaVersion: 1, OccurredAt: occurredAt.UTC(), Producer: "subscription-service", CorrelationID: correlationID, CausationID: causationID, AggregateType: "subscription", AggregateID: subscriptionID, PartitionKey: "user:" + userID, Data: dataJSON}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("encode subscription event envelope: %w", err)
	}
	return payload, nil
}

func timePtr(value time.Time) *time.Time { return &value }
