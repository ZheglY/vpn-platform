package config

import (
	"errors"
	"testing"
	"time"
)

func TestRequiredString(t *testing.T) {
	t.Setenv("VPN_REQUIRED", "value")
	got, err := RequiredString("VPN_REQUIRED")
	if err != nil {
		t.Fatalf("RequiredString returned error: %v", err)
	}
	if got != "value" {
		t.Fatalf("RequiredString = %q, want value", got)
	}

	t.Setenv("VPN_EMPTY", " ")
	if _, err := RequiredString("VPN_EMPTY"); err == nil {
		t.Fatal("expected error for empty required variable")
	}
}

func TestDuration(t *testing.T) {
	t.Setenv("VPN_TIMEOUT", "250ms")
	got, err := Duration("VPN_TIMEOUT", time.Second)
	if err != nil {
		t.Fatalf("Duration returned error: %v", err)
	}
	if got != 250*time.Millisecond {
		t.Fatalf("Duration = %s, want 250ms", got)
	}

	t.Setenv("VPN_BAD_TIMEOUT", "-1s")
	if _, err := Duration("VPN_BAD_TIMEOUT", time.Second); err == nil {
		t.Fatal("expected error for negative duration")
	}
}

func TestCombine(t *testing.T) {
	errs := Append(nil, "A", errors.New("bad"))
	err := Combine(errs)
	if err == nil {
		t.Fatal("expected combined error")
	}
	if err.Error() != "invalid configuration: A" {
		t.Fatalf("unexpected error: %v", err)
	}
}
