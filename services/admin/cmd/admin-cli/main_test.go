package main

import (
	"io"
	"testing"
)

func TestParseCommandRequiresMutationSafetyFields(t *testing.T) {
	_, _, _, _, err := parseCommand("retry-notification", []string{"--notification-id", "11111111-1111-4111-8111-111111111111"}, io.Discard)
	if err == nil {
		t.Fatal("mutation without reason and idempotency key accepted")
	}
}

func TestParseCommandRejectsUnknownOrInvalidTarget(t *testing.T) {
	if _, _, _, _, err := parseCommand("get-access", []string{"--subscription-id", "not-a-uuid"}, io.Discard); err == nil {
		t.Fatal("invalid target accepted")
	}
	if _, _, _, _, err := parseCommand("sql", nil, io.Discard); err == nil {
		t.Fatal("arbitrary command accepted")
	}
}

func TestUnsafePayloadRejectsSecretFields(t *testing.T) {
	if !unsafePayload([]byte(`{"result":{"vless_client_uuid":"secret"}}`)) || !unsafePayload([]byte(`{"token":"secret"}`)) || unsafePayload([]byte(`{"token_status":"active","credential_id":"safe-id"}`)) {
		t.Fatal("CLI output redaction guard failed")
	}
}

func TestParseConsentCommandIsTyped(t *testing.T) {
	method, path, _, _, err := parseCommand("get-consent", []string{
		"--user-id", "11111111-1111-4111-8111-111111111111", "--document-type", "terms", "--document-version", "terms-v1",
	}, io.Discard)
	if err != nil || method != "GET" || path != "/admin/v1/users/11111111-1111-4111-8111-111111111111/consents/terms/terms-v1" {
		t.Fatalf("method=%q path=%q err=%v", method, path, err)
	}
}
