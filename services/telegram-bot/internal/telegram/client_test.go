package telegram

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestClientSendMessageCallsTelegramBotAPI(t *testing.T) {
	var gotPath string
	var gotPayload sendMessageRequest
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
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
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
	if gotPayload.ChatID != 100 || gotPayload.Text != "hello" {
		t.Fatalf("payload = %#v", gotPayload)
	}
}

func TestClientSendMessageReturnsErrorOnNon2xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"parameters":{"retry_after":3}}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "fake-token", time.Second)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	err = client.SendMessage(t.Context(), 100, "hello")
	if err == nil {
		t.Fatal("send message returned nil error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error type = %T, want *APIError", err)
	}
	if apiErr.Kind != ErrorKindRateLimited || apiErr.RetryAfter != 3*time.Second {
		t.Fatalf("api error = %+v", apiErr)
	}
}

func TestClientSendMessageReturnsErrorOnOkFalse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "fake-token", time.Second)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	err = client.SendMessage(t.Context(), 100, "hello")
	if err == nil {
		t.Fatal("send message returned nil error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error type = %T, want *APIError", err)
	}
	if apiErr.Kind != ErrorKindAPI || apiErr.ErrorCode != 400 {
		t.Fatalf("api error = %+v", apiErr)
	}
}

func TestClientSendMessageDoesNotReturnTokenInNetworkError(t *testing.T) {
	const token = "123456:secret-token"
	client, err := NewClientWithHTTPClient("https://api.telegram.invalid", token, &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, &url.Error{
				Op:  "Post",
				URL: "https://api.telegram.invalid/bot" + token + "/sendMessage",
				Err: errors.New("dial failed"),
			}
		}),
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	err = client.SendMessage(t.Context(), 100, "hello")
	if err == nil {
		t.Fatal("send message returned nil error")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("error leaked token: %q", err.Error())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
