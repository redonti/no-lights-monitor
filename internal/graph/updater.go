package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"no-lights-monitor/internal/database"
	"no-lights-monitor/internal/models"
	"no-lights-monitor/internal/mq"
)

// Updater is a background service that generates weekly graph images
// and publishes them to RabbitMQ for the bot service to send to Telegram.
type Updater struct {
	db     *database.DB
	client *Client
	pub    *mq.Publisher
}

// NewUpdater creates a graph updater.
func NewUpdater(db *database.DB, client *Client, pub *mq.Publisher) *Updater {
	return &Updater{db: db, client: client, pub: pub}
}

// Start runs the hourly update loop and listens for on-demand graph requests.
func (u *Updater) Start(ctx context.Context, consumer *mq.Consumer) {
	log.Println("[graph] updater started, waiting 30s for graph-service")
	select {
	case <-ctx.Done():
		return
	case <-time.After(30 * time.Second):
	}

	// Listen for on-demand graph requests from the bot service.
	if consumer != nil {
		go u.listenRequests(ctx, consumer)
	}

	log.Println("[graph] running initial pass")
	u.runAll(ctx)

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("[graph] updater stopped")
			return
		case <-ticker.C:
			u.runAll(ctx)
		}
	}
}

// listenRequests consumes graph request messages from the bot and generates graphs on-demand.
func (u *Updater) listenRequests(ctx context.Context, consumer *mq.Consumer) {
	deliveries, err := consumer.Consume(mq.QueueGraphRequest)
	if err != nil {
		log.Printf("[graph] failed to consume graph requests: %v", err)
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case d, ok := <-deliveries:
			if !ok {
				return
			}
			u.handleRequest(ctx, d)
		}
	}
}

func (u *Updater) handleRequest(ctx context.Context, d amqp.Delivery) {
	var msg mq.GraphRequestMsg
	if err := json.Unmarshal(d.Body, &msg); err != nil {
		log.Printf("[graph] bad graph request: %v", err)
		d.Nack(false, false)
		return
	}
	if err := u.UpdateSingle(ctx, msg.MonitorID, msg.ChannelID); err != nil {
		log.Printf("[graph] on-demand graph for monitor %d failed: %v", msg.MonitorID, err)
	}
	d.Ack(false)
}

// currentWeekStart returns Monday 00:00 UTC for the week containing t.
func currentWeekStart(t time.Time) time.Time {
	t = t.UTC()
	weekday := t.Weekday()
	if weekday == time.Sunday {
		weekday = 7
	}
	monday := t.AddDate(0, 0, -int(weekday-time.Monday))
	return time.Date(monday.Year(), monday.Month(), monday.Day(), 0, 0, 0, 0, time.UTC)
}

// UpdateSingle generates and publishes the graph for a single monitor.
func (u *Updater) UpdateSingle(ctx context.Context, monitorID, channelID int64) error {
	now := time.Now().UTC()
	weekStart := currentWeekStart(now)

	monitors, err := u.db.GetMonitorsWithChannels(ctx)
	if err != nil {
		return err
	}
	for _, m := range monitors {
		if m.ID == monitorID {
			if !m.GraphEnabled {
				return nil
			}
			return u.updateOne(ctx, m.ID, m.ChannelID, m.Name, m.Address, m.NotifyAddress, m.GraphMessageID, m.GraphWeekStart, weekStart, now)
		}
	}
	// Monitor just created — graph_enabled defaults to true, so post.
	return u.updateOne(ctx, monitorID, channelID, "", "", false, 0, nil, weekStart, now)
}

// runAll iterates over every monitor with a channel and updates its graph.
func (u *Updater) runAll(ctx context.Context) {
	monitors, err := u.db.GetMonitorsWithChannels(ctx)
	if err != nil {
		log.Printf("[graph] failed to list monitors: %v", err)
		return
	}
	log.Printf("[graph] updating graphs for %d monitors", len(monitors))

	now := time.Now().UTC()
	weekStart := currentWeekStart(now)

	for _, m := range monitors {
		if !m.GraphEnabled {
			continue
		}
		if err := u.updateOne(ctx, m.ID, m.ChannelID, m.Name, m.Address, m.NotifyAddress, m.GraphMessageID, m.GraphWeekStart, weekStart, now); err != nil {
			log.Printf("[graph] monitor %d: %v", m.ID, err)
		}
	}
}

// updateOne generates a graph PNG and publishes a message for the bot service.
func (u *Updater) updateOne(ctx context.Context, monitorID, channelID int64, monitorName, monitorAddress string, notifyAddress bool, oldMsgID int, oldWeekStart *time.Time, weekStart, now time.Time) error {
	needsNewMessage := oldMsgID == 0 || oldWeekStart == nil || !oldWeekStart.Equal(weekStart)

	caption := fmt.Sprintf("📊 Тижневий графік (від %s)", weekStart.Format("02.01.2006"))
	if notifyAddress && monitorAddress != "" {
		caption += fmt.Sprintf("\n📍 %s", monitorAddress)
	}

	// Fetch week events.
	events, err := u.db.GetStatusHistory(ctx, monitorID, weekStart, now)
	if err != nil {
		return fmt.Errorf("fetch events: %w", err)
	}

	anchor, err := u.db.GetLastEventBefore(ctx, monitorID, weekStart)
	if err != nil {
		return fmt.Errorf("fetch anchor event: %w", err)
	}
	if anchor != nil {
		events = append([]*models.StatusEvent{anchor}, events...)
	}

	// Call graph service.
	png, err := u.client.GenerateWeekGraph(monitorID, weekStart, events)
	if err != nil {
		return fmt.Errorf("generate graph: %w", err)
	}

	// Publish to RabbitMQ for the bot service to send to Telegram.
	msg := mq.GraphReadyMsg{
		MonitorID:      monitorID,
		ChannelID:      channelID,
		MonitorName:    monitorName,
		MonitorAddress: monitorAddress,
		NotifyAddress:  notifyAddress,
		WeekStart:      weekStart,
		OldMsgID:       oldMsgID,
		NeedsNewMsg:    needsNewMessage,
		ImagePNG:       png,
		Caption:        caption,
	}
	if err := u.pub.Publish(ctx, mq.RoutingGraphReady, msg); err != nil {
		return fmt.Errorf("publish graph: %w", err)
	}

	log.Printf("[graph] monitor %d: published graph for week %s (new=%v)", monitorID, weekStart.Format("2006-01-02"), needsNewMessage)
	return nil
}
