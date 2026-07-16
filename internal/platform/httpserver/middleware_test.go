package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"

	"github.com/ZheglY/vpn-platform/internal/platform/requestid"
)

func TestRequestIDMiddlewareGeneratesRequestID(t *testing.T) {
	handler := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requestid.FromRequest(r) == "" {
			t.Fatal("request ID missing from context")
		}
		w.WriteHeader(http.StatusNoContent)
	}), RequestID)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/livez", nil))

	if got := rec.Header().Get(requestid.Header); !requestid.Valid(got) {
		t.Fatalf("response request ID %q is invalid", got)
	}
}

func TestRecoverMiddlewareWritesSafeError(t *testing.T) {
	handler := Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}), RequestID, Recover(zap.NewNop()))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panic", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if body := rec.Body.String(); body == "" || body == "boom" {
		t.Fatalf("unsafe or empty error response: %q", body)
	}
}
