package kafka

import (
	"context"
	"fmt"

	"github.com/twmb/franz-go/pkg/kgo"
)

type Publisher struct{ client *kgo.Client }

func NewPublisher(client *kgo.Client) *Publisher { return &Publisher{client: client} }

func (p *Publisher) Publish(ctx context.Context, topic, key string, value []byte) error {
	result := p.client.ProduceSync(ctx, &kgo.Record{Topic: topic, Key: []byte(key), Value: value})
	if err := result.FirstErr(); err != nil {
		return fmt.Errorf("publish subscription event")
	}
	return nil
}
