package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

type Check func(context.Context) error

func LivenessHandler(service string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeHealth(w, http.StatusOK, service, "ok", nil)
	})
}

func ReadinessHandler(service string, checks map[string]Check) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failures := make(map[string]string)
		for name, check := range checks {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			err := check(ctx)
			cancel()
			if err != nil {
				failures[name] = "unavailable"
			}
		}
		if len(failures) > 0 {
			writeHealth(w, http.StatusServiceUnavailable, service, "degraded", failures)
			return
		}
		writeHealth(w, http.StatusOK, service, "ready", nil)
	})
}

func writeHealth(w http.ResponseWriter, status int, service, state string, checks map[string]string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"service": service,
		"status":  state,
		"checks":  checks,
	})
}
