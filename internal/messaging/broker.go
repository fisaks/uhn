package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/fisaks/uhn/internal/logging"
)

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

	Subscribe(ctx context.Context, topic string, qos QoS, handler Subscriber) (Subscription, error)
	IsConnected() bool
	AddOnConnectPublisher(id string, publisher OnConnectPublisher)
	RemoveOnConnectPublisher(id string)
}
type OnConnectPublisher interface {
	OnConnectPublish(ctx context.Context) (*ConnectMessage, error)
}
type Subscriber interface {
	OnMessage(ctx context.Context, topic string, payload []byte)
}
type ConnectMessage struct {
	Topic        string
	Qos          QoS
	Retain       bool
	PayloadBytes []byte
	Payload      interface{}
}

type BrokerConfig struct {
	BrokerURL        string
	ClientName       string
	TopicPrefix      string
	ConnectTimeout   time.Duration
	PublishTimeout   time.Duration
	SubscribeTimeout time.Duration
}

type MsgBroker struct {
	config              BrokerConfig
	client              mqtt.Client
	mu                  sync.RWMutex
	subs                map[string]mqtt.Token
	onConnectPublishers map[string]OnConnectPublisher
}

func NewBroker(cfg BrokerConfig) Broker {
	return &MsgBroker{
		config:              cfg,
		subs:                make(map[string]mqtt.Token),
		onConnectPublishers: make(map[string]OnConnectPublisher),
	}
}

func (b *MsgBroker) Connect(ctx context.Context) error {
	if b.client == nil {
		b.client = mqtt.NewClient(b.optionsFromConfig())
	}
	if b.client.IsConnected() {
		return nil
	}

	t := b.client.Connect()
	done := make(chan struct{})
	go func() {
		t.Wait()
		close(done)
	}()

	select {
	case <-done:
		return t.Error()
	case <-ctx.Done():

		b.client.Disconnect(250)
		return ctx.Err()
	}
}

func (b *MsgBroker) optionsFromConfig() *mqtt.ClientOptions {
	opts := mqtt.NewClientOptions().AddBroker(b.config.BrokerURL)
	opts.SetClientID("uhn-" + b.config.ClientName)
	opts.SetConnectTimeout(b.config.ConnectTimeout)
	opts.SetAutoReconnect(true)
	opts.OnConnect = func(c mqtt.Client) {
		b.onConnectPublisher()
	}
	return opts
}

func (b *MsgBroker) AddOnConnectPublisher(id string, publisher OnConnectPublisher) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onConnectPublishers[id] = publisher
}

func (b *MsgBroker) RemoveOnConnectPublisher(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.onConnectPublishers, id)
}

func (b *MsgBroker) onConnectPublisher() {
	b.mu.RLock()
	publisherCopy := make(map[string]OnConnectPublisher, len(b.onConnectPublishers))
	for k, v := range b.onConnectPublishers {
		publisherCopy[k] = v
	}
	b.mu.RUnlock()

	ctx := context.Background()

	for id, publisher := range publisherCopy {
		req, err := publisher.OnConnectPublish(ctx)
		if err != nil {
			logging.Error("onConnectPublisher failed", "clientName", b.config.ClientName, "id", id, "error", err)
			continue
		}

		var pubErr error
		if req.PayloadBytes == nil {
			pubErr = b.PublishJSON(ctx, req.Topic, req.Qos, req.Retain, req.Payload)
		} else {
			pubErr = b.Publish(ctx, req.Topic, req.Qos, req.Retain, req.PayloadBytes)
		}
		if pubErr != nil {
			logging.Error("onConnect publish failed", "clientName", b.config.ClientName, "id", id, "topic", req.Topic, "error", pubErr)
		}

	}
}

func (b *MsgBroker) IsConnected() bool {
	if b.client == nil {
		return false
	}
	return b.client.IsConnected()
}

func (b *MsgBroker) Close(ctx context.Context) error {
	if b.client == nil {
		return nil
	}
	// Graceful disconnect with short timeout
	done := make(chan struct{})
	go func() {
		// 250 ms quiesce period
		b.client.Disconnect(250)
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (b *MsgBroker) prefixTopic(topic string) string {
	if b.config.TopicPrefix != "" {
		if b.config.TopicPrefix[len(b.config.TopicPrefix)-1] == '/' {
			topic = b.config.TopicPrefix + topic
		} else {
			topic = b.config.TopicPrefix + "/" + topic
		}
	}
	return topic
}

func (b *MsgBroker) Publish(ctx context.Context, topic string, qos QoS, retain bool, payload []byte) error {
	if b.client == nil {
		return errors.New("client not initialized")
	}
	fullTopic := b.prefixTopic(topic)
	qosByte, wait := qosToByte(qos)
	token := b.client.Publish(fullTopic, qosByte, retain, payload)
	if !wait {
		return nil
	}
	timeout := b.config.PublishTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	select {
	case <-token.Done():
		return token.Error()
	case <-time.After(timeout):
		return fmt.Errorf("publish timeout after %v", timeout)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func qosToByte(qos QoS) (byte, bool) {
	if qos > 2 {
		return 0, false
	}
	return byte(qos), true
}

func (b *MsgBroker) PublishJSON(ctx context.Context, topic string, qos QoS, retain bool, v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	logging.Debug("Publishing JSON", "topic", topic, "payload", string(data))
	return b.Publish(ctx, topic, qos, retain, data)
}

// Subscribe registers handler and waits for SUBACK with timeout
func (b *MsgBroker) Subscribe(ctx context.Context, topic string, qos QoS, handler Subscriber) (Subscription, error) {
	if b.client == nil {
		return nil, errors.New("client not initialized")
	}
	fullTopic := b.prefixTopic(topic)
	// wrapper that converts paho message to our handler and logs panics without crashing
	onMessageHandler := func(_ mqtt.Client, msg mqtt.Message) {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					logging.Error("mqtt handler panic", "ClientName", b.config.ClientName, "topic", msg.Topic(), "err", r)
				}
			}()
			handler.OnMessage(ctx, msg.Topic(), msg.Payload())
		}()
	}
	token := b.client.Subscribe(fullTopic, byte(qos), onMessageHandler)

	timeout := b.config.SubscribeTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	select {
	case <-token.Done():
		if err := token.Error(); err != nil {
			return nil, err
		}

		b.mu.Lock()
		b.subs[topic] = token
		b.mu.Unlock()
		logging.Info("Subscribed to topic", "clientName", b.config.ClientName, "topic", fullTopic)
		return &msgSubscription{broker: b, topic: topic}, nil

	case <-time.After(timeout):
		return nil, fmt.Errorf("subscribe timeout for %s", topic)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// subscription wrapper
type msgSubscription struct {
	broker *MsgBroker
	topic  string
}

func (s *msgSubscription) Unsubscribe(ctx context.Context) error {
	b := s.broker
	token := b.client.Unsubscribe(s.topic)
	timeout := 3 * time.Second
	select {
	case <-token.Done():
		return token.Error()
	case <-time.After(timeout):
		return fmt.Errorf("unsubscribe timeout for %s", s.topic)
	case <-ctx.Done():
		return ctx.Err()
	}
}
