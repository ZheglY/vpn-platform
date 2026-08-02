package httpapi

import (
	"strings"
	"testing"
)

func TestDecodeStrictRejectsOversizedRetryBody(t *testing.T) {
	var request struct {
		Reason string `json:"reason"`
	}
	if err := decodeStrict(strings.NewReader(strings.Repeat(" ", maxRetryBodyBytes+1)), &request); err == nil {
		t.Fatal("oversized retry body accepted")
	}
}
