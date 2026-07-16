package httpauth

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/yarik/vpn-service/internal/platform/httperror"
)

type ServiceIdentity struct {
	TrustDomain string
	Namespace   string
	Name        string
}

type contextKey struct{}

func FromContext(ctx context.Context) (ServiceIdentity, bool) {
	identity, ok := ctx.Value(contextKey{}).(ServiceIdentity)
	return identity, ok
}

type ServicePolicy struct {
	TrustDomain string
	Namespace   string
	Allowed     []string
}

func RequireService(policy ServicePolicy) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identity, ok := IdentityFromTLS(r.TLS, policy.TrustDomain, policy.Namespace)
			if !ok {
				httperror.Write(w, r, http.StatusUnauthorized, "unauthenticated", "client certificate is required")
				return
			}
			if len(policy.Allowed) > 0 && !slices.Contains(policy.Allowed, identity.Name) {
				httperror.Write(w, r, http.StatusForbidden, "forbidden", "service identity is not allowed")
				return
			}
			ctx := context.WithValue(r.Context(), contextKey{}, identity)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func IdentityFromTLS(state *tls.ConnectionState, trustDomain, namespace string) (ServiceIdentity, bool) {
	if state == nil || trustDomain == "" || namespace == "" || len(state.VerifiedChains) == 0 || len(state.VerifiedChains[0]) == 0 {
		return ServiceIdentity{}, false
	}
	cert := state.VerifiedChains[0][0]
	for _, uri := range cert.URIs {
		identity, ok := identityFromSPIFFEURI(uri, trustDomain, namespace)
		if ok {
			return identity, true
		}
	}
	return ServiceIdentity{}, false
}

func identityFromSPIFFEURI(uri *url.URL, trustDomain, namespace string) (ServiceIdentity, bool) {
	if uri == nil || uri.Scheme != "spiffe" || uri.Host != trustDomain || uri.User != nil || uri.RawQuery != "" || uri.Fragment != "" {
		return ServiceIdentity{}, false
	}

	parts := strings.Split(strings.Trim(uri.Path, "/"), "/")
	if len(parts) != 4 || parts[0] != "ns" || parts[2] != "sa" || parts[1] != namespace || parts[3] == "" {
		return ServiceIdentity{}, false
	}
	if strings.Contains(parts[3], "/") {
		return ServiceIdentity{}, false
	}

	return ServiceIdentity{
		TrustDomain: trustDomain,
		Namespace:   namespace,
		Name:        parts[3],
	}, true
}

func SPIFFEID(trustDomain, namespace, service string) (string, error) {
	if trustDomain == "" || namespace == "" || service == "" {
		return "", fmt.Errorf("trust domain, namespace, and service are required")
	}
	return fmt.Sprintf("spiffe://%s/ns/%s/sa/%s", trustDomain, namespace, service), nil
}
