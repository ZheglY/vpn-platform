package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ZheglY/vpn-platform/internal/platform/cryptoutil"
	platformkafka "github.com/ZheglY/vpn-platform/internal/platform/kafka"
	platformpostgres "github.com/ZheglY/vpn-platform/internal/platform/postgres"
	"github.com/ZheglY/vpn-platform/services/billing/internal/domain"
)

type Store struct{ pool *pgxpool.Pool }

func Open(ctx context.Context, dsn string, options ...platformpostgres.Option) (*Store, error) {
	pool, err := platformpostgres.OpenPool(ctx, dsn, options...)
	if err != nil {
		return nil, err
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close()                         { s.pool.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func (s *Store) FindOrderReplay(ctx context.Context, userID, idempotencyKey, requestHash string) (domain.Order, bool, error) {
	resourceID, found, err := lookupIdempotency(ctx, s.pool, userID, "create_order", idempotencyKey, requestHash)
	if err != nil || !found {
		return domain.Order{}, false, err
	}
	order, err := getOrder(ctx, s.pool, userID, resourceID)
	return order, err == nil, err
}

func (s *Store) CreateOrder(ctx context.Context, input domain.CreateOrderInput) (domain.Order, bool, error) {
	orderID, err := cryptoutil.RandomUUID()
	if err != nil {
		return domain.Order{}, false, err
	}
	snapshot, err := json.Marshal(input.Snapshot)
	if err != nil {
		return domain.Order{}, false, fmt.Errorf("marshal plan snapshot: %w", err)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.Order{}, false, fmt.Errorf("begin create order: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockIdempotency(ctx, tx, input.UserID, "create_order", input.IdempotencyKey); err != nil {
		return domain.Order{}, false, err
	}
	resourceID, found, err := lookupIdempotency(ctx, tx, input.UserID, "create_order", input.IdempotencyKey, input.RequestHash)
	if err != nil {
		return domain.Order{}, false, err
	}
	if found {
		order, err := getOrder(ctx, tx, input.UserID, resourceID)
		if err != nil {
			return domain.Order{}, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.Order{}, false, fmt.Errorf("commit order replay: %w", err)
		}
		return order, false, nil
	}

	var insertedID string
	err = tx.QueryRow(ctx, `
INSERT INTO orders (id,user_id,status,amount_minor,currency,plan_id,region,accepted_terms_version,plan_snapshot)
VALUES ($1,$2,'created',$3,$4,$5,$6,$7,$8)
ON CONFLICT (user_id) WHERE status IN ('created','payment_pending') DO NOTHING
RETURNING id`, orderID, input.UserID, input.Snapshot.AmountMinor, input.Snapshot.Currency, input.Snapshot.PlanID, input.Region, input.AcceptedTermsVersion, snapshot).Scan(&insertedID)
	created := true
	if errors.Is(err, pgx.ErrNoRows) {
		created = false
		var existingSnapshot []byte
		var existingTerms string
		err = tx.QueryRow(ctx, `
SELECT id, plan_snapshot, accepted_terms_version FROM orders
WHERE user_id=$1 AND status IN ('created','payment_pending') FOR UPDATE`, input.UserID).Scan(&insertedID, &existingSnapshot, &existingTerms)
		if err == nil {
			var existing domain.PlanSnapshot
			if json.Unmarshal(existingSnapshot, &existing) != nil || !reflect.DeepEqual(existing, input.Snapshot) || existingTerms != input.AcceptedTermsVersion {
				return domain.Order{}, false, domain.ErrStateConflict
			}
		}
	}
	if err != nil {
		return domain.Order{}, false, fmt.Errorf("insert order: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO idempotency_keys (subject_id,operation,idempotency_key,request_hash,resource_type,resource_id)
VALUES ($1,'create_order',$2,$3,'order',$4)`, input.UserID, input.IdempotencyKey, input.RequestHash, insertedID); err != nil {
		return domain.Order{}, false, fmt.Errorf("store order idempotency: %w", err)
	}
	order, err := getOrder(ctx, tx, input.UserID, insertedID)
	if err != nil {
		return domain.Order{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Order{}, false, fmt.Errorf("commit create order: %w", err)
	}
	return order, created, nil
}

func (s *Store) GetOrder(ctx context.Context, userID, orderID string) (domain.Order, error) {
	return getOrder(ctx, s.pool, userID, orderID)
}

func (s *Store) FindPaymentReplay(ctx context.Context, userID, idempotencyKey, requestHash string) (domain.PaymentOperation, bool, error) {
	resourceID, found, err := lookupIdempotency(ctx, s.pool, userID, "create_payment", idempotencyKey, requestHash)
	if err != nil || !found {
		return domain.PaymentOperation{}, false, err
	}
	payment, err := getPayment(ctx, s.pool, "p.id=$1", resourceID)
	return payment, err == nil, err
}

func (s *Store) CreatePayment(ctx context.Context, input domain.CreatePaymentInput, provider string, createWindow time.Duration) (domain.PaymentOperation, bool, error) {
	paymentID, err := cryptoutil.RandomUUID()
	if err != nil {
		return domain.PaymentOperation{}, false, err
	}
	providerKey, err := cryptoutil.RandomUUID()
	if err != nil {
		return domain.PaymentOperation{}, false, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.PaymentOperation{}, false, fmt.Errorf("begin create payment: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockIdempotency(ctx, tx, input.UserID, "create_payment", input.IdempotencyKey); err != nil {
		return domain.PaymentOperation{}, false, err
	}
	resourceID, found, err := lookupIdempotency(ctx, tx, input.UserID, "create_payment", input.IdempotencyKey, input.RequestHash)
	if err != nil {
		return domain.PaymentOperation{}, false, err
	}
	if found {
		payment, err := getPayment(ctx, tx, "p.id=$1", resourceID)
		if err != nil {
			return domain.PaymentOperation{}, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.PaymentOperation{}, false, fmt.Errorf("commit payment replay: %w", err)
		}
		return payment, false, nil
	}
	if _, err := tx.Exec(ctx, `SELECT id FROM orders WHERE id=$1 AND user_id=$2 FOR UPDATE`, input.OrderID, input.UserID); err != nil {
		return domain.PaymentOperation{}, false, fmt.Errorf("lock payment order: %w", err)
	}
	order, err := getOrder(ctx, tx, input.UserID, input.OrderID)
	if err != nil {
		return domain.PaymentOperation{}, false, err
	}
	if order.Status != domain.OrderStatusCreated && order.Status != domain.OrderStatusPaymentPending {
		return domain.PaymentOperation{}, false, domain.ErrStateConflict
	}
	payment, err := getPayment(ctx, tx, "p.order_id=$1", input.OrderID)
	created := false
	if errors.Is(err, domain.ErrNotFound) {
		created = true
		deadline := time.Now().UTC().Add(createWindow)
		if _, err := tx.Exec(ctx, `
INSERT INTO payments (id,order_id,provider,provider_idempotency_key,status,amount_minor,currency,provider_create_deadline,next_reconcile_at)
VALUES ($1,$2,$3,$4,'created',$5,$6,$7,now())`, paymentID, input.OrderID, provider, providerKey, order.AmountMinor, order.Currency, deadline); err != nil {
			return domain.PaymentOperation{}, false, fmt.Errorf("insert payment: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE orders SET status='payment_pending',updated_at=now() WHERE id=$1`, input.OrderID); err != nil {
			return domain.PaymentOperation{}, false, fmt.Errorf("mark order payment pending: %w", err)
		}
		payment, err = getPayment(ctx, tx, "p.id=$1", paymentID)
	}
	if err != nil {
		return domain.PaymentOperation{}, false, err
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO idempotency_keys (subject_id,operation,idempotency_key,request_hash,resource_type,resource_id)
VALUES ($1,'create_payment',$2,$3,'payment',$4)`, input.UserID, input.IdempotencyKey, input.RequestHash, payment.PaymentID); err != nil {
		return domain.PaymentOperation{}, false, fmt.Errorf("store payment idempotency: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.PaymentOperation{}, false, fmt.Errorf("commit create payment: %w", err)
	}
	return payment, created, nil
}

func (s *Store) GetPayment(ctx context.Context, paymentID string) (domain.PaymentOperation, error) {
	return getPayment(ctx, s.pool, "p.id=$1", paymentID)
}

func (s *Store) GetPaymentByProviderID(ctx context.Context, providerID string) (domain.PaymentOperation, error) {
	return getPayment(ctx, s.pool, "p.provider_payment_id=$1", providerID)
}

func (s *Store) PrepareProviderCreate(ctx context.Context, paymentID string, observedAt time.Time) (domain.PaymentOperation, bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.PaymentOperation{}, false, fmt.Errorf("begin provider create guard: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	payment, err := getPayment(ctx, tx, "p.id=$1 FOR UPDATE OF p,o", paymentID)
	if err != nil {
		return domain.PaymentOperation{}, false, err
	}
	active := payment.Status == domain.PaymentStatusCreated || payment.Status == domain.PaymentStatusVerificationPending
	if !active || payment.ProviderPaymentID != nil {
		if err := tx.Commit(ctx); err != nil {
			return domain.PaymentOperation{}, false, fmt.Errorf("commit provider create guard: %w", err)
		}
		return payment, false, nil
	}
	if !observedAt.Before(payment.ProviderCreateDeadline) {
		if _, err := tx.Exec(ctx, `
UPDATE payments SET status='failed',confirmation_url=NULL,last_error_code='create_window_expired',reconcile_lease_until=NULL,updated_at=now()
WHERE id=$1`, paymentID); err != nil {
			return domain.PaymentOperation{}, false, fmt.Errorf("expire provider create: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE orders SET status='canceled',updated_at=now() WHERE id=$1 AND status IN ('created','payment_pending')`, payment.OrderID); err != nil {
			return domain.PaymentOperation{}, false, fmt.Errorf("cancel expired order: %w", err)
		}
		payment, err = getPayment(ctx, tx, "p.id=$1", paymentID)
		if err != nil {
			return domain.PaymentOperation{}, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.PaymentOperation{}, false, fmt.Errorf("commit expired provider create: %w", err)
		}
		return payment, false, nil
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.PaymentOperation{}, false, fmt.Errorf("commit provider create guard: %w", err)
	}
	return payment, true, nil
}

func (s *Store) ApplyProviderCreate(ctx context.Context, paymentID string, providerPayment domain.ProviderPayment) (domain.PaymentOperation, error) {
	status := domain.PaymentStatusPending
	if providerPayment.Status != "pending" {
		status = domain.PaymentStatusVerificationPending
	}
	result, err := s.pool.Exec(ctx, `
UPDATE payments SET provider_payment_id=$2,status=$3,confirmation_url=$4,next_reconcile_at=now(),reconcile_lease_until=NULL,last_error_code=NULL,updated_at=now()
WHERE id=$1 AND status IN ('created','verification_pending','pending') AND (provider_payment_id IS NULL OR provider_payment_id=$2)`, paymentID, providerPayment.ProviderPaymentID, status, providerPayment.ConfirmationURL)
	if err != nil {
		return domain.PaymentOperation{}, fmt.Errorf("apply provider create: %w", err)
	}
	if result.RowsAffected() != 1 {
		return domain.PaymentOperation{}, domain.ErrStateConflict
	}
	return s.GetPayment(ctx, paymentID)
}

func (s *Store) MarkProviderCreateAmbiguous(ctx context.Context, paymentID, code string, delay time.Duration) error {
	_, err := s.pool.Exec(ctx, `
UPDATE payments SET status='verification_pending',last_error_code=$2,next_reconcile_at=now()+$3::interval,reconcile_lease_until=NULL,updated_at=now()
WHERE id=$1 AND status IN ('created','verification_pending')`, paymentID, code, delay.String())
	if err != nil {
		return fmt.Errorf("mark provider create ambiguous: %w", err)
	}
	return nil
}

func (s *Store) MarkProviderCreateFailed(ctx context.Context, paymentID, code string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin fail payment: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var orderID string
	if err := tx.QueryRow(ctx, `UPDATE payments SET status='failed',confirmation_url=NULL,last_error_code=$2,reconcile_lease_until=NULL,updated_at=now() WHERE id=$1 AND status IN ('created','verification_pending','pending') RETURNING order_id`, paymentID, code).Scan(&orderID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("fail payment: %w", err)
	}
	if orderID != "" {
		if _, err := tx.Exec(ctx, `UPDATE orders SET status='canceled',updated_at=now() WHERE id=$1 AND status IN ('created','payment_pending')`, orderID); err != nil {
			return fmt.Errorf("cancel failed order: %w", err)
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) InsertWebhook(ctx context.Context, notification domain.WebhookNotification) (bool, error) {
	result, err := s.pool.Exec(ctx, `
INSERT INTO webhook_inbox (id,provider,event_type,provider_object_id,observed_status)
VALUES ($1,'yookassa',$2,$3,$4) ON CONFLICT DO NOTHING`, notification.InboxID, notification.EventType, notification.ProviderObjectID, notification.ObservedStatus)
	if err != nil {
		return false, fmt.Errorf("insert webhook inbox: %w", err)
	}
	return result.RowsAffected() == 1, nil
}

func (s *Store) ClaimWebhook(ctx context.Context, lease time.Duration) (domain.WebhookNotification, bool, error) {
	var notification domain.WebhookNotification
	err := s.pool.QueryRow(ctx, `
WITH candidate AS (
    SELECT id FROM webhook_inbox
    WHERE (state='pending' AND next_attempt_at<=now()) OR (state='processing' AND lease_until<now())
    ORDER BY received_at FOR UPDATE SKIP LOCKED LIMIT 1
)
UPDATE webhook_inbox i SET state='processing',attempts=attempts+1,lease_until=now()+$1::interval
FROM candidate WHERE i.id=candidate.id
RETURNING i.id,i.event_type,i.provider_object_id,i.observed_status,i.attempts`, lease.String()).Scan(&notification.InboxID, &notification.EventType, &notification.ProviderObjectID, &notification.ObservedStatus, &notification.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.WebhookNotification{}, false, nil
	}
	if err != nil {
		return domain.WebhookNotification{}, false, fmt.Errorf("claim webhook inbox: %w", err)
	}
	return notification, true, nil
}

func (s *Store) CompleteWebhook(ctx context.Context, inboxID, code string) error {
	return s.finishWebhook(ctx, inboxID, "processed", code)
}

func (s *Store) RejectWebhook(ctx context.Context, inboxID, code string) error {
	return s.finishWebhook(ctx, inboxID, "dead", code)
}

func (s *Store) finishWebhook(ctx context.Context, inboxID, state, code string) error {
	_, err := s.pool.Exec(ctx, `UPDATE webhook_inbox SET state=$2,last_error_code=NULLIF($3,''),lease_until=NULL,processed_at=now() WHERE id=$1 AND state='processing'`, inboxID, state, code)
	if err != nil {
		return fmt.Errorf("finish webhook inbox: %w", err)
	}
	return nil
}

func (s *Store) RetryWebhook(ctx context.Context, inboxID, code string, delay time.Duration) error {
	_, err := s.pool.Exec(ctx, `UPDATE webhook_inbox SET state='pending',last_error_code=$2,lease_until=NULL,next_attempt_at=now()+$3::interval WHERE id=$1 AND state='processing'`, inboxID, code, delay.String())
	if err != nil {
		return fmt.Errorf("retry webhook inbox: %w", err)
	}
	return nil
}

func (s *Store) ClaimPaymentForReconcile(ctx context.Context, lease time.Duration) (domain.PaymentOperation, bool, error) {
	var paymentID string
	err := s.pool.QueryRow(ctx, `
WITH candidate AS (
    SELECT id FROM payments
    WHERE status IN ('created','verification_pending','pending') AND next_reconcile_at<=now()
      AND (reconcile_lease_until IS NULL OR reconcile_lease_until<now())
    ORDER BY next_reconcile_at FOR UPDATE SKIP LOCKED LIMIT 1
)
UPDATE payments p SET reconcile_lease_until=now()+$1::interval,reconcile_attempts=reconcile_attempts+1
FROM candidate WHERE p.id=candidate.id RETURNING p.id`, lease.String()).Scan(&paymentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PaymentOperation{}, false, nil
	}
	if err != nil {
		return domain.PaymentOperation{}, false, fmt.Errorf("claim payment reconciliation: %w", err)
	}
	payment, err := s.GetPayment(ctx, paymentID)
	return payment, err == nil, err
}

func (s *Store) ReschedulePayment(ctx context.Context, paymentID, code string, delay time.Duration) error {
	_, err := s.pool.Exec(ctx, `UPDATE payments SET last_error_code=NULLIF($2,''),next_reconcile_at=now()+$3::interval,reconcile_lease_until=NULL,updated_at=now() WHERE id=$1 AND status IN ('created','verification_pending','pending')`, paymentID, code, delay.String())
	if err != nil {
		return fmt.Errorf("reschedule payment: %w", err)
	}
	return nil
}

func (s *Store) ApplyVerifiedPayment(ctx context.Context, expected domain.PaymentOperation, provider domain.ProviderPayment, correlationID string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin verified payment: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := getPayment(ctx, tx, "p.id=$1 FOR UPDATE OF p,o", expected.PaymentID)
	if err != nil {
		return err
	}
	if current.Status == domain.PaymentStatusSucceeded || current.Status == domain.PaymentStatusCanceled {
		if current.Status == provider.Status {
			return tx.Commit(ctx)
		}
		return domain.ErrStateConflict
	}
	if provider.Status == "pending" {
		if _, err := tx.Exec(ctx, `UPDATE payments SET status='pending',next_reconcile_at=now()+interval '1 minute',reconcile_lease_until=NULL,last_error_code=NULL,updated_at=now() WHERE id=$1`, current.PaymentID); err != nil {
			return fmt.Errorf("keep payment pending: %w", err)
		}
		return tx.Commit(ctx)
	}
	eventID, err := cryptoutil.RandomUUID()
	if err != nil {
		return err
	}
	topic, occurredAt, payload, err := buildPaymentEvent(current, provider, correlationID, eventID)
	if err != nil {
		return err
	}
	if provider.Status == "succeeded" {
		if _, err := tx.Exec(ctx, `UPDATE payments SET status='succeeded',confirmation_url=NULL,paid_at=$2,reconcile_lease_until=NULL,last_error_code=NULL,updated_at=now() WHERE id=$1`, current.PaymentID, occurredAt); err != nil {
			return fmt.Errorf("succeed payment: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE orders SET status='paid',updated_at=now() WHERE id=$1`, current.OrderID); err != nil {
			return fmt.Errorf("pay order: %w", err)
		}
	} else {
		if _, err := tx.Exec(ctx, `UPDATE payments SET status='canceled',confirmation_url=NULL,canceled_at=$2,reconcile_lease_until=NULL,last_error_code=NULL,updated_at=now() WHERE id=$1`, current.PaymentID, occurredAt); err != nil {
			return fmt.Errorf("cancel payment: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE orders SET status='canceled',updated_at=now() WHERE id=$1`, current.OrderID); err != nil {
			return fmt.Errorf("cancel order: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO outbox (event_id,topic,partition_key,aggregate_id,payload) VALUES ($1,$2,$3,$4,$5) ON CONFLICT (topic,aggregate_id) DO NOTHING`, eventID, topic, "user:"+current.UserID, current.PaymentID, payload); err != nil {
		return fmt.Errorf("insert billing outbox: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit verified payment: %w", err)
	}
	return nil
}

func buildPaymentEvent(payment domain.PaymentOperation, provider domain.ProviderPayment, correlationID, eventID string) (string, time.Time, []byte, error) {
	var topic string
	var occurredAt time.Time
	var data any
	switch provider.Status {
	case "succeeded":
		if provider.CapturedAt == nil {
			return "", time.Time{}, nil, fmt.Errorf("succeeded payment has no captured_at")
		}
		occurredAt = provider.CapturedAt.UTC()
		topic = "billing.payment.succeeded.v1"
		data = map[string]any{"payment_id": payment.PaymentID, "order_id": payment.OrderID, "user_id": payment.UserID, "plan_id": payment.PlanID, "amount_minor": payment.AmountMinor, "currency": payment.Currency, "paid_at": occurredAt}
	case "canceled":
		if provider.CanceledAt == nil {
			return "", time.Time{}, nil, fmt.Errorf("canceled payment has no observed cancellation time")
		}
		occurredAt = provider.CanceledAt.UTC()
		topic = "billing.payment.canceled.v1"
		data = map[string]any{"payment_id": payment.PaymentID, "order_id": payment.OrderID, "user_id": payment.UserID, "canceled_at": occurredAt}
	default:
		return "", time.Time{}, nil, fmt.Errorf("provider status %q is not terminal", provider.Status)
	}
	dataJSON, err := json.Marshal(data)
	if err != nil {
		return "", time.Time{}, nil, fmt.Errorf("marshal billing event data: %w", err)
	}
	envelope := platformkafka.Envelope{EventID: eventID, EventType: topic, SchemaVersion: 1, OccurredAt: occurredAt, Producer: "billing-service", CorrelationID: correlationID, AggregateType: "payment", AggregateID: payment.PaymentID, PartitionKey: "user:" + payment.UserID, Data: dataJSON}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return "", time.Time{}, nil, fmt.Errorf("marshal billing event: %w", err)
	}
	return topic, occurredAt, payload, nil
}

func (s *Store) ClaimOutbox(ctx context.Context, lease time.Duration) (domain.OutboxMessage, bool, error) {
	var message domain.OutboxMessage
	err := s.pool.QueryRow(ctx, `
WITH candidate AS (
    SELECT event_id FROM outbox
    WHERE (state='pending' AND next_attempt_at<=now()) OR (state='processing' AND lease_until<now())
    ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1
)
UPDATE outbox o SET state='processing',attempts=attempts+1,lease_until=now()+$1::interval
FROM candidate WHERE o.event_id=candidate.event_id
RETURNING o.event_id,o.topic,o.partition_key,o.payload,o.attempts`, lease.String()).Scan(&message.EventID, &message.Topic, &message.PartitionKey, &message.Payload, &message.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.OutboxMessage{}, false, nil
	}
	if err != nil {
		return domain.OutboxMessage{}, false, fmt.Errorf("claim outbox: %w", err)
	}
	return message, true, nil
}

func (s *Store) CompleteOutbox(ctx context.Context, eventID string) error {
	_, err := s.pool.Exec(ctx, `UPDATE outbox SET state='published',lease_until=NULL,published_at=now() WHERE event_id=$1 AND state='processing'`, eventID)
	if err != nil {
		return fmt.Errorf("complete outbox: %w", err)
	}
	return nil
}

func (s *Store) RetryOutbox(ctx context.Context, eventID string, delay time.Duration) error {
	_, err := s.pool.Exec(ctx, `UPDATE outbox SET state='pending',lease_until=NULL,next_attempt_at=now()+$2::interval WHERE event_id=$1 AND state='processing'`, eventID, delay.String())
	if err != nil {
		return fmt.Errorf("retry outbox: %w", err)
	}
	return nil
}

type querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func getOrder(ctx context.Context, q querier, userID, orderID string) (domain.Order, error) {
	var order domain.Order
	var snapshot []byte
	err := q.QueryRow(ctx, `SELECT id,user_id,status,amount_minor,currency,accepted_terms_version,plan_snapshot,created_at FROM orders WHERE user_id=$1 AND id=$2`, userID, orderID).Scan(&order.OrderID, &order.UserID, &order.Status, &order.AmountMinor, &order.Currency, &order.AcceptedTermsVersion, &snapshot, &order.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Order{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Order{}, fmt.Errorf("get order: %w", err)
	}
	if err := json.Unmarshal(snapshot, &order.PlanSnapshot); err != nil {
		return domain.Order{}, fmt.Errorf("decode plan snapshot: %w", err)
	}
	return order, nil
}

const paymentSelect = `SELECT p.id,p.order_id,p.status,p.amount_minor,p.currency,p.confirmation_url,p.paid_at,p.created_at,o.user_id,o.plan_id,p.provider,p.provider_payment_id,p.provider_idempotency_key,p.provider_create_deadline,p.next_reconcile_at,p.reconcile_attempts FROM payments p JOIN orders o ON o.id=p.order_id WHERE `

func getPayment(ctx context.Context, q querier, condition string, arg any) (domain.PaymentOperation, error) {
	var payment domain.PaymentOperation
	err := q.QueryRow(ctx, paymentSelect+condition, arg).Scan(&payment.PaymentID, &payment.OrderID, &payment.Status, &payment.AmountMinor, &payment.Currency, &payment.ConfirmationURL, &payment.PaidAt, &payment.CreatedAt, &payment.UserID, &payment.PlanID, &payment.Provider, &payment.ProviderPaymentID, &payment.ProviderIdempotencyKey, &payment.ProviderCreateDeadline, &payment.NextReconcileAt, &payment.ReconcileAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PaymentOperation{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.PaymentOperation{}, fmt.Errorf("get payment: %w", err)
	}
	return payment, nil
}

func lockIdempotency(ctx context.Context, tx pgx.Tx, subject, operation, key string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, subject+":"+operation+":"+key)
	if err != nil {
		return fmt.Errorf("lock idempotency key: %w", err)
	}
	return nil
}

func lookupIdempotency(ctx context.Context, q querier, subject, operation, key, requestHash string) (string, bool, error) {
	var storedHash, resourceID string
	err := q.QueryRow(ctx, `SELECT request_hash,resource_id FROM idempotency_keys WHERE subject_id=$1 AND operation=$2 AND idempotency_key=$3`, subject, operation, key).Scan(&storedHash, &resourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("lookup idempotency key: %w", err)
	}
	if storedHash != requestHash {
		return "", false, domain.ErrIdempotencyConflict
	}
	return resourceID, true, nil
}
