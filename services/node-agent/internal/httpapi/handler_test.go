package httpapi

import (
	"strings"
	"testing"
)

func TestStrictDecodeRejectsOversizedBody(t *testing.T) {
	var target map[string]any
	body := "{}" + strings.Repeat(" ", 64<<10)
	if err := strictDecode(strings.NewReader(body), &target); err == nil {
		t.Fatal("oversized desired-state body was accepted")
	}
}
