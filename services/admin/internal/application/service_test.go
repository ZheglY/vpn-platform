package application

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ZheglY/vpn-platform/services/admin/internal/domain"
)

func TestExecuteDoesNotRepeatCompletedReplay(t *testing.T) {
	store := &actionStore{replay: true, action: domain.Action{ActionID: "11111111-1111-4111-8111-111111111111", Status: "succeeded", Replay: true}}
	service := New(store)
	calls := 0
	result, err := service.Execute(context.Background(), executeInput(), func(context.Context, string, string) (any, error) {
		calls++
		return map[string]any{"status": "ok"}, nil
	})
	if err != nil || calls != 0 || result.Status != "succeeded" {
		t.Fatalf("result=%+v calls=%d err=%v", result, calls, err)
	}
}

func TestExecuteRejectsSecretBearingOwnerResult(t *testing.T) {
	store := &actionStore{action: domain.Action{ActionID: "11111111-1111-4111-8111-111111111111", CorrelationID: "22222222-2222-4222-8222-222222222222", Status: "pending"}}
	service := New(store)
	_, err := service.Execute(context.Background(), executeInput(), func(context.Context, string, string) (any, error) {
		return map[string]any{"subscription_url": "https://example.invalid/s/secret"}, nil
	})
	if err == nil || store.completedCode != "unsafe_owner_response" {
		t.Fatalf("err=%v completed_code=%q", err, store.completedCode)
	}
}

func TestValidateSafePayloadRejectsNestedCredentialMaterial(t *testing.T) {
	for _, payload := range []string{
		`{"result":{"vless_client_uuid":"11111111-1111-4111-8111-111111111111"}}`,
		`{"result":{"token":"bearer-secret"}}`,
		`{"result":{"endpoint":"https://example.invalid/s/secret"}}`,
	} {
		if ValidateSafePayload([]byte(payload)) == nil {
			t.Fatalf("unsafe payload accepted: %s", payload)
		}
	}
	if err := ValidateSafePayload([]byte(`{"token_status":"active","credential_id":"11111111-1111-4111-8111-111111111111"}`)); err != nil {
		t.Fatalf("safe status rejected: %v", err)
	}
}

func TestExecutePersistsBoundedOwnerFailure(t *testing.T) {
	store := &actionStore{action: domain.Action{ActionID: "11111111-1111-4111-8111-111111111111", CorrelationID: "22222222-2222-4222-8222-222222222222", Status: "pending"}}
	service := New(store)
	_, err := service.Execute(context.Background(), executeInput(), func(context.Context, string, string) (any, error) {
		return nil, errors.New("contains upstream body and secret")
	})
	if err == nil || store.completedCode != "owner_unavailable" {
		t.Fatalf("err=%v completed_code=%q", err, store.completedCode)
	}
}

type actionStore struct {
	domain.Store
	action        domain.Action
	replay        bool
	completedCode string
}

func (s *actionStore) BeginAction(context.Context, domain.ActionInput) (domain.Action, bool, error) {
	return s.action, s.replay, nil
}
func (s *actionStore) CompleteAction(_ context.Context, _ string, result json.RawMessage, code string) (domain.Action, error) {
	s.completedCode = code
	action := s.action
	if code == "" {
		action.Status, action.Result = "succeeded", result
	} else {
		action.Status = "failed"
	}
	return action, nil
}

func executeInput() ExecuteInput {
	return ExecuteInput{
		Principal: domain.Principal{SPIFFEID: "spiffe://vpn-service/ns/local/admin/ops", Name: "ops", Roles: []string{"operations"}, Permissions: []string{"notification.retry"}},
		Action:    domain.ActionNotificationRetry, Permission: "notification.retry", TargetType: "notification",
		TargetID: "33333333-3333-4333-8333-333333333333", Reason: "retry after outage",
		IdempotencyKey: "retry-0001", RequestSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", RequestID: "request-1",
	}
}
