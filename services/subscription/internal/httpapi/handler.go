package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/ZheglY/vpn-platform/internal/platform/httperror"
	"github.com/ZheglY/vpn-platform/services/subscription/internal/domain"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

type Handler struct{ store domain.Store }

func New(store domain.Store) *Handler { return &Handler{store: store} }

func (h *Handler) GetSubscription(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(r.PathValue("user_id"))
	if !uuidPattern.MatchString(userID) {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_user_id", "user id is invalid")
		return
	}
	subscription, err := h.store.GetSubscription(r.Context(), userID)
	if errors.Is(err, domain.ErrNotFound) {
		httperror.Write(w, r, http.StatusNotFound, "subscription_not_found", "subscription was not found")
		return
	}
	if err != nil {
		httperror.Write(w, r, http.StatusInternalServerError, "subscription_unavailable", "subscription is unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(subscription)
}
