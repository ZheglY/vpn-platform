package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPlannedRateLimitIsBounded(t *testing.T) {
	server := &server{}
	behavior := httptest.NewRequest(http.MethodPost, "/behavior", bytes.NewBufferString(`{"mode":"rate_limited","count":1,"retry_after_seconds":2}`))
	behaviorResult := httptest.NewRecorder()
	server.setBehavior(behaviorResult, behavior)
	if behaviorResult.Code != http.StatusNoContent {
		t.Fatalf("behavior status = %d", behaviorResult.Code)
	}

	first := httptest.NewRecorder()
	server.sendMessage(first, telegramRequest())
	if first.Code != http.StatusTooManyRequests || len(server.messages) != 0 {
		t.Fatalf("first status=%d messages=%d", first.Code, len(server.messages))
	}
	second := httptest.NewRecorder()
	server.sendMessage(second, telegramRequest())
	if second.Code != http.StatusOK || len(server.messages) != 1 {
		t.Fatalf("second status=%d messages=%d", second.Code, len(server.messages))
	}
}

func TestPlannedBlockedResponseIsPermanentShape(t *testing.T) {
	server := &server{failure: failurePlan{Mode: "blocked", Remaining: 1}}
	result := httptest.NewRecorder()
	server.sendMessage(result, telegramRequest())
	if result.Code != http.StatusForbidden || !bytes.Contains(result.Body.Bytes(), []byte(`"error_code":403`)) {
		t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
	}
}

func TestSendMessageAcceptsProjectHTMLParseMode(t *testing.T) {
	server := &server{}
	request := httptest.NewRequest(http.MethodPost, "/bot-test/sendMessage", bytes.NewBufferString(`{"chat_id":42,"text":"<b>ready</b>","parse_mode":"HTML"}`))
	result := httptest.NewRecorder()

	server.sendMessage(result, request)

	if result.Code != http.StatusOK || len(server.messages) != 1 {
		t.Fatalf("status=%d messages=%d body=%s", result.Code, len(server.messages), result.Body.String())
	}
}

func TestSendMessageRejectsUnsupportedParseMode(t *testing.T) {
	server := &server{}
	request := httptest.NewRequest(http.MethodPost, "/bot-test/sendMessage", bytes.NewBufferString(`{"chat_id":42,"text":"ready","parse_mode":"MarkdownV2"}`))
	result := httptest.NewRecorder()

	server.sendMessage(result, request)

	if result.Code != http.StatusBadRequest || len(server.messages) != 0 {
		t.Fatalf("status=%d messages=%d", result.Code, len(server.messages))
	}
}

func telegramRequest() *http.Request {
	return httptest.NewRequest(http.MethodPost, "/bot-test/sendMessage", bytes.NewBufferString(`{"chat_id":42,"text":"hello"}`))
}
