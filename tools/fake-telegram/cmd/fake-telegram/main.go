package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type message struct {
	MessageID int64  `json:"message_id"`
	ChatID    string `json:"chat_id"`
	Text      string `json:"text"`
}

type server struct {
	mu       sync.Mutex
	messages []message
	delay    time.Duration
	failure  failurePlan
}

type failurePlan struct {
	Mode              string
	Remaining         int
	RetryAfterSeconds int
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(runHealthcheck())
	}
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		os.Exit(1)
	}
}

func run() error {
	delay, err := parseDurationEnv("SEND_MESSAGE_DELAY", 0)
	if err != nil {
		return err
	}
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8082"
	}
	s := &server{delay: delay}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /", s.sendMessage)
	mux.HandleFunc("POST /reset", s.reset)
	mux.HandleFunc("POST /behavior", s.setBehavior)
	mux.HandleFunc("GET /messages", s.listMessages)

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           http.MaxBytesHandler(mux, 64*1024),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.ListenAndServe()
	}()
	select {
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return fmt.Errorf("listen and serve: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	}
}

func (s *server) sendMessage(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/bot") || !strings.HasSuffix(r.URL.Path, "/sendMessage") {
		http.NotFound(w, r)
		return
	}
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-r.Context().Done():
			return
		}
	}
	var req struct {
		ChatID    json.RawMessage `json:"chat_id"`
		Text      string          `json:"text"`
		ParseMode string          `json:"parse_mode,omitempty"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":          false,
			"error_code":  400,
			"description": "bad request",
		})
		return
	}
	chatID, ok := normalizeChatID(req.ChatID)
	if !ok || req.Text == "" || req.ParseMode != "" && req.ParseMode != "HTML" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":          false,
			"error_code":  400,
			"description": "bad request",
		})
		return
	}
	if s.writePlannedFailure(w) {
		return
	}

	s.mu.Lock()
	msg := message{
		MessageID: int64(len(s.messages) + 1),
		ChatID:    chatID,
		Text:      req.Text,
	}
	s.messages = append(s.messages, msg)
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"result": msg,
	})
}

func normalizeChatID(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var numeric int64
	if err := json.Unmarshal(raw, &numeric); err == nil && numeric != 0 {
		return strconv.FormatInt(numeric, 10), true
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		text = strings.TrimSpace(text)
		return text, text != ""
	}
	return "", false
}

func (s *server) listMessages(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	messages := append([]message(nil), s.messages...)
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"count":    len(messages),
		"messages": messages,
	})
}

func (s *server) reset(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	s.messages = nil
	s.failure = failurePlan{}
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) setBehavior(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Mode              string `json:"mode"`
		Count             int    `json:"count"`
		RetryAfterSeconds int    `json:"retry_after_seconds"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.Count < 0 || request.Count > 100 || request.RetryAfterSeconds < 0 || request.RetryAfterSeconds > 60 {
		http.Error(w, "invalid behavior", http.StatusBadRequest)
		return
	}
	allowed := map[string]bool{"success": true, "rate_limited": true, "blocked": true, "unauthorized": true, "server_error": true}
	if !allowed[request.Mode] || request.Mode == "rate_limited" && request.RetryAfterSeconds < 1 {
		http.Error(w, "invalid behavior", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.failure = failurePlan{Mode: request.Mode, Remaining: request.Count, RetryAfterSeconds: request.RetryAfterSeconds}
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) writePlannedFailure(w http.ResponseWriter) bool {
	s.mu.Lock()
	plan := s.failure
	if plan.Remaining > 0 {
		s.failure.Remaining--
	}
	s.mu.Unlock()
	if plan.Remaining < 1 || plan.Mode == "success" {
		return false
	}
	switch plan.Mode {
	case "rate_limited":
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"ok": false, "error_code": 429, "description": "too many requests",
			"parameters": map[string]int{"retry_after": plan.RetryAfterSeconds},
		})
	case "blocked":
		writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error_code": 403, "description": "bot was blocked by the user"})
	case "unauthorized":
		writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error_code": 401, "description": "unauthorized"})
	case "server_error":
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error_code": 500, "description": "temporary failure"})
	default:
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func parseDurationEnv(name string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}
	if duration < 0 {
		return 0, fmt.Errorf("%s must not be negative", name)
	}
	return duration, nil
}

func runHealthcheck() int {
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8082"
	}
	addr = strings.TrimPrefix(addr, ":")
	port, err := strconv.Atoi(addr)
	if err != nil || port <= 0 {
		fmt.Fprintf(os.Stderr, "invalid HTTP_ADDR: %q\n", os.Getenv("HTTP_ADDR"))
		return 1
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/livez")
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck failed: %v\n", err)
		return 1
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck status: %d\n", resp.StatusCode)
		return 1
	}
	return 0
}
