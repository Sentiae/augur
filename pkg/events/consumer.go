package events

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/IBM/sarama"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/logger"
)

// MessageHandler handles a CloudEvent message
type MessageHandler func(ctx context.Context, event CloudEvent) error

// KafkaConsumer consumes events from Kafka topics
type KafkaConsumer struct {
	group   sarama.ConsumerGroup
	topics  []string
	handler MessageHandler
	ready   chan bool
}

// NewKafkaConsumer creates a new Kafka consumer group
func NewKafkaConsumer(brokers []string, groupID string, topics []string, handler MessageHandler) (*KafkaConsumer, error) {
	config := sarama.NewConfig()
	config.Consumer.Group.Rebalance.GroupStrategies = []sarama.BalanceStrategy{sarama.NewBalanceStrategyRoundRobin()}
	config.Consumer.Offsets.Initial = sarama.OffsetNewest

	group, err := sarama.NewConsumerGroup(brokers, groupID, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create consumer group: %w", err)
	}

	return &KafkaConsumer{
		group:   group,
		topics:  topics,
		handler: handler,
		ready:   make(chan bool),
	}, nil
}

// Start begins consuming messages. Blocks until ctx is cancelled.
func (c *KafkaConsumer) Start(ctx context.Context) error {
	for {
		if err := c.group.Consume(ctx, c.topics, c); err != nil {
			logger.Error("Consumer group error: %v", err)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		c.ready = make(chan bool)
	}
}

// Close closes the consumer group
func (c *KafkaConsumer) Close() error {
	return c.group.Close()
}

// Setup is called at the beginning of a new session
func (c *KafkaConsumer) Setup(sarama.ConsumerGroupSession) error {
	close(c.ready)
	return nil
}

// Cleanup is called at the end of a session
func (c *KafkaConsumer) Cleanup(sarama.ConsumerGroupSession) error {
	return nil
}

// ConsumeClaim processes messages from a partition
func (c *KafkaConsumer) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for {
		select {
		case msg, ok := <-claim.Messages():
			if !ok {
				return nil
			}

			var event CloudEvent
			if err := json.Unmarshal(msg.Value, &event); err != nil {
				logger.Error("Failed to unmarshal event: %v", err)
				session.MarkMessage(msg, "")
				continue
			}

			if err := c.handler(session.Context(), event); err != nil {
				logger.Error("Failed to handle event %s: %v", event.Type, err)
			}

			session.MarkMessage(msg, "")

		case <-session.Context().Done():
			return nil
		}
	}
}
