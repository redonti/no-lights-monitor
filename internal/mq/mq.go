package mq

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Exchange and queue/routing key constants.
const (
	ExchangeName = "nlm"

	RoutingStatusChange = "status.change"
	RoutingGraphReady   = "graph.ready"
	RoutingOutagePhoto  = "outage.photo"
	RoutingGraphRequest = "graph.request"

	QueueStatusChange = "nlm.status_change"
	QueueGraphReady   = "nlm.graph_ready"
	QueueOutagePhoto  = "nlm.outage_photo"
	QueueGraphRequest = "nlm.graph_request"
)

// ── Message types ────────────────────────────────────────────────────

// StatusChangeMsg is published by the worker when a monitor changes status.
type StatusChangeMsg struct {
	MonitorID     int64     `json:"monitor_id"`
	ChannelID     int64     `json:"channel_id"`
	Name          string    `json:"name"`
	Address       string    `json:"address"`
	NotifyAddress bool      `json:"notify_address"`
	IsOnline      bool      `json:"is_online"`
	DurationSec   float64   `json:"duration_sec"`
	When          time.Time `json:"when"`
	OutageRegion  string    `json:"outage_region"`
	OutageGroup   string    `json:"outage_group"`
	NotifyOutage  bool      `json:"notify_outage"`
}

// GraphReadyMsg is published by the worker when a graph image is generated.
type GraphReadyMsg struct {
	MonitorID      int64     `json:"monitor_id"`
	ChannelID      int64     `json:"channel_id"`
	MonitorName    string    `json:"monitor_name"`
	MonitorAddress string    `json:"monitor_address"`
	NotifyAddress  bool      `json:"notify_address"`
	WeekStart      time.Time `json:"week_start"`
	OldMsgID       int       `json:"old_msg_id"`
	NeedsNewMsg    bool      `json:"needs_new_msg"`
	ImagePNG       []byte    `json:"image_png"`
	Caption        string    `json:"caption"`
}

// OutagePhotoAction specifies what the bot should do with an outage photo.
type OutagePhotoAction string

const (
	OutagePhotoSend   OutagePhotoAction = "send"
	OutagePhotoEdit   OutagePhotoAction = "edit"
	OutagePhotoDelete OutagePhotoAction = "delete"
)

// OutagePhotoMsg is published by the worker when an outage photo needs action.
type OutagePhotoMsg struct {
	MonitorID   int64             `json:"monitor_id"`
	ChannelID   int64             `json:"channel_id"`
	MonitorName string            `json:"monitor_name"`
	Action      OutagePhotoAction `json:"action"`
	OldMsgID    int               `json:"old_msg_id"`
	ImageData   []byte            `json:"image_data,omitempty"`
	Filename    string            `json:"filename,omitempty"`
	ETag        string            `json:"etag,omitempty"`
}

// GraphRequestMsg is published by the bot to request immediate graph generation.
type GraphRequestMsg struct {
	MonitorID int64 `json:"monitor_id"`
	ChannelID int64 `json:"channel_id"`
}

// ── Topology setup ───────────────────────────────────────────────────

// queues maps queue names to their routing keys.
var queues = map[string]string{
	QueueStatusChange: RoutingStatusChange,
	QueueGraphReady:   RoutingGraphReady,
	QueueOutagePhoto:  RoutingOutagePhoto,
	QueueGraphRequest: RoutingGraphRequest,
}

// SetupTopology declares the exchange, all queues, and bindings.
// Safe to call multiple times (all declarations are idempotent).
func SetupTopology(ch *amqp.Channel) error {
	if err := ch.ExchangeDeclare(ExchangeName, "topic", true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare exchange: %w", err)
	}
	for queue, key := range queues {
		if _, err := ch.QueueDeclare(queue, true, false, false, false, nil); err != nil {
			return fmt.Errorf("declare queue %s: %w", queue, err)
		}
		if err := ch.QueueBind(queue, key, ExchangeName, false, nil); err != nil {
			return fmt.Errorf("bind queue %s: %w", queue, err)
		}
	}
	return nil
}

// ── Publisher ────────────────────────────────────────────────────────

// Publisher publishes messages to the RabbitMQ exchange.
type Publisher struct {
	conn *amqp.Connection
	ch   *amqp.Channel
}

// NewPublisher connects to RabbitMQ, sets up topology, and returns a Publisher.
func NewPublisher(url string) (*Publisher, error) {
	conn, err := dialWithRetry(url)
	if err != nil {
		return nil, err
	}
	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("open channel: %w", err)
	}
	if err := SetupTopology(ch); err != nil {
		ch.Close()
		conn.Close()
		return nil, err
	}
	return &Publisher{conn: conn, ch: ch}, nil
}

// Publish serializes msg to JSON and publishes it with the given routing key.
func (p *Publisher) Publish(ctx context.Context, routingKey string, msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}
	return p.ch.PublishWithContext(ctx, ExchangeName, routingKey, false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Body:         data,
	})
}

// Close closes the channel and connection.
func (p *Publisher) Close() {
	if p.ch != nil {
		p.ch.Close()
	}
	if p.conn != nil {
		p.conn.Close()
	}
}

// ── Consumer ─────────────────────────────────────────────────────────

// Consumer consumes messages from RabbitMQ queues.
type Consumer struct {
	conn *amqp.Connection
	ch   *amqp.Channel
}

// NewConsumer connects to RabbitMQ, sets up topology, and returns a Consumer.
func NewConsumer(url string) (*Consumer, error) {
	conn, err := dialWithRetry(url)
	if err != nil {
		return nil, err
	}
	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("open channel: %w", err)
	}
	if err := SetupTopology(ch); err != nil {
		ch.Close()
		conn.Close()
		return nil, err
	}
	// Process one message at a time per consumer.
	if err := ch.Qos(1, 0, false); err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("set qos: %w", err)
	}
	return &Consumer{conn: conn, ch: ch}, nil
}

// Consume starts consuming from the given queue and returns a delivery channel.
func (c *Consumer) Consume(queue string) (<-chan amqp.Delivery, error) {
	return c.ch.Consume(queue, "", false, false, false, false, nil)
}

// Close closes the channel and connection.
func (c *Consumer) Close() {
	if c.ch != nil {
		c.ch.Close()
	}
	if c.conn != nil {
		c.conn.Close()
	}
}

// ── Helpers ──────────────────────────────────────────────────────────

// dialWithRetry attempts to connect to RabbitMQ with exponential backoff.
func dialWithRetry(url string) (*amqp.Connection, error) {
	var conn *amqp.Connection
	var err error
	for i := range 5 {
		conn, err = amqp.Dial(url)
		if err == nil {
			return conn, nil
		}
		wait := time.Duration(1<<uint(i)) * time.Second
		log.Printf("[mq] connection attempt %d failed: %v, retrying in %s", i+1, err, wait)
		time.Sleep(wait)
	}
	return nil, fmt.Errorf("connect to rabbitmq after 5 attempts: %w", err)
}
