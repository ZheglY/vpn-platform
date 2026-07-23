package template

import (
	"fmt"
	"html"
	"strings"
	"unicode/utf8"
)

const MaxTelegramRunes = 4096

func Render(notificationType string, version int, variables map[string]string) (string, error) {
	if version != 1 {
		return "", fmt.Errorf("unsupported template version")
	}
	escape := func(name string) string { return html.EscapeString(strings.TrimSpace(variables[name])) }
	var text string
	switch notificationType {
	case "payment_confirmed":
		text = "<b>Payment confirmed.</b> We are preparing your VPN access."
	case "subscription_extended":
		text = "<b>Subscription extended.</b> Access is valid through " + escape("period_end") + "."
	case "subscription_grace":
		text = "<b>Grace period started.</b> Renew before " + escape("grace_ends_at") + " to keep VPN access."
	case "subscription_expired":
		text = "<b>Subscription expired.</b> VPN access is no longer available."
	case "subscription_revoked":
		text = "<b>Subscription access revoked.</b> Contact support if this was unexpected."
	case "refund_confirmed":
		text = "<b>Refund confirmed.</b> Subscription access will follow the remaining paid periods."
	case "access_ready":
		text = "<b>VPN access is ready.</b> Send /link to receive your one-time Happ subscription link."
	case "access_degraded":
		text = "<b>VPN access is ready.</b> Backup capacity is temporarily reduced. Send /link to receive your one-time Happ subscription link."
	case "provisioning_failed":
		text = "<b>VPN setup needs attention.</b> We could not finish setup. Please contact support."
	default:
		return "", fmt.Errorf("unsupported notification type")
	}
	if strings.Contains(strings.ToLower(text), "vless://") || strings.Contains(strings.ToLower(text), "/s/") {
		return "", fmt.Errorf("template contains forbidden credential material")
	}
	if utf8.RuneCountInString(text) > MaxTelegramRunes {
		return "", fmt.Errorf("rendered notification is too long")
	}
	return text, nil
}
