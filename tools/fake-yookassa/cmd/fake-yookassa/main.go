package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ZheglY/vpn-platform/internal/platform/cryptoutil"
)

type payment struct {
	ID           string            `json:"id"`
	Status       string            `json:"status"`
	Amount       amount            `json:"amount"`
	Confirmation *confirmation     `json:"confirmation,omitempty"`
	Metadata     map[string]string `json:"metadata"`
	Test         bool              `json:"test"`
	Recipient    recipient         `json:"recipient"`
	CreatedAt    time.Time         `json:"created_at"`
	CapturedAt   *time.Time        `json:"captured_at,omitempty"`
	CanceledAt   *time.Time        `json:"canceled_at,omitempty"`
}
type amount struct {
	Value    string `json:"value"`
	Currency string `json:"currency"`
}
type confirmation struct {
	Type            string `json:"type"`
	ConfirmationURL string `json:"confirmation_url,omitempty"`
}
type recipient struct {
	AccountID string `json:"account_id"`
	GatewayID string `json:"gateway_id"`
}
type idempotentResult struct {
	Hash      string
	PaymentID string
}

type server struct {
	mu             sync.Mutex
	shopID         string
	secret         string
	payments       map[string]payment
	idempotency    map[string]idempotentResult
	createAttempts int
	failNext       string
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(runHealthcheck())
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	shopID := env("FAKE_YOOKASSA_SHOP_ID", "test-shop")
	secret := env("FAKE_YOOKASSA_SECRET_KEY", "test-secret")
	s := &server{shopID: shopID, secret: secret, payments: make(map[string]payment), idempotency: make(map[string]idempotentResult)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /v3/payments", s.createPayment)
	mux.HandleFunc("GET /v3/payments/{payment_id}", s.getPayment)
	mux.HandleFunc("POST /test/reset", s.reset)
	mux.HandleFunc("POST /test/fail-next", s.setFailNext)
	mux.HandleFunc("POST /test/payments/{payment_id}/status", s.setStatus)
	mux.HandleFunc("GET /test/payments", s.listPayments)
	srv := &http.Server{Addr: env("HTTP_ADDR", ":8085"), Handler: http.MaxBytesHandler(mux, 64<<10), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	select {
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdown)
	}
}

func (s *server) authenticate(w http.ResponseWriter, r *http.Request) bool {
	user, password, ok := r.BasicAuth()
	if !ok || user != s.shopID || password != s.secret {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"type": "unauthorized"})
		return false
	}
	return true
}

func (s *server) createPayment(w http.ResponseWriter, r *http.Request) {
	if !s.authenticate(w, r) {
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotence-Key"))
	if key == "" || len(key) > 64 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"type": "invalid_request"})
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return
	}
	hashBytes := sha256.Sum256(body)
	hash := hex.EncodeToString(hashBytes[:])
	var request struct {
		Amount       amount `json:"amount"`
		Capture      bool   `json:"capture"`
		Confirmation struct {
			Type      string `json:"type"`
			ReturnURL string `json:"return_url"`
		} `json:"confirmation"`
		Description string            `json:"description"`
		Metadata    map[string]string `json:"metadata"`
	}
	if json.Unmarshal(body, &request) != nil || !request.Capture || request.Confirmation.Type != "redirect" || request.Amount.Value == "" || request.Amount.Currency == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"type": "invalid_request"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.createAttempts++
	if result, ok := s.idempotency[key]; ok {
		if result.Hash != hash {
			writeJSON(w, http.StatusConflict, map[string]string{"type": "idempotency_conflict"})
			return
		}
		writeJSON(w, http.StatusOK, s.payments[result.PaymentID])
		return
	}
	id, err := cryptoutil.RandomUUID()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"type": "internal"})
		return
	}
	p := payment{ID: id, Status: "pending", Amount: request.Amount, Confirmation: &confirmation{Type: "redirect", ConfirmationURL: "https://pay.invalid/" + id}, Metadata: request.Metadata, Test: true, Recipient: recipient{AccountID: s.shopID, GatewayID: "fake-gateway"}, CreatedAt: time.Now().UTC()}
	s.payments[id] = p
	s.idempotency[key] = idempotentResult{Hash: hash, PaymentID: id}
	if s.failNext == "ambiguous_after_commit" {
		s.failNext = ""
		writeJSON(w, http.StatusInternalServerError, map[string]string{"type": "internal_server_error"})
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *server) getPayment(w http.ResponseWriter, r *http.Request) {
	if !s.authenticate(w, r) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.payments[r.PathValue("payment_id")]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"type": "not_found"})
		return
	}
	writeJSON(w, http.StatusOK, p)
}
func (s *server) reset(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	s.payments = make(map[string]payment)
	s.idempotency = make(map[string]idempotentResult)
	s.createAttempts = 0
	s.failNext = ""
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}
func (s *server) setFailNext(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Mode string `json:"mode"`
	}
	if json.NewDecoder(r.Body).Decode(&request) != nil || request.Mode != "ambiguous_after_commit" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid mode"})
		return
	}
	s.mu.Lock()
	s.failNext = request.Mode
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}
func (s *server) setStatus(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Status string `json:"status"`
	}
	if json.NewDecoder(r.Body).Decode(&request) != nil || (request.Status != "pending" && request.Status != "succeeded" && request.Status != "canceled") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid status"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.payments[r.PathValue("payment_id")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	now := time.Now().UTC()
	p.Status = request.Status
	if request.Status == "succeeded" {
		p.CapturedAt = &now
		p.Confirmation = nil
	}
	if request.Status == "canceled" {
		p.CanceledAt = &now
		p.Confirmation = nil
	}
	s.payments[p.ID] = p
	writeJSON(w, http.StatusOK, p)
}
func (s *server) listPayments(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	payments := make([]payment, 0, len(s.payments))
	for _, p := range s.payments {
		payments = append(payments, p)
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(payments), "create_attempts": s.createAttempts, "payments": payments})
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
func runHealthcheck() int {
	addr := strings.TrimPrefix(env("HTTP_ADDR", ":8085"), ":")
	if _, err := strconv.Atoi(addr); err != nil {
		return 1
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + addr + "/livez")
	if err != nil {
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
