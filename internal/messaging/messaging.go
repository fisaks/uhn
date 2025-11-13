package messaging

import "context"

type QoS byte

const (
	AtMostOnce    QoS = 0
	FireAndForget QoS = 0
	AtLeastOnce   QoS = 1
	ExactlyOnce   QoS = 2
	AsyncNoWait   QoS = 3 // not a real QoS, will switch to 0 on publish but not wait on returned token
)

// Subscription is returned when you Subscribe you can Unsubscribe later.
type Subscription interface {
	Unsubscribe(ctx context.Context) error
}

type Broker interface {
	Connect(ctx context.Context) error
	Close(ctx context.Context) error
	Publish(ctx context.Context, topic string, qos QoS, retain bool, payload []byte) error
	PublishJSON(ctx context.Context, topic string, qos QoS, retain bool, v interface{}) error
	Subscribe(ctx context.Context, topic string, qos QoS, handler func(ctx context.Context, topic string, payload []byte)) (Subscription, error)
	IsConnected() bool
	Topic(parts ...string) string
}
