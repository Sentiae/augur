package events

import (
	kafka "github.com/sentiae/platform-kit/kafka"
)

// EventPublisher is the platform-kit Kafka Publisher interface.
type EventPublisher = kafka.Publisher

// CloudEvent is the platform-kit CloudEvent type (used by consumer).
type CloudEvent = kafka.CloudEvent

// NewKafkaPublisher creates a platform-kit Kafka publisher.
// Falls back to a no-op publisher when disabled or on connection failure.
func NewKafkaPublisher(brokers []string, topic string, enabled bool) EventPublisher {
	if !enabled {
		return kafka.NewNoopPublisher()
	}

	pub, err := kafka.NewPublisher(kafka.PublisherConfig{
		Brokers:     brokers,
		Source:      "infrastructure-intelligence-service",
		TopicPrefix: "sentiae",
	})
	if err != nil {
		return kafka.NewNoopPublisher()
	}
	return pub
}

// NewNoopPublisher returns a no-op publisher for development/testing.
func NewNoopPublisher() EventPublisher {
	return kafka.NewNoopPublisher()
}
