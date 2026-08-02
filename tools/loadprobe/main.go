package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxProbeRequests = 100000

type probeConfig struct {
	URL               string
	CAFile            string
	Duration          time.Duration
	RequestsPerSecond int
	Concurrency       int
	P95Budget         time.Duration
	MaxErrorRatio     float64
	ExpectedStatuses  []int
}

type sample struct {
	duration time.Duration
	failed   bool
}

type report struct {
	FormatVersion   int     `json:"format_version"`
	Result          string  `json:"result"`
	Requests        int     `json:"requests"`
	Errors          int     `json:"errors"`
	ErrorRatio      float64 `json:"error_ratio"`
	P50Millis       float64 `json:"p50_millis"`
	P95Millis       float64 `json:"p95_millis"`
	P99Millis       float64 `json:"p99_millis"`
	P95BudgetMillis float64 `json:"p95_budget_millis"`
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "load probe configuration is invalid")
		os.Exit(1)
	}
	result, err := run(context.Background(), cfg)
	if encodeErr := json.NewEncoder(os.Stdout).Encode(result); encodeErr != nil && err == nil {
		err = encodeErr
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "load probe budget failed")
		os.Exit(1)
	}
}

func loadConfig() (probeConfig, error) {
	duration, err := time.ParseDuration(envOr("LOAD_PROBE_DURATION", "30s"))
	if err != nil {
		return probeConfig{}, err
	}
	rps, err := strconv.Atoi(envOr("LOAD_PROBE_RPS", "20"))
	if err != nil {
		return probeConfig{}, err
	}
	concurrency, err := strconv.Atoi(envOr("LOAD_PROBE_CONCURRENCY", "8"))
	if err != nil {
		return probeConfig{}, err
	}
	p95Millis, err := strconv.Atoi(envOr("LOAD_PROBE_P95_BUDGET_MS", "200"))
	if err != nil {
		return probeConfig{}, err
	}
	maxErrorRatio, err := strconv.ParseFloat(envOr("LOAD_PROBE_MAX_ERROR_RATIO", "0.001"), 64)
	if err != nil {
		return probeConfig{}, err
	}
	expectedStatuses, err := parseExpectedStatuses(envOr(
		"LOAD_PROBE_EXPECTED_STATUSES",
		envOr("LOAD_PROBE_EXPECTED_STATUS", "200"),
	))
	if err != nil {
		return probeConfig{}, err
	}
	cfg := probeConfig{
		URL: os.Getenv("LOAD_PROBE_URL"), CAFile: os.Getenv("LOAD_PROBE_CA_FILE"),
		Duration: duration, RequestsPerSecond: rps, Concurrency: concurrency,
		P95Budget:        time.Duration(p95Millis) * time.Millisecond,
		MaxErrorRatio:    maxErrorRatio,
		ExpectedStatuses: expectedStatuses,
	}
	if _, err := validateConfig(cfg); err != nil {
		return probeConfig{}, err
	}
	return cfg, nil
}

func validateConfig(cfg probeConfig) (int, error) {
	if cfg.Duration < time.Second || cfg.Duration > 30*time.Minute ||
		cfg.RequestsPerSecond < 1 || cfg.RequestsPerSecond > 5000 ||
		cfg.Concurrency < 1 || cfg.Concurrency > 256 ||
		cfg.P95Budget <= 0 || cfg.MaxErrorRatio < 0 || cfg.MaxErrorRatio > 1 {
		return 0, fmt.Errorf("probe bounds are invalid")
	}
	if err := validateTargetURL(cfg.URL); err != nil {
		return 0, err
	}
	requestLimit, err := calculateRequestLimit(cfg.Duration, cfg.RequestsPerSecond)
	if err != nil {
		return 0, err
	}
	return requestLimit, nil
}

func validateTargetURL(value string) error {
	target, err := url.Parse(value)
	if err != nil || target.Scheme != "https" || target.Host == "" || target.Hostname() == "" ||
		target.User != nil || target.Fragment != "" || strings.Contains(value, "#") {
		return fmt.Errorf("load probe target must be an HTTPS URL without userinfo or fragment")
	}
	return nil
}

func calculateRequestLimit(duration time.Duration, requestsPerSecond int) (int, error) {
	if duration <= 0 || requestsPerSecond <= 0 {
		return 0, fmt.Errorf("probe request limit is invalid")
	}
	numerator := int64(duration) * int64(requestsPerSecond)
	requestLimit := (numerator + int64(time.Second) - 1) / int64(time.Second)
	if requestLimit <= 0 || requestLimit > maxProbeRequests {
		return 0, fmt.Errorf("probe request limit exceeds the maximum")
	}
	return int(requestLimit), nil
}

func parseExpectedStatuses(value string) ([]int, error) {
	parts := strings.Split(value, ",")
	if len(parts) == 0 || len(parts) > 8 {
		return nil, fmt.Errorf("expected status count is invalid")
	}
	statuses := make([]int, 0, len(parts))
	seen := make(map[int]struct{}, len(parts))
	for _, part := range parts {
		status, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || status < 100 || status > 599 {
			return nil, fmt.Errorf("expected status is invalid")
		}
		if _, duplicate := seen[status]; duplicate {
			return nil, fmt.Errorf("expected status is duplicated")
		}
		seen[status] = struct{}{}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func run(ctx context.Context, cfg probeConfig) (report, error) {
	requestLimit, err := validateConfig(cfg)
	if err != nil {
		return report{}, err
	}
	client, err := newClient(cfg.CAFile)
	if err != nil {
		return report{}, err
	}
	defer client.CloseIdleConnections()
	runCtx, cancel := context.WithTimeout(ctx, cfg.Duration+10*time.Second)
	defer cancel()
	jobs := make(chan struct{})
	results := make(chan sample, requestLimit)
	var workers sync.WaitGroup
	for range cfg.Concurrency {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range jobs {
				startedAt := time.Now()
				failed := probe(runCtx, client, cfg.URL, cfg.ExpectedStatuses) != nil
				results <- sample{duration: time.Since(startedAt), failed: failed}
			}
		}()
	}
	interval := time.Second / time.Duration(cfg.RequestsPerSecond)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	deadline := time.NewTimer(cfg.Duration)
	defer deadline.Stop()
	submitted := 0
	finish := func(runErr error) (report, error) {
		close(jobs)
		workers.Wait()
		close(results)
		return summarize(cfg, results, runErr)
	}
	for {
		if submitted == requestLimit {
			return finish(nil)
		}
		select {
		case <-runCtx.Done():
			return finish(runCtx.Err())
		case <-deadline.C:
			return finish(nil)
		case <-ticker.C:
			select {
			case jobs <- struct{}{}:
				submitted++
			case <-runCtx.Done():
			}
		}
	}
}

func newClient(caFile string) (*http.Client, error) {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS13}
	if caFile != "" {
		body, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read load probe CA")
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(body) {
			return nil, fmt.Errorf("parse load probe CA")
		}
		tlsConfig.RootCAs = pool
	}
	transport := &http.Transport{
		TLSClientConfig: tlsConfig,
		MaxIdleConns:    512, MaxIdleConnsPerHost: 256,
		IdleConnTimeout: 30 * time.Second,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   5 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, nil
}

func probe(ctx context.Context, client *http.Client, target string, expectedStatuses []int) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("load probe request failed")
	}
	if _, err := io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10)); err != nil {
		_ = response.Body.Close()
		return fmt.Errorf("drain response body: %w", err)
	}
	if err := response.Body.Close(); err != nil {
		return fmt.Errorf("close response body: %w", err)
	}
	for _, expectedStatus := range expectedStatuses {
		if response.StatusCode == expectedStatus {
			return nil
		}
	}
	return fmt.Errorf("unexpected status")
}

func summarize(cfg probeConfig, samples <-chan sample, runErr error) (report, error) {
	var durations []time.Duration
	errorsCount := 0
	for item := range samples {
		durations = append(durations, item.duration)
		if item.failed {
			errorsCount++
		}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	result := report{
		FormatVersion: 1, Result: "passed", Requests: len(durations), Errors: errorsCount,
		P95BudgetMillis: float64(cfg.P95Budget) / float64(time.Millisecond),
	}
	if len(durations) > 0 {
		result.ErrorRatio = float64(errorsCount) / float64(len(durations))
		result.P50Millis = millis(percentile(durations, 0.50))
		result.P95Millis = millis(percentile(durations, 0.95))
		result.P99Millis = millis(percentile(durations, 0.99))
	}
	if runErr != nil || len(durations) == 0 || result.ErrorRatio > cfg.MaxErrorRatio ||
		percentile(durations, 0.95) > cfg.P95Budget {
		result.Result = "failed"
		return result, fmt.Errorf("load probe budget exceeded")
	}
	return result, nil
}

func percentile(values []time.Duration, quantile float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	index := int(math.Ceil(float64(len(values))*quantile)) - 1
	index = max(0, min(index, len(values)-1))
	return values[index]
}

func millis(value time.Duration) float64 {
	return float64(value) / float64(time.Millisecond)
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
