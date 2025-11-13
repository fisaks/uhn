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

type BrokerConfig struct {
	BrokerURL         string
	ClientName        string
	TopicPrefix       string
	ConnectTimeout    time.Duration
	PublishTimeout    time.Duration
	SubscribeTimeout  time.Duration
	CommandBufferSize int
}

type MsgBroker struct {
	config         BrokerConfig
	client         mqtt.Client
	mu             sync.RWMutex
	subs           map[string]mqtt.Token
	onConnectFuncs map[string]OnConnectPublisher
}

type PublishRequest struct {
	// If Context is nil, context.Background() is used
	Context      context.Context
	Topic        string
	Qos          QoS
	Retain       bool
	PayloadBytes []byte
	Payload      interface{}
}

type OnConnectPublisher func() (PublishRequest, error)

func NewMsgBroker(cfg BrokerConfig) *MsgBroker {
	return &MsgBroker{
		config:         cfg,
		subs:           make(map[string]mqtt.Token),
		onConnectFuncs: make(map[string]OnConnectPublisher),
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
	opts.SetAutoReconnect(true)
	opts.OnConnect = func(c mqtt.Client) {
		b.onConnectPublisher()
	}
	return opts
}

func (b *MsgBroker) AddOnConnectPublisher(id string, fn OnConnectPublisher) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onConnectFuncs[id] = fn
}

func (b *MsgBroker) RemoveOnConnectPublisher(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.onConnectFuncs, id)
}

func (b *MsgBroker) onConnectPublisher() {
	b.mu.RLock()
	funcsCopy := make(map[string]OnConnectPublisher, len(b.onConnectFuncs))
	for k, v := range b.onConnectFuncs {
		funcsCopy[k] = v
	}
	b.mu.RUnlock()

	for id, fn := range funcsCopy {
		req, err := fn()
		if err != nil {
			logging.Error("onConnectPublisher failed", "clientName", b.config.ClientName, "id", id, "error", err)
			continue
		}
		ctx := req.Context
		if ctx == nil {
			ctx = context.Background()
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

func (b *MsgBroker) Publish(ctx context.Context, topic string, qos QoS, retain bool, payload []byte) error {
	if b.client == nil {
		return errors.New("client not initialized")
	}
	qosByte, wait := qosToByte(qos)
	token := b.client.Publish(topic, qosByte, retain, payload)
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
	return b.Publish(ctx, topic, qos, retain, data)
}

// Subscribe registers handler and waits for SUBACK with timeout
func (b *MsgBroker) Subscribe(ctx context.Context, topic string, qos QoS, handler func(context.Context, string, []byte)) (Subscription, error) {
	if b.client == nil {
		return nil, errors.New("client not initialized")
	}
	// wrapper that converts paho message to our handler and logs panics without crashing
	onMessageHandler := func(_ mqtt.Client, msg mqtt.Message) {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					logging.Error("mqtt handler panic", "ClientName", b.config.ClientName, "topic", msg.Topic(), "err", r)
				}
			}()
			handler(ctx, msg.Topic(), msg.Payload())
		}()
	}
	token := b.client.Subscribe(topic, byte(qos), onMessageHandler)

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
