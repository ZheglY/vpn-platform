package requestid

import (
	"context"
	"net/http"

	"github.com/ZheglY/vpn-platform/internal/platform/cryptoutil"
)

const Header = "X-Request-Id"

type contextKey struct{}

func New() string {
	id, err := cryptoutil.RandomBase64URL(16)
	if err != nil {
		return "request-id-unavailable"
	}
	return id
}

func Valid(id string) bool {
	if len(id) < 8 || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			continue
		}
		switch r {
		case '-', '_', '.', ':':
			continue
		default:
			return false
		}
	}
	return true
}

func FromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	if id, ok := r.Context().Value(contextKey{}).(string); ok {
		return id
	}
	return ""
}

func FromContext(ctx context.Context) string {
	if id, ok := ctx.Value(contextKey{}).(string); ok {
		return id
	}
	return ""
}

func WithContext(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, contextKey{}, id)
}
