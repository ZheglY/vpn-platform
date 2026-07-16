package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yarik/vpn-service/services/identity/internal/domain"
)

const testUserID = "00000000-0000-4000-8000-000000000001"

func TestUpsertTelegramIdentity(t *testing.T) {
	store := &fakeStore{}
	handler := New(store)
	req := httptest.NewRequest(http.MethodPut, "/internal/v1/telegram-users/42", bytes.NewBufferString(`{"username":"tester","display_name":"Test User","language_code":"en","locale":"en"}`))
	req.SetPathValue("telegram_id", "42")
	rec := httptest.NewRecorder()

	handler.UpsertTelegramIdentity(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if store.profile.TelegramUserID != 42 {
		t.Fatalf("telegram id = %d, want 42", store.profile.TelegramUserID)
	}
	var response userResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.UserID != store.user.ID || response.Status != domain.UserStatusActive {
		t.Fatalf("response = %+v", response)
	}
}

func TestAcceptConsent(t *testing.T) {
	store := &fakeStore{}
	handler := New(store)
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/users/"+testUserID+"/consents", bytes.NewBufferString(`{"document_type":"terms","document_version":"terms-v1"}`))
	req.SetPathValue("user_id", testUserID)
	rec := httptest.NewRecorder()

	handler.AcceptConsent(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if store.consent.DocumentType != "terms" || store.consent.DocumentVersion != "terms-v1" || store.consent.Source != "telegram" {
		t.Fatalf("consent = %+v", store.consent)
	}
}

func TestHasConsent(t *testing.T) {
	store := &fakeStore{accepted: true}
	handler := New(store)
	req := httptest.NewRequest(http.MethodGet, "/internal/v1/users/"+testUserID+"/consents/terms/terms-v1", nil)
	req.SetPathValue("user_id", testUserID)
	req.SetPathValue("document_type", "terms")
	req.SetPathValue("document_version", "terms-v1")
	rec := httptest.NewRecorder()

	handler.HasConsent(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var response map[string]bool
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response["accepted"] {
		t.Fatalf("accepted = false, want true")
	}
}

func TestAcceptConsentRejectsInvalidUserID(t *testing.T) {
	store := &fakeStore{}
	handler := New(store)
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/users/not-a-uuid/consents", bytes.NewBufferString(`{"document_type":"terms","document_version":"terms-v1"}`))
	req.SetPathValue("user_id", "not-a-uuid")
	rec := httptest.NewRecorder()

	handler.AcceptConsent(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

type fakeStore struct {
	user     domain.User
	profile  domain.TelegramProfile
	consent  domain.ConsentInput
	accepted bool
}

func (s *fakeStore) UpsertTelegramIdentity(_ context.Context, profile domain.TelegramProfile) (domain.User, error) {
	s.profile = profile
	s.user = domain.User{ID: testUserID, Status: domain.UserStatusActive}
	return s.user, nil
}

func (s *fakeStore) GetUser(context.Context, string) (domain.User, error) {
	if s.user.ID == "" {
		s.user = domain.User{ID: testUserID, Status: domain.UserStatusActive}
	}
	return s.user, nil
}

func (s *fakeStore) AcceptConsent(_ context.Context, input domain.ConsentInput) error {
	s.consent = input
	return nil
}

func (s *fakeStore) HasConsent(context.Context, string, string, string) (bool, error) {
	return s.accepted, nil
}

func (s *fakeStore) Ping(context.Context) error { return nil }

func (s *fakeStore) Close() {}
