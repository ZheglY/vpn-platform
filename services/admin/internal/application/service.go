package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ZheglY/vpn-platform/internal/platform/cryptoutil"
	"github.com/ZheglY/vpn-platform/services/admin/internal/domain"
	"github.com/ZheglY/vpn-platform/services/admin/internal/rbac"
)

type Service struct{ store domain.Store }

func New(store domain.Store) *Service { return &Service{store: store} }

func (s *Service) Authenticate(ctx context.Context, spiffeID, principal string) (domain.Principal, error) {
	return s.store.GetPrincipal(ctx, spiffeID, principal)
}

func (s *Service) Authorize(principal domain.Principal, permission string) error {
	if !rbac.Allowed(principal.Permissions, permission) {
		return domain.ErrForbidden
	}
	return nil
}

type ExecuteInput struct {
	Principal      domain.Principal
	Action         string
	Permission     string
	TargetType     string
	TargetID       string
	Reason         string
	ReasonCode     string
	IdempotencyKey string
	RequestSHA256  string
	RequestID      string
}

type OwnerCall func(context.Context, string, string) (any, error)

func (s *Service) Execute(ctx context.Context, input ExecuteInput, call OwnerCall) (domain.Action, error) {
	if err := s.Authorize(input.Principal, input.Permission); err != nil {
		return domain.Action{}, err
	}
	actionID, err := cryptoutil.RandomUUID()
	if err != nil {
		return domain.Action{}, err
	}
	correlationID, err := cryptoutil.RandomUUID()
	if err != nil {
		return domain.Action{}, err
	}
	keyHash := sha256.Sum256([]byte(input.IdempotencyKey))
	action, replay, err := s.store.BeginAction(ctx, domain.ActionInput{
		ActionID: actionID, Actor: input.Principal, Action: input.Action, Permission: input.Permission,
		TargetType: input.TargetType, TargetID: input.TargetID, Reason: input.Reason, ReasonCode: input.ReasonCode,
		IdempotencyKeySHA256: hex.EncodeToString(keyHash[:]), RequestSHA256: input.RequestSHA256,
		RequestID: input.RequestID, CorrelationID: correlationID,
	})
	if err != nil {
		return domain.Action{}, err
	}
	if replay && action.Status != "pending" {
		return action, nil
	}
	result, ownerErr := call(ctx, action.ActionID, action.CorrelationID)
	if ownerErr != nil {
		code := ownerErrorCode(ownerErr)
		completeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		completed, completeErr := s.store.CompleteAction(completeCtx, action.ActionID, nil, code)
		cancel()
		if completeErr != nil {
			return domain.Action{}, completeErr
		}
		return completed, ownerErr
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return domain.Action{}, fmt.Errorf("encode owner result: %w", err)
	}
	if err := validateSafeResult(payload); err != nil {
		completeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		completed, completeErr := s.store.CompleteAction(completeCtx, action.ActionID, nil, "unsafe_owner_response")
		cancel()
		if completeErr != nil {
			return domain.Action{}, completeErr
		}
		return completed, &domain.OwnerError{Code: "unsafe_owner_response"}
	}
	completeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return s.store.CompleteAction(completeCtx, action.ActionID, payload, "")
}

func (s *Service) ListAudit(ctx context.Context, principal domain.Principal, limit int) ([]domain.AuditEvent, error) {
	if err := s.Authorize(principal, "audit.read"); err != nil {
		return nil, err
	}
	return s.store.ListAudit(ctx, limit)
}

func ownerErrorCode(err error) string {
	var ownerErr *domain.OwnerError
	if errors.As(err, &ownerErr) && validErrorCode(ownerErr.Code) {
		return ownerErr.Code
	}
	return "owner_unavailable"
}

func validErrorCode(value string) bool {
	if len(value) < 3 || len(value) > 64 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, char := range value[1:] {
		if char != '_' && (char < 'a' || char > 'z') && (char < '0' || char > '9') {
			return false
		}
	}
	return true
}

func validateSafeResult(payload []byte) error {
	var object any
	if err := json.Unmarshal(payload, &object); err != nil {
		return fmt.Errorf("owner result must be a JSON object")
	}
	if _, ok := object.(map[string]any); !ok {
		return fmt.Errorf("owner result must be a JSON object")
	}
	if containsForbiddenResult(object) {
		return fmt.Errorf("owner result contains forbidden field")
	}
	return nil
}

func containsForbiddenResult(value any) bool {
	forbiddenKeys := map[string]struct{}{
		"subscription_url": {}, "vless_uuid": {}, "vless_client_uuid": {}, "ciphertext": {},
		"telegram_chat_id": {}, "confirmation_url": {}, "provider_payment_id": {}, "token_hash": {},
		"access_token": {}, "token": {}, "reality_private_key": {}, "private_key": {}, "management_url": {},
	}
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if _, forbidden := forbiddenKeys[strings.ToLower(key)]; forbidden || containsForbiddenResult(nested) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if containsForbiddenResult(nested) {
				return true
			}
		}
	case string:
		lower := strings.ToLower(typed)
		return strings.Contains(lower, "vless://") || strings.Contains(lower, "/s/")
	}
	return false
}

func ValidateSafePayload(payload []byte) error { return validateSafeResult(payload) }
