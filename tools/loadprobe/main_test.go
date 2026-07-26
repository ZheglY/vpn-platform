package main

import (
	"context"
	"crypto/tls"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoadProbePassesWithinBudget(t *testing.T) {
	var negotiatedVersion uint16
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		negotiatedVersion = r.TLS.Version
		w.WriteHeader(http.StatusOK)
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13}
	server.StartTLS()
	defer server.Close()
	caFile := writeServerCA(t, server)
	result, err := run(context.Background(), probeConfig{
		URL: server.URL, CAFile: caFile, Duration: time.Second, RequestsPerSecond: 20, Concurrency: 4,
		P95Budget: 100 * time.Millisecond, MaxErrorRatio: 0, ExpectedStatuses: []int{http.StatusOK},
	})
	if err != nil || result.Result != "passed" || result.Requests < 10 || negotiatedVersion != tls.VersionTLS13 {
		t.Fatalf("probe = %+v, err=%v", result, err)
	}
}

func TestLoadProbeFailsClosedOnErrors(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	result, err := run(context.Background(), probeConfig{
		URL: server.URL, CAFile: writeServerCA(t, server), Duration: time.Second, RequestsPerSecond: 10, Concurrency: 2,
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
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		}))
		result, runErr := run(context.Background(), probeConfig{
			URL: server.URL, CAFile: writeServerCA(t, server), Duration: time.Second, RequestsPerSecond: 10, Concurrency: 2,
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

func TestLoadProbeRejectsPlainHTTPAndUnsafeURLs(t *testing.T) {
	for _, target := range []string{
		"http://example.invalid/path",
		"https:///missing-host",
		"https://user@example.invalid/path",
		"https://example.invalid/path#fragment",
	} {
		cfg := validProbeConfig(target)
		if _, err := validateConfig(cfg); err == nil {
			t.Fatalf("validateConfig(%q) unexpectedly succeeded", target)
		}
	}
}

func TestLoadProbeRejectsRedirectDowngrade(t *testing.T) {
	var plaintextRequests atomic.Int32
	plaintext := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		plaintextRequests.Add(1)
	}))
	defer plaintext.Close()
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plaintext.URL, http.StatusFound)
	}))
	defer tlsServer.Close()
	client, err := newClient(writeServerCA(t, tlsServer))
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	if _, err := client.Get(tlsServer.URL); err == nil {
		t.Fatal("HTTPS-to-HTTP redirect unexpectedly succeeded")
	}
	if got := plaintextRequests.Load(); got != 0 {
		t.Fatalf("plaintext redirect target received %d requests", got)
	}
}

func TestRequestLimitRejectsFractionalOverflow(t *testing.T) {
	if _, err := calculateRequestLimit(25900*time.Millisecond, 4000); err == nil {
		t.Fatal("fractional duration exceeding 100000 requests was accepted")
	}
	if got, err := calculateRequestLimit(25000*time.Millisecond, 4000); err != nil || got != maxProbeRequests {
		t.Fatalf("request limit = %d, err=%v", got, err)
	}
}

func TestLoadProbeCompletesWithoutResultChannelDeadlock(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	cfg := validProbeConfig(server.URL)
	cfg.CAFile = writeServerCA(t, server)
	cfg.RequestsPerSecond = 5000
	cfg.Concurrency = 64
	cfg.ExpectedStatuses = []int{http.StatusNoContent}
	done := make(chan error, 1)
	go func() {
		_, err := run(context.Background(), cfg)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("load probe did not terminate")
	}
}

func validProbeConfig(target string) probeConfig {
	return probeConfig{
		URL: target, Duration: time.Second, RequestsPerSecond: 10, Concurrency: 2,
		P95Budget: time.Second, MaxErrorRatio: 0, ExpectedStatuses: []int{http.StatusOK},
	}
}

func writeServerCA(t *testing.T, server *httptest.Server) string {
	t.Helper()
	body := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	path := t.TempDir() + "/ca.pem"
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
