package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheck(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	if err := check(context.Background(), server.URL+"/ready"); err != nil {
		t.Fatalf("check: %v", err)
	}
}

func TestCheckRejectsNonHTTPAndFailureStatus(t *testing.T) {
	if err := check(context.Background(), "https://example.invalid/ready"); err == nil {
		t.Fatal("expected HTTPS endpoint to be rejected")
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "not ready", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	if err := check(context.Background(), server.URL); err == nil {
		t.Fatal("expected failure status")
	}
}
