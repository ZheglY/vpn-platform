package owner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ZheglY/vpn-platform/services/admin/internal/domain"
)

const maxResponseBytes = 128 << 10

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

type Client struct {
	http         *http.Client
	identity     string
	billing      string
	subscription string
	access       string
	provisioning string
	notification string
}

type Config struct {
	IdentityBaseURL     string
	BillingBaseURL      string
	SubscriptionBaseURL string
	AccessBaseURL       string
	ProvisioningBaseURL string
	NotificationBaseURL string
}

func New(config Config, client *http.Client) (*Client, error) {
	values := map[string]string{
		"identity": config.IdentityBaseURL, "billing": config.BillingBaseURL,
		"subscription": config.SubscriptionBaseURL, "access": config.AccessBaseURL,
		"provisioning": config.ProvisioningBaseURL, "notification": config.NotificationBaseURL,
	}
	for name, value := range values {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, fmt.Errorf("%s owner URL is invalid", name)
		}
		values[name] = strings.TrimRight(parsed.String(), "/")
	}
	return &Client{http: client, identity: values["identity"], billing: values["billing"],
		subscription: values["subscription"], access: values["access"], provisioning: values["provisioning"],
		notification: values["notification"]}, nil
}

type NotificationRetryResult struct {
	Notification struct {
		NotificationID   string `json:"notification_id"`
		NotificationType string `json:"notification_type"`
		Status           string `json:"status"`
		Attempts         int    `json:"attempts"`
		MaxAttempts      int    `json:"max_attempts"`
	} `json:"notification"`
	Replay bool `json:"replay"`
}

type SubscriptionRevokeResult struct {
	SubscriptionID string `json:"subscription_id"`
	Status         string `json:"status"`
	ReasonCode     string `json:"reason_code"`
	Replay         bool   `json:"replay"`
}

type AccessRecoveryResult struct {
	CredentialID    string `json:"credential_id"`
	OperationID     string `json:"operation_id"`
	DesiredRevision int    `json:"desired_revision"`
	Status          string `json:"status"`
	Replay          bool   `json:"replay"`
}

type ServiceHealth struct {
	Service string `json:"service"`
	Status  string `json:"status"`
	Version string `json:"version,omitempty"`
}

type HealthSummary struct {
	Services []ServiceHealth `json:"services"`
}

type UserView struct {
	UserID   string  `json:"user_id"`
	Status   string  `json:"status"`
	Locale   *string `json:"locale,omitempty"`
	Timezone *string `json:"timezone,omitempty"`
}

type ConsentView struct {
	Accepted bool `json:"accepted"`
}

type PlanSnapshotView struct {
	PlanID           string `json:"plan_id"`
	Name             string `json:"name"`
	DurationDays     int    `json:"duration_days"`
	GracePeriodHours int    `json:"grace_period_hours"`
	AmountMinor      int64  `json:"amount_minor"`
	Currency         string `json:"currency"`
	Region           string `json:"region"`
	RegionPolicy     string `json:"region_policy"`
	TrafficPolicy    string `json:"traffic_policy"`
	PrimaryNodes     int    `json:"primary_nodes"`
	FailoverNodes    int    `json:"failover_nodes"`
}

type OrderView struct {
	OrderID              string           `json:"order_id"`
	UserID               string           `json:"user_id"`
	Status               string           `json:"status"`
	AmountMinor          int64            `json:"amount_minor"`
	Currency             string           `json:"currency"`
	AcceptedTermsVersion string           `json:"accepted_terms_version"`
	PlanSnapshot         PlanSnapshotView `json:"plan_snapshot"`
	CreatedAt            time.Time        `json:"created_at"`
}

type PaymentView struct {
	PaymentID   string     `json:"payment_id"`
	OrderID     string     `json:"order_id"`
	Status      string     `json:"status"`
	AmountMinor int64      `json:"amount_minor"`
	Currency    string     `json:"currency"`
	PaidAt      *time.Time `json:"paid_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

type SubscriptionView struct {
	SubscriptionID     string     `json:"subscription_id"`
	UserID             string     `json:"user_id"`
	Status             string     `json:"status"`
	CurrentPeriodStart *time.Time `json:"current_period_start"`
	CurrentPeriodEnd   *time.Time `json:"current_period_end"`
	GraceEndsAt        *time.Time `json:"grace_ends_at"`
}

type AccessView struct {
	SubscriptionID     string `json:"subscription_id"`
	CredentialID       string `json:"credential_id"`
	AccessStatus       string `json:"access_status"`
	ProvisioningStatus string `json:"provisioning_status"`
	TokenStatus        string `json:"token_status"`
}

type NotificationView struct {
	NotificationID   string     `json:"notification_id"`
	UserID           string     `json:"user_id"`
	SubscriptionID   *string    `json:"subscription_id,omitempty"`
	CredentialID     *string    `json:"credential_id,omitempty"`
	NotificationType string     `json:"notification_type"`
	TemplateVersion  int        `json:"template_version"`
	Status           string     `json:"status"`
	Attempts         int        `json:"attempts"`
	MaxAttempts      int        `json:"max_attempts"`
	NextAttemptAt    time.Time  `json:"next_attempt_at"`
	CorrelationID    string     `json:"correlation_id"`
	CausationID      *string    `json:"causation_id,omitempty"`
	TerminalReason   *string    `json:"terminal_reason_code,omitempty"`
	DeliveredAt      *time.Time `json:"delivered_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type DeadLetterView struct {
	SourceTopic     string    `json:"source_topic"`
	SourcePartition int32     `json:"source_partition"`
	SourceOffset    int64     `json:"source_offset"`
	PayloadSHA256   string    `json:"payload_sha256"`
	EventType       *string   `json:"event_type,omitempty"`
	ReasonCode      string    `json:"reason_code"`
	State           string    `json:"state"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type DeadLetterListView struct {
	DeadLetters []DeadLetterView `json:"dead_letters"`
}

type SupportAllocationView struct {
	NodeID             string  `json:"node_id"`
	NodeLabel          string  `json:"node_label"`
	Region             string  `json:"region"`
	NodeStatus         string  `json:"node_status"`
	Role               string  `json:"role"`
	DesiredRevision    int     `json:"desired_revision"`
	DesiredState       string  `json:"desired_state"`
	AllocationRevision int     `json:"allocation_revision"`
	State              string  `json:"state"`
	LastErrorCode      *string `json:"last_error_code,omitempty"`
}

type ProvisioningView struct {
	CredentialID    string                  `json:"credential_id"`
	OperationID     string                  `json:"operation_id"`
	Kind            string                  `json:"kind"`
	DesiredRevision int                     `json:"desired_revision"`
	State           string                  `json:"state"`
	Attempts        int                     `json:"attempts"`
	MaxAttempts     int                     `json:"max_attempts"`
	LastErrorCode   *string                 `json:"last_error_code,omitempty"`
	CreatedAt       time.Time               `json:"created_at"`
	CompletedAt     *time.Time              `json:"completed_at,omitempty"`
	Allocations     []SupportAllocationView `json:"allocations"`
}

func (c *Client) RetryNotification(ctx context.Context, notificationID, actionID, correlationID, reason string) (NotificationRetryResult, error) {
	var result NotificationRetryResult
	err := c.mutate(ctx, c.notification+"/internal/v1/notifications/"+url.PathEscape(notificationID)+"/retry", actionID,
		map[string]string{"reason": reason}, &result)
	if err == nil && (result.Notification.NotificationID != notificationID || !notificationStatus(result.Notification.Status) || result.Notification.Attempts < 0 || result.Notification.MaxAttempts < 1 || result.Notification.Attempts > result.Notification.MaxAttempts) {
		err = &domain.OwnerError{Code: "owner_response_invalid"}
	}
	return result, err
}

func (c *Client) RevokeSubscription(ctx context.Context, subscriptionID, actionID, correlationID, reasonCode string) (SubscriptionRevokeResult, error) {
	var result SubscriptionRevokeResult
	err := c.mutate(ctx, c.subscription+"/internal/v1/subscriptions/"+url.PathEscape(subscriptionID)+"/admin-revoke", actionID,
		map[string]string{"action_id": actionID, "correlation_id": correlationID, "reason_code": reasonCode}, &result)
	if err == nil && (result.SubscriptionID != subscriptionID || result.Status != "revoked" || result.ReasonCode != reasonCode) {
		err = &domain.OwnerError{Code: "owner_response_invalid"}
	}
	return result, err
}

func (c *Client) RecoverAccess(ctx context.Context, credentialID, actionID, correlationID string) (AccessRecoveryResult, error) {
	var result AccessRecoveryResult
	err := c.mutate(ctx, c.access+"/internal/v1/credentials/"+url.PathEscape(credentialID)+"/recover", actionID,
		map[string]string{"action_id": actionID, "correlation_id": correlationID}, &result)
	if err == nil && (result.CredentialID != credentialID || !uuidPattern.MatchString(result.OperationID) || result.DesiredRevision < 2 || result.Status != "provisioning") {
		err = &domain.OwnerError{Code: "owner_response_invalid"}
	}
	return result, err
}

func notificationStatus(value string) bool {
	switch value {
	case "pending", "processing", "retry", "delivered", "permanently_failed", "suppressed":
		return true
	default:
		return false
	}
}

func (c *Client) ReadIdentity(ctx context.Context, userID string) (json.RawMessage, error) {
	return c.get(ctx, c.identity+"/internal/v1/users/"+url.PathEscape(userID), &UserView{})
}

func (c *Client) ReadConsent(ctx context.Context, userID, documentType, documentVersion string) (json.RawMessage, error) {
	return c.get(ctx, c.identity+"/internal/v1/users/"+url.PathEscape(userID)+"/consents/"+url.PathEscape(documentType)+"/"+url.PathEscape(documentVersion), &ConsentView{})
}

func (c *Client) ReadOrder(ctx context.Context, userID, orderID string) (json.RawMessage, error) {
	return c.get(ctx, c.billing+"/internal/v1/users/"+url.PathEscape(userID)+"/orders/"+url.PathEscape(orderID), &OrderView{})
}

func (c *Client) ReadPayment(ctx context.Context, userID, orderID, paymentID string) (json.RawMessage, error) {
	return c.get(ctx, c.billing+"/internal/v1/users/"+url.PathEscape(userID)+"/orders/"+url.PathEscape(orderID)+"/payments/"+url.PathEscape(paymentID), &PaymentView{})
}

func (c *Client) ReadSubscription(ctx context.Context, userID string) (json.RawMessage, error) {
	return c.get(ctx, c.subscription+"/internal/v1/users/"+url.PathEscape(userID)+"/subscription", &SubscriptionView{})
}

func (c *Client) ReadAccess(ctx context.Context, subscriptionID string) (json.RawMessage, error) {
	return c.get(ctx, c.access+"/internal/v1/subscriptions/"+url.PathEscape(subscriptionID)+"/access", &AccessView{})
}

func (c *Client) ReadNotification(ctx context.Context, notificationID string) (json.RawMessage, error) {
	return c.get(ctx, c.notification+"/internal/v1/notifications/"+url.PathEscape(notificationID), &NotificationView{})
}

func (c *Client) ReadNotificationDeadLetters(ctx context.Context, limit int) (json.RawMessage, error) {
	return c.get(ctx, c.notification+"/internal/v1/dead-letters?limit="+strconv.Itoa(limit), &DeadLetterListView{})
}

func (c *Client) ReadProvisioning(ctx context.Context, credentialID string) (json.RawMessage, error) {
	return c.get(ctx, c.provisioning+"/internal/v1/credentials/"+url.PathEscape(credentialID)+"/support-status", &ProvisioningView{})
}

func (c *Client) ReadHealthSummary(ctx context.Context) HealthSummary {
	targets := map[string]string{
		"access-service": c.access, "billing-service": c.billing, "identity-service": c.identity,
		"notification-service": c.notification, "provisioning-service": c.provisioning, "subscription-service": c.subscription,
	}
	results := make(chan ServiceHealth, len(targets))
	for service, baseURL := range targets {
		go func() { results <- c.probe(ctx, service, baseURL) }()
	}
	summary := HealthSummary{Services: make([]ServiceHealth, 0, len(targets))}
	for range targets {
		summary.Services = append(summary.Services, <-results)
	}
	slices.SortFunc(summary.Services, func(left, right ServiceHealth) int { return strings.Compare(left.Service, right.Service) })
	return summary
}

func (c *Client) probe(ctx context.Context, service, baseURL string) ServiceHealth {
	result := ServiceHealth{Service: service, Status: "unavailable"}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/readyz", nil)
	if err != nil {
		return result
	}
	var readiness struct {
		Service string `json:"service"`
		Status  string `json:"status"`
		Checks  any    `json:"checks"`
	}
	if c.do(request, &readiness) != nil || readiness.Service != service || readiness.Status != "ready" {
		return result
	}
	result.Status = "ready"
	request, err = http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/version", nil)
	if err != nil {
		return result
	}
	var version struct {
		Service string `json:"service"`
		Version string `json:"version"`
		Commit  string `json:"commit"`
		Date    string `json:"date"`
		Go      string `json:"go"`
	}
	if c.do(request, &version) == nil && version.Service == service {
		result.Version = version.Version
	}
	return result
}

func (c *Client) mutate(ctx context.Context, target, idempotencyKey string, body any, output any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode owner request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create owner request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "admin-"+idempotencyKey)
	return c.do(request, output)
}

func (c *Client) get(ctx context.Context, target string, output any) (json.RawMessage, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("create owner request: %w", err)
	}
	if err := c.do(request, output); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(output)
	if err != nil {
		return nil, &domain.OwnerError{Code: "owner_response_invalid"}
	}
	return payload, nil
}

func (c *Client) do(request *http.Request, output any) error {
	response, err := c.http.Do(request)
	if err != nil {
		return &domain.OwnerError{Code: "owner_unavailable"}
	}
	defer func() { _ = response.Body.Close() }()
	reader := io.LimitReader(response.Body, maxResponseBytes+1)
	payload, err := io.ReadAll(reader)
	if err != nil || len(payload) > maxResponseBytes {
		return &domain.OwnerError{Code: "owner_response_invalid"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ownerStatusError(response.StatusCode)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return &domain.OwnerError{Code: "owner_response_invalid"}
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return &domain.OwnerError{Code: "owner_response_invalid"}
	}
	return nil
}

func ownerStatusError(status int) error {
	switch status {
	case http.StatusNotFound:
		return &domain.OwnerError{Code: "owner_not_found"}
	case http.StatusConflict:
		return &domain.OwnerError{Code: "owner_conflict"}
	case http.StatusBadRequest:
		return &domain.OwnerError{Code: "owner_request_rejected"}
	case http.StatusUnauthorized, http.StatusForbidden:
		return &domain.OwnerError{Code: "owner_authorization_failed"}
	default:
		return &domain.OwnerError{Code: "owner_unavailable"}
	}
}
