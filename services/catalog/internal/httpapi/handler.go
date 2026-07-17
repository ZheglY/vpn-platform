package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/ZheglY/vpn-platform/internal/platform/httperror"
	"github.com/ZheglY/vpn-platform/services/catalog/internal/domain"
)

type Handler struct{ store domain.Store }

func New(store domain.Store) *Handler { return &Handler{store: store} }

func (h *Handler) ListPlans(w http.ResponseWriter, r *http.Request) {
	channel := strings.TrimSpace(r.URL.Query().Get("channel"))
	if channel == "" {
		channel = "telegram"
	}
	if channel != "telegram" || len(r.URL.Query().Get("locale")) > 32 {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_catalog_query", "catalog query is invalid")
		return
	}
	plans, err := h.store.ListPublished(r.Context(), channel)
	if err != nil {
		httperror.Write(w, r, http.StatusInternalServerError, "catalog_unavailable", "catalog is unavailable")
		return
	}
	if plans == nil {
		plans = []domain.Plan{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"plans": plans})
}

func (h *Handler) GetPlan(w http.ResponseWriter, r *http.Request) {
	planID := strings.TrimSpace(r.PathValue("plan_id"))
	region := strings.TrimSpace(r.URL.Query().Get("region"))
	if planID == "" || len(planID) > 128 || len(region) > 64 {
		httperror.Write(w, r, http.StatusBadRequest, "invalid_plan", "plan request is invalid")
		return
	}
	plan, err := h.store.GetPublished(r.Context(), planID, "telegram")
	if errors.Is(err, domain.ErrNotFound) || (err == nil && region != "" && !plan.SupportsRegion(region)) {
		httperror.Write(w, r, http.StatusNotFound, "plan_not_found", "plan was not found")
		return
	}
	if err != nil {
		httperror.Write(w, r, http.StatusInternalServerError, "catalog_unavailable", "catalog is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
