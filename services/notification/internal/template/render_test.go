package template

import (
	"strings"
	"testing"
)

func TestRenderEscapesVariables(t *testing.T) {
	text, err := Render("subscription_extended", 1, map[string]string{"period_end": `<b onclick="x">&`})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, `onclick="x"`) || !strings.Contains(text, "&lt;b") || !strings.Contains(text, "&amp;") {
		t.Fatalf("variable was not escaped: %q", text)
	}
}

func TestRenderRejectsOversizedMessage(t *testing.T) {
	_, err := Render("subscription_extended", 1, map[string]string{"period_end": strings.Repeat("x", MaxTelegramRunes)})
	if err == nil {
		t.Fatal("oversized message accepted")
	}
}

func TestAccessReadyNeverContainsSubscriptionURL(t *testing.T) {
	text, err := Render("access_ready", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(text), "vless://") || strings.Contains(strings.ToLower(text), "/s/") {
		t.Fatalf("secret-bearing link rendered: %q", text)
	}
}
