package kafka

import (
	"testing"

	platformkafka "github.com/ZheglY/vpn-platform/internal/platform/kafka"
)

func TestStrictDecodeRejectsUnknownAndTrailingFields(t *testing.T) {
	for _, payload := range [][]byte{
		[]byte(`{"event_id":"x","unknown":true}`),
		[]byte(`{} {}`),
	} {
		var envelope platformkafka.Envelope
		if err := strictDecode(payload, &envelope); err == nil {
			t.Fatalf("payload %s was accepted", payload)
		}
	}
}
