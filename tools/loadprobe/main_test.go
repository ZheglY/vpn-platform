package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLoadProbePassesWithinBudget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	result, err := run(context.Background(), probeConfig{
		URL: server.URL, Duration: time.Second, RequestsPerSecond: 20, Concurrency: 4,
		P95Budget: 100 * time.Millisecond, MaxErrorRatio: 0, ExpectedStatuses: []int{http.StatusOK},
	})
	if err != nil || result.Result != "passed" || result.Requests < 10 {
		t.Fatalf("probe = %+v, err=%v", result, err)
	}
}

func TestLoadProbeFailsClosedOnErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	result, err := run(context.Background(), probeConfig{
		URL: server.URL, Duration: time.Second, RequestsPerSecond: 10, Concurrency: 2,
		P95Budget: time.Second, MaxErrorRatio: 0, ExpectedStatuses: []int{http.StatusOK},
	})
	if err == nil || result.Result != "failed" || result.Errors == 0 {
		t.Fatalf("probe = %+v, err=%v", result, err)
	}
}

func TestLoadProbeAcceptsBoundedExpectedStatusSet(t *testing.T) {
	statuses, err := parseExpectedStatuses("404, 429")
	if err != nil {
		t.Fatalf("parse expected statuses: %v", err)
	}
	for _, status := range []int{http.StatusNotFound, http.StatusTooManyRequests} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		}))
		result, runErr := run(context.Background(), probeConfig{
			URL: server.URL, Duration: time.Second, RequestsPerSecond: 10, Concurrency: 2,
			P95Budget: time.Second, MaxErrorRatio: 0, ExpectedStatuses: statuses,
		})
		server.Close()
		if runErr != nil || result.Result != "passed" {
			t.Fatalf("status %d: probe = %+v, err=%v", status, result, runErr)
		}
	}
}

func TestExpectedStatusSetRejectsInvalidOrUnboundedValues(t *testing.T) {
	for _, value := range []string{"", "99", "600", "404,404", "200,201,202,203,204,205,206,207,208"} {
		if _, err := parseExpectedStatuses(value); err == nil {
			t.Fatalf("parseExpectedStatuses(%q) unexpectedly succeeded", value)
		}
	}
}

func TestPercentileUsesNearestRank(t *testing.T) {
	values := []time.Duration{time.Millisecond, 2 * time.Millisecond, 100 * time.Millisecond}
	if got := percentile(values, 0.95); got != 100*time.Millisecond {
		t.Fatalf("p95 = %s, want 100ms", got)
	}
}
