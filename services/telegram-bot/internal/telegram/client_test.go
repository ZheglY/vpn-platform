package telegram

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientSendMessageCallsTelegramBotAPI(t *testing.T) {
	var gotPath string
	var gotPayload map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want %s", r.Method, http.MethodPost)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("content type = %q", r.Header.Get("Content-Type"))
		}
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "fake-token", time.Second)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	if err := client.SendMessage(t.Context(), 100, "hello"); err != nil {
		t.Fatalf("send message: %v", err)
	}

	if gotPath != "/botfake-token/sendMessage" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotPayload["chat_id"] != "100" || gotPayload["text"] != "hello" {
		t.Fatalf("payload = %#v", gotPayload)
	}
}

func TestClientSendMessageReturnsErrorOnNon2xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "fake-token", time.Second)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	if err := client.SendMessage(t.Context(), 100, "hello"); err == nil {
		t.Fatal("send message returned nil error")
	}
}
