package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/ZheglY/vpn-platform/services/provisioning/internal/domain"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

type ContractError struct{ Code string }

func (e *ContractError) Error() string { return e.Code }

func ContractErrorCode(err error) (string, bool) {
	var contract *ContractError
	if errors.As(err, &contract) {
		return contract.Code, true
	}
	if errors.Is(err, domain.ErrConflict) {
		return "durable_state_conflict", true
	}
	return "", false
}

type Service struct {
	store       domain.Store
	maxAttempts int
}

func NewService(store domain.Store, maxAttempts int) *Service {
	return &Service{store: store, maxAttempts: maxAttempts}
}

func (s *Service) ProcessEvent(ctx context.Context, meta domain.EventMeta, data json.RawMessage) error {
	kind := ""
	switch meta.EventType {
	case "access.provision.request.v1":
		kind = "provision"
	case "access.revoke.request.v1":
		kind = "revoke"
	default:
		return &ContractError{Code: "unsupported_event_type"}
	}
	var command domain.Command
	if err := strictDecode(data, &command); err != nil {
		return &ContractError{Code: "invalid_event_data"}
	}
	if !uuidPattern.MatchString(meta.EventID) || !uuidPattern.MatchString(meta.AggregateID) || !uuidPattern.MatchString(meta.CorrelationID) || !uuidPattern.MatchString(command.OperationID) || !uuidPattern.MatchString(command.CredentialID) || command.CredentialID != meta.AggregateID || command.DesiredRevision < 1 || meta.AggregateSequence != int64(command.DesiredRevision) || meta.PartitionKey != "credential:"+command.CredentialID || meta.OccurredAt.IsZero() {
		return &ContractError{Code: "event_invariant_failed"}
	}
	if meta.CausationID != nil && !uuidPattern.MatchString(*meta.CausationID) {
		return &ContractError{Code: "event_invariant_failed"}
	}
	return s.store.RecordCommand(ctx, meta, command, kind, s.maxAttempts)
}

func strictDecode(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("unexpected trailing JSON")
	}
	return nil
}

func validMaterial(material domain.CredentialMaterial, operation domain.Operation) bool {
	return material.CredentialID == operation.CredentialID && uuidPattern.MatchString(material.CredentialID) && uuidPattern.MatchString(material.SubscriptionID) && uuidPattern.MatchString(material.VLESSClientUUID) && material.Revision == operation.DesiredRevision && material.Protocol == "vless_reality"
}

func validPlacement(placement domain.Placement, subscriptionID string) bool {
	return placement.SubscriptionID == subscriptionID && uuidPattern.MatchString(placement.SubscriptionID) && uuidPattern.MatchString(placement.PeriodID) && len(strings.TrimSpace(placement.Region)) >= 2 && len(placement.Region) <= 64 && strings.TrimSpace(placement.Region) == placement.Region && placement.PrimaryNodes == 1 && placement.FailoverNodes == 1 && !placement.ValidUntil.IsZero()
}
