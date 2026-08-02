package httpdecode

import (
	"strings"
	"testing"
)

func TestStrictRejectsContractViolations(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		payload string
		limit   int64
	}{
		{name: "unknown field", payload: `{"status":"ok","secret":"value"}`, limit: 1024},
		{name: "trailing value", payload: `{"status":"ok"}{"status":"second"}`, limit: 1024},
		{name: "oversized", payload: `{"status":"` + strings.Repeat("x", 32) + `"}`, limit: 16},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var output struct {
				Status string `json:"status"`
			}
			if err := Strict(strings.NewReader(test.payload), test.limit, &output); err == nil {
				t.Fatal("Strict() error = nil, want contract violation")
			}
		})
	}
}

func TestStrictAcceptsOneKnownValue(t *testing.T) {
	t.Parallel()
	var output struct {
		Status string `json:"status"`
	}
	if err := Strict(strings.NewReader(`{"status":"ok"}`), 1024, &output); err != nil {
		t.Fatalf("Strict() error = %v", err)
	}
	if output.Status != "ok" {
		t.Fatalf("status = %q, want ok", output.Status)
	}
}
