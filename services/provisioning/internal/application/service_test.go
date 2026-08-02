package application

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ZheglY/vpn-platform/services/provisioning/internal/domain"
)

func TestProcessEventRejectsCommandSequenceDifferentFromDesiredRevision(t *testing.T) {
	store := &commandStore{}
	service := NewService(store, 5)
	credentialID := "62000000-0000-4000-8000-000000000001"
	meta := domain.EventMeta{
		EventID: "63000000-0000-4000-8000-000000000001", EventType: "access.revoke.request.v1",
		AggregateID: credentialID, AggregateSequence: 3, PartitionKey: "credential:" + credentialID,
		CorrelationID: "64000000-0000-4000-8000-000000000001", OccurredAt: time.Now().UTC(),
	}
	data, err := json.Marshal(domain.Command{
		OperationID: "65000000-0000-4000-8000-000000000001", CredentialID: credentialID, DesiredRevision: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = service.ProcessEvent(context.Background(), meta, data)
	var contractErr *ContractError
	if !errors.As(err, &contractErr) || contractErr.Code != "event_invariant_failed" {
		t.Fatalf("sequence mismatch = %v", err)
	}
	if store.calls != 0 {
		t.Fatalf("sequence mismatch reached store %d time(s)", store.calls)
	}
}

type commandStore struct {
	domain.Store
	calls int
}

func (s *commandStore) RecordCommand(context.Context, domain.EventMeta, domain.Command, string, int) error {
	s.calls++
	return nil
}
