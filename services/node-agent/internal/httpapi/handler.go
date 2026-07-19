package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/ZheglY/vpn-platform/internal/platform/httperror"
	"github.com/ZheglY/vpn-platform/services/node-agent/internal/domain"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

type Service interface {
	Apply(context.Context, domain.DesiredState) (domain.ApplyResult, error)
	Status(context.Context) domain.Status
	CredentialState(string) (domain.CredentialState, error)
	Healthy() bool
}

type Handler struct{ service Service }

func New(service Service) *Handler { return &Handler{service: service} }

func (h *Handler) ApplyDesiredState(w http.ResponseWriter, r *http.Request) {
	credentialID := strings.TrimSpace(r.PathValue("credential_id"))
	if !uuidPattern.MatchString(credentialID) {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_credential_id", "credential id is invalid")
		return
	}
	var desired domain.DesiredState
	if err := strictDecode(r.Body, &desired); err != nil || desired.CredentialID != credentialID {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_desired_state", "desired state is invalid")
		return
	}
	result, err := h.service.Apply(r.Context(), desired)
	if errors.Is(err, domain.ErrStale) {
		httperror.Write(w, r, http.StatusConflict, "stale_revision", "desired revision is stale")
		return
	}
	if errors.Is(err, domain.ErrConflict) {
		httperror.Write(w, r, http.StatusConflict, "operation_conflict", "operation conflicts with current state")
		return
	}
	if err != nil {
		httperror.Write(w, r, http.StatusServiceUnavailable, "apply_failed", "desired state could not be applied")
		return
	}
	writeJSON(w, result)
}

func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, h.service.Status(r.Context()))
}

func (h *Handler) CredentialState(w http.ResponseWriter, r *http.Request) {
	credentialID := strings.TrimSpace(r.PathValue("credential_id"))
	if !uuidPattern.MatchString(credentialID) {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_credential_id", "credential id is invalid")
		return
	}
	state, err := h.service.CredentialState(credentialID)
	if err != nil {
		httperror.Write(w, r, http.StatusInternalServerError, "state_unavailable", "credential state is unavailable")
		return
	}
	writeJSON(w, state)
}

func (h *Handler) Readiness(w http.ResponseWriter, r *http.Request) {
	if !h.service.Healthy() {
		httperror.Write(w, r, http.StatusServiceUnavailable, "not_ready", "node is not ready")
		return
	}
	writeJSON(w, map[string]string{"status": "ready"})
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(value)
}

func strictDecode(reader io.Reader, target any) error {
	const maxBodyBytes = 64 << 10
	var buffer bytes.Buffer
	if _, err := io.Copy(&buffer, io.LimitReader(reader, maxBodyBytes+1)); err != nil {
		return err
	}
	if buffer.Len() > maxBodyBytes {
		return fmt.Errorf("request body exceeds limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(buffer.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("unexpected trailing JSON")
	}
	return nil
}
