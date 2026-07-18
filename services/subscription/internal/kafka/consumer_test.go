package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	platformkafka "github.com/ZheglY/vpn-platform/internal/platform/kafka"
	"github.com/ZheglY/vpn-platform/services/subscription/internal/application"
	"github.com/ZheglY/vpn-platform/services/subscription/internal/domain"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.uber.org/zap"
)

func TestStrictDecodeRejectsUnknownAndTrailingFields(t *testing.T) {
	for _, payload := range [][]byte{
		[]byte(`{"event_id":"x","unknown":true}`),
		[]byte(`{} {}`),
	} {
		var envelope platformkafka.Envelope
		if err := strictDecode(payload, &envelope); err == nil {
			t.Fatalf("payload %s was accepted", payload)
		}
	}
}

func TestPollProcessesRecordAlongsidePartitionError(t *testing.T) {
	record := paymentRecord(t)
	fetch := fetchWithRecord(record)
	fetch[0].Topics[0].Partitions = append(fetch[0].Topics[0].Partitions, kgo.FetchPartition{Partition: 1, Err: errors.New("partition unavailable")})
	client := &fakeConsumerClient{fetch: fetch}
	store := &consumerTestStore{}
	consumer := NewConsumer(client, application.NewService(store, &consumerTestBilling{}), store, zap.NewNop(), time.Millisecond)

	if !consumer.pollOnce(context.Background()) {
		t.Fatal("consumer stopped after a non-context partition error")
	}
	if store.paymentCalls != 1 || len(client.committed) != 1 {
		t.Fatalf("payment calls=%d committed=%d", store.paymentCalls, len(client.committed))
	}
	if client.allowCalls != 1 {
		t.Fatalf("AllowRebalance calls=%d, want 1", client.allowCalls)
	}
}

func TestPollCancellationRewindsUnprocessedRecordAndAllowsRebalance(t *testing.T) {
	record := paymentRecord(t)
	started := make(chan struct{})
	client := &fakeConsumerClient{poll: func(ctx context.Context) kgo.Fetches {
		close(started)
		<-ctx.Done()
		return fetchWithRecord(record)
	}}
	store := &consumerTestStore{}
	consumer := NewConsumer(client, application.NewService(store, &consumerTestBilling{}), store, zap.NewNop(), time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool, 1)
	go func() { done <- consumer.pollOnce(ctx) }()
	<-started
	cancel()

	if keepRunning := <-done; keepRunning {
		t.Fatal("consumer kept running after context cancellation")
	}
	if store.paymentCalls != 0 || len(client.committed) != 0 {
		t.Fatalf("payment calls=%d committed=%d", store.paymentCalls, len(client.committed))
	}
	if client.allowCalls != 1 {
		t.Fatalf("AllowRebalance calls=%d, want 1", client.allowCalls)
	}
	if got := client.offset(record.Topic, record.Partition); got.Offset != record.Offset {
		t.Fatalf("rewound offset=%d, want %d", got.Offset, record.Offset)
	}
}

type fakeConsumerClient struct {
	mu         sync.Mutex
	fetch      kgo.Fetches
	poll       func(context.Context) kgo.Fetches
	allowCalls int
	committed  []*kgo.Record
	offsets    map[string]map[int32]kgo.EpochOffset
}

func (c *fakeConsumerClient) PollRecords(ctx context.Context, _ int) kgo.Fetches {
	if c.poll != nil {
		return c.poll(ctx)
	}
	return c.fetch
}

func (c *fakeConsumerClient) AllowRebalance() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.allowCalls++
}

func (c *fakeConsumerClient) CommitRecords(_ context.Context, records ...*kgo.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.committed = append(c.committed, records...)
	return nil
}

func (c *fakeConsumerClient) SetOffsets(offsets map[string]map[int32]kgo.EpochOffset) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.offsets = offsets
}

func (c *fakeConsumerClient) offset(topic string, partition int32) kgo.EpochOffset {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.offsets[topic][partition]
}

type consumerTestStore struct {
	domain.Store
	paymentCalls int
}

func (s *consumerTestStore) RecordPaymentReplay(_ context.Context, meta domain.EventMeta, _ domain.PaymentSucceeded) (bool, error) {
	s.paymentCalls++
	if meta.SourceTopic == "" || meta.PayloadSHA256 == "" {
		return false, errors.New("source metadata missing")
	}
	return true, nil
}

type consumerTestBilling struct{ domain.Billing }

func paymentRecord(t *testing.T) *kgo.Record {
	t.Helper()
	const (
		topic     = "billing.payment.succeeded.v1"
		userID    = "55555555-5555-4555-8555-555555555555"
		paymentID = "33333333-3333-4333-8333-333333333333"
	)
	paidAt := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	data, err := json.Marshal(domain.PaymentSucceeded{PaymentID: paymentID, OrderID: "44444444-4444-4444-8444-444444444444", UserID: userID, PlanID: "vpn-30d-v1", AmountMinor: 29900, Currency: "RUB", PaidAt: paidAt})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(platformkafka.Envelope{
		EventID: "11111111-1111-4111-8111-111111111111", EventType: topic, SchemaVersion: 1,
		OccurredAt: paidAt, Producer: "billing-service", CorrelationID: "22222222-2222-4222-8222-222222222222",
		AggregateType: "payment", AggregateID: paymentID, PartitionKey: "user:" + userID, Data: data,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &kgo.Record{Topic: topic, Partition: 0, Offset: 42, LeaderEpoch: 3, Key: []byte("user:" + userID), Value: payload}
}

func fetchWithRecord(record *kgo.Record) kgo.Fetches {
	return kgo.Fetches{{Topics: []kgo.FetchTopic{{Topic: record.Topic, Partitions: []kgo.FetchPartition{{Partition: record.Partition, Records: []*kgo.Record{record}}}}}}}
}
