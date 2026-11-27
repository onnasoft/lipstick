package subscriptions

import (
	"fmt"
	"sync"

	"github.com/OnnaSoft/lipstick/logger"
	"github.com/OnnaSoft/lipstick/server/config"
	"github.com/nats-io/nats.go"
)

var (
	managerMu sync.Mutex
	manager   *SubscriptionManager
)

type SubscriptionManager struct {
	conn *nats.Conn
}

// GetSubscriptionManager returns the shared NATS connection. A failed connection
// is returned as an error and retried on the next call. It is never cached as
// nil, which would make callers dereference a missing manager.
func GetSubscriptionManager() (*SubscriptionManager, error) {
	conf, err := config.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("cannot read configuration: %w", err)
	}
	return sharedManager(conf.Nats.URL)
}

func sharedManager(url string) (*SubscriptionManager, error) {
	managerMu.Lock()
	defer managerMu.Unlock()

	if manager != nil {
		return manager, nil
	}
	m, err := NewSubscriptionManager(url)
	if err != nil {
		return nil, err
	}
	manager = m
	return m, nil
}

func NewSubscriptionManager(url string) (*SubscriptionManager, error) {
	conn, err := nats.Connect(url)
	if err != nil {
		return nil, fmt.Errorf("error connecting to NATS: %v", err)
	}

	logger.Default.Info("Connected to NATS:", url)
	return &SubscriptionManager{conn: conn}, nil
}

func (s *SubscriptionManager) Subscribe(topic string, callback func(msg *nats.Msg)) (*nats.Subscription, error) {
	sub, err := s.conn.QueueSubscribe(topic, "all", callback)
	if err != nil {
		return nil, fmt.Errorf("error subscribing to topic %s: %v", topic, err)
	}

	logger.Default.Info("Subscribed to topic: ", topic)
	return sub, nil
}

// SubscribeBroadcast subscribes to a topic without a queue group. Every
// subscriber receives every message, which is what presence needs. Subscribe
// uses a queue group, so each message reaches only one of its members.
func (s *SubscriptionManager) SubscribeBroadcast(topic string, callback func(msg *nats.Msg)) (*nats.Subscription, error) {
	sub, err := s.conn.Subscribe(topic, callback)
	if err != nil {
		return nil, fmt.Errorf("error subscribing to topic %s: %v", topic, err)
	}

	logger.Default.Info("Subscribed to topic (broadcast): ", topic)
	return sub, nil
}

func (s *SubscriptionManager) Unsubscribe() {
	s.conn.Drain()
	logger.Default.Info("All subscriptions drained and connection closed")
}

func (s *SubscriptionManager) Publish(topic string, message []byte) error {
	err := s.conn.Publish(topic, message)
	if err != nil {
		return fmt.Errorf("error publishing to topic %s: %v", topic, err)
	}

	logger.Default.Debug("Published message to topic: ", topic)
	return nil
}

func (s *SubscriptionManager) Close() {
	s.conn.Close()
	logger.Default.Info("NATS connection closed")
}
