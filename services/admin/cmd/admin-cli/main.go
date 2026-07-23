package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ZheglY/vpn-platform/internal/platform/requestid"
)

const maxResponseBytes = 256 << 10

var (
	cliUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)
	cliKeyPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{7,127}$`)
)

type options struct {
	baseURL  string
	certFile string
	keyFile  string
	caFile   string
	timeout  time.Duration
	json     bool
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	global := flag.NewFlagSet("admin-cli", flag.ContinueOnError)
	global.SetOutput(stderr)
	var opts options
	global.StringVar(&opts.baseURL, "base-url", os.Getenv("ADMIN_BASE_URL"), "admin-service HTTPS base URL")
	global.StringVar(&opts.certFile, "cert", os.Getenv("ADMIN_CLIENT_CERT_FILE"), "administrator client certificate")
	global.StringVar(&opts.keyFile, "key", os.Getenv("ADMIN_CLIENT_KEY_FILE"), "administrator private key")
	global.StringVar(&opts.caFile, "ca", os.Getenv("ADMIN_CA_FILE"), "admin-service CA certificate")
	global.DurationVar(&opts.timeout, "timeout", 10*time.Second, "request timeout")
	global.BoolVar(&opts.json, "json", false, "print JSON")
	if err := global.Parse(args); err != nil || global.NArg() < 1 {
		_, _ = fmt.Fprintln(stderr, "a typed command is required")
		return 2
	}
	if opts.timeout <= 0 || opts.timeout > time.Minute {
		_, _ = fmt.Fprintln(stderr, "timeout must be positive and at most 1m")
		return 2
	}
	client, baseURL, err := newClient(opts)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	method, path, body, idempotencyKey, err := parseCommand(global.Arg(0), global.Args()[1:], stderr)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, baseURL+path, bytes.NewReader(body))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "failed to create request")
		return 1
	}
	request.Header.Set("X-Request-ID", requestid.New())
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response, err := client.Do(request)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "admin-service request failed")
		return 1
	}
	defer func() { _ = response.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(payload) > maxResponseBytes {
		_, _ = fmt.Fprintln(stderr, "admin-service response was invalid")
		return 1
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = fmt.Fprintf(stderr, "admin-service returned HTTP %d\n", response.StatusCode)
		return 1
	}
	if unsafePayload(payload) {
		_, _ = fmt.Fprintln(stderr, "admin-service response contained a forbidden field")
		return 1
	}
	var value any
	if json.Unmarshal(payload, &value) != nil {
		_, _ = fmt.Fprintln(stderr, "admin-service response was invalid")
		return 1
	}
	if status, ok := actionStatus(value); ok && status == "failed" {
		_, _ = fmt.Fprintln(stderr, "administrative action failed")
		return 1
	}
	if opts.json {
		_, _ = stdout.Write(payload)
		if len(payload) == 0 || payload[len(payload)-1] != '\n' {
			_, _ = fmt.Fprintln(stdout)
		}
		return 0
	}
	pretty, _ := json.MarshalIndent(value, "", "  ")
	_, _ = fmt.Fprintln(stdout, string(pretty))
	return 0
}

func newClient(opts options) (*http.Client, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(opts.baseURL))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, "", fmt.Errorf("base-url must be a fixed HTTPS URL")
	}
	certificate, err := tls.LoadX509KeyPair(filepath.Clean(opts.certFile), filepath.Clean(opts.keyFile))
	if err != nil {
		return nil, "", fmt.Errorf("failed to load administrator certificate")
	}
	caPEM, err := os.ReadFile(filepath.Clean(opts.caFile))
	if err != nil {
		return nil, "", fmt.Errorf("failed to load CA certificate")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, "", fmt.Errorf("CA certificate is invalid")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}, RootCAs: roots}}
	return &http.Client{Transport: transport, Timeout: opts.timeout}, strings.TrimRight(parsed.String(), "/"), nil
}

func parseCommand(command string, args []string, stderr io.Writer) (string, string, []byte, string, error) {
	set := flag.NewFlagSet(command, flag.ContinueOnError)
	set.SetOutput(stderr)
	var userID, orderID, subscriptionID, notificationID, credentialID, reason, reasonCode, idempotencyKey string
	var documentType, documentVersion string
	var paymentID string
	var limit int
	set.StringVar(&userID, "user-id", "", "user UUID")
	set.StringVar(&orderID, "order-id", "", "order UUID")
	set.StringVar(&subscriptionID, "subscription-id", "", "subscription UUID")
	set.StringVar(&notificationID, "notification-id", "", "notification UUID")
	set.StringVar(&credentialID, "credential-id", "", "credential UUID")
	set.StringVar(&paymentID, "payment-id", "", "payment UUID")
	set.StringVar(&documentType, "document-type", "", "consent document type")
	set.StringVar(&documentVersion, "document-version", "", "consent document version")
	set.StringVar(&reason, "reason", "", "human-readable reason")
	set.StringVar(&reasonCode, "reason-code", "", "bounded reason code")
	set.StringVar(&idempotencyKey, "idempotency-key", "", "mutation idempotency key")
	set.IntVar(&limit, "limit", 50, "audit result limit")
	if err := set.Parse(args); err != nil {
		return "", "", nil, "", err
	}
	requireIDs := func(values ...string) error {
		for _, value := range values {
			if !cliUUIDPattern.MatchString(value) {
				return fmt.Errorf("required resource ID must be a UUID")
			}
		}
		return nil
	}
	requireMutation := func(targetID string) error {
		if err := requireIDs(targetID); err != nil {
			return err
		}
		if len(strings.TrimSpace(reason)) < 3 || len(reason) > 512 || !cliKeyPattern.MatchString(idempotencyKey) {
			return fmt.Errorf("mutation requires a reason and valid idempotency-key")
		}
		return nil
	}
	encode := func() ([]byte, error) {
		return json.Marshal(map[string]string{"reason": reason, "reason_code": reasonCode})
	}
	switch command {
	case "get-user":
		if err := requireIDs(userID); err != nil {
			return "", "", nil, "", err
		}
		return http.MethodGet, "/admin/v1/users/" + url.PathEscape(userID), nil, "", nil
	case "get-order":
		if err := requireIDs(userID, orderID); err != nil {
			return "", "", nil, "", err
		}
		return http.MethodGet, "/admin/v1/users/" + url.PathEscape(userID) + "/orders/" + url.PathEscape(orderID), nil, "", nil
	case "get-consent":
		if err := requireIDs(userID); err != nil || !validName(documentType) || !validName(documentVersion) {
			return "", "", nil, "", fmt.Errorf("get-consent requires a user UUID, document-type, and document-version")
		}
		return http.MethodGet, "/admin/v1/users/" + url.PathEscape(userID) + "/consents/" + url.PathEscape(documentType) + "/" + url.PathEscape(documentVersion), nil, "", nil
	case "get-payment":
		if err := requireIDs(userID, orderID, paymentID); err != nil {
			return "", "", nil, "", err
		}
		return http.MethodGet, "/admin/v1/users/" + url.PathEscape(userID) + "/orders/" + url.PathEscape(orderID) + "/payments/" + url.PathEscape(paymentID), nil, "", nil
	case "get-subscription":
		if err := requireIDs(userID); err != nil {
			return "", "", nil, "", err
		}
		return http.MethodGet, "/admin/v1/users/" + url.PathEscape(userID) + "/subscription", nil, "", nil
	case "get-access":
		if err := requireIDs(subscriptionID); err != nil {
			return "", "", nil, "", err
		}
		return http.MethodGet, "/admin/v1/subscriptions/" + url.PathEscape(subscriptionID) + "/access", nil, "", nil
	case "get-notification":
		if err := requireIDs(notificationID); err != nil {
			return "", "", nil, "", err
		}
		return http.MethodGet, "/admin/v1/notifications/" + url.PathEscape(notificationID), nil, "", nil
	case "get-provisioning":
		if err := requireIDs(credentialID); err != nil {
			return "", "", nil, "", err
		}
		return http.MethodGet, "/admin/v1/credentials/" + url.PathEscape(credentialID) + "/provisioning", nil, "", nil
	case "notification-dlq":
		return http.MethodGet, "/admin/v1/notification-dead-letters?limit=" + strconv.Itoa(limit), nil, "", nil
	case "audit":
		return http.MethodGet, "/admin/v1/audit?limit=" + strconv.Itoa(limit), nil, "", nil
	case "health":
		return http.MethodGet, "/admin/v1/health", nil, "", nil
	case "retry-notification":
		if err := requireMutation(notificationID); err != nil {
			return "", "", nil, "", err
		}
		body, err := encode()
		return http.MethodPost, "/admin/v1/notifications/" + url.PathEscape(notificationID) + "/retry", body, idempotencyKey, err
	case "revoke-subscription":
		if err := requireMutation(subscriptionID); err != nil {
			return "", "", nil, "", err
		}
		if reasonCode != "admin_block" && reasonCode != "abuse" && reasonCode != "deleted" {
			return "", "", nil, "", fmt.Errorf("revoke-subscription requires a valid reason-code")
		}
		body, err := encode()
		return http.MethodPost, "/admin/v1/subscriptions/" + url.PathEscape(subscriptionID) + "/revoke", body, idempotencyKey, err
	case "recover-access":
		if err := requireMutation(credentialID); err != nil {
			return "", "", nil, "", err
		}
		body, err := encode()
		return http.MethodPost, "/admin/v1/credentials/" + url.PathEscape(credentialID) + "/recover", body, idempotencyKey, err
	default:
		return "", "", nil, "", fmt.Errorf("unknown typed command")
	}
}

func unsafePayload(payload []byte) bool {
	var value any
	return json.Unmarshal(payload, &value) != nil || containsForbiddenOutput(value)
}

func containsForbiddenOutput(value any) bool {
	forbiddenKeys := map[string]struct{}{
		"subscription_url": {}, "vless_uuid": {}, "vless_client_uuid": {}, "ciphertext": {},
		"telegram_chat_id": {}, "confirmation_url": {}, "provider_payment_id": {}, "token_hash": {},
		"access_token": {}, "token": {}, "reality_private_key": {}, "private_key": {}, "management_url": {},
	}
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if _, forbidden := forbiddenKeys[strings.ToLower(key)]; forbidden || containsForbiddenOutput(nested) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if containsForbiddenOutput(nested) {
				return true
			}
		}
	case string:
		lower := strings.ToLower(typed)
		return strings.Contains(lower, "vless://") || strings.Contains(lower, "/s/")
	}
	return false
}

func validName(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for index, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || index > 0 && (char == '.' || char == '_' || char == '-') {
			continue
		}
		return false
	}
	return true
}

func actionStatus(value any) (string, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return "", false
	}
	status, ok := object["status"].(string)
	if ok {
		return status, true
	}
	if action, ok := object["action"].(map[string]any); ok {
		status, ok = action["status"].(string)
		return status, ok
	}
	return "", false
}
