package kafka

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/ZheglY/vpn-platform/internal/platform/telemetry"
	"github.com/ZheglY/vpn-platform/internal/platform/tlsconfig"
)

type Envelope struct {
	EventID           string          `json:"event_id"`
	EventType         string          `json:"event_type"`
	SchemaVersion     int             `json:"schema_version"`
	OccurredAt        time.Time       `json:"occurred_at"`
	Producer          string          `json:"producer"`
	CorrelationID     string          `json:"correlation_id"`
	CausationID       *string         `json:"causation_id"`
	AggregateType     string          `json:"aggregate_type"`
	AggregateID       string          `json:"aggregate_id"`
	AggregateSequence int64           `json:"aggregate_sequence,omitempty"`
	PartitionKey      string          `json:"partition_key"`
	Data              json.RawMessage `json:"data"`
}

func NewClientForEnvironment(environment string, brokers []string, clientID string, opts ...kgo.Opt) (*kgo.Client, error) {
	if environment == "staging" || environment == "production" {
		caFile := strings.TrimSpace(os.Getenv("KAFKA_TLS_CA_FILE"))
		certFile := strings.TrimSpace(os.Getenv("KAFKA_TLS_CERT_FILE"))
		keyFile := strings.TrimSpace(os.Getenv("KAFKA_TLS_KEY_FILE"))
		if caFile == "" || certFile == "" || keyFile == "" {
			return nil, fmt.Errorf("Kafka mTLS credential files are required")
		}
		tlsConfig, err := tlsconfig.NewClient([]string{caFile}, certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("configure Kafka mTLS: %w", err)
		}
		opts = append(opts, kgo.DialTLSConfig(tlsConfig))
	}
	return NewClient(brokers, clientID, opts...)
}

func NewClient(brokers []string, clientID string, opts ...kgo.Opt) (*kgo.Client, error) {
	if len(brokers) == 0 {
		return nil, fmt.Errorf("at least one Kafka broker is required")
	}
	allOpts := []kgo.Opt{
		kgo.SeedBrokers(brokers...),
		kgo.ClientID(clientID),
		kgo.WithHooks(telemetry.KafkaHook{}),
	}
	allOpts = append(allOpts, opts...)
	client, err := kgo.NewClient(allOpts...)
	if err != nil {
		return nil, fmt.Errorf("create Kafka client: %w", err)
	}
	return client, nil
}
