package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/ZheglY/vpn-platform/internal/platform/httperror"
	"github.com/ZheglY/vpn-platform/services/provisioning/internal/domain"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

type Handler struct{ store domain.Store }

func New(store domain.Store) *Handler { return &Handler{store: store} }

func (h *Handler) GetCredentialSupportSnapshot(w http.ResponseWriter, r *http.Request) {
	credentialID := strings.TrimSpace(r.PathValue("credential_id"))
	if !uuidPattern.MatchString(credentialID) {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_credential_id", "credential id is invalid")
		return
	}
	snapshot, err := h.store.GetSupportSnapshot(r.Context(), credentialID)
	if errors.Is(err, domain.ErrNotFound) {
		httperror.Write(w, r, http.StatusNotFound, "not_found", "provisioning operation was not found")
		return
	}
	if err != nil {
		httperror.Write(w, r, http.StatusInternalServerError, "provisioning_unavailable", "provisioning status is unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(snapshot)
}
