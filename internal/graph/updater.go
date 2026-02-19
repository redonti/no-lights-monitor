package graph

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	tele "gopkg.in/telebot.v3"

	"no-lights-monitor/internal/database"
	"no-lights-monitor/internal/models"
)

// Updater is a background service that creates / updates weekly graph
// images in each monitor's Telegram channel.
type Updater struct {
	db     *database.DB
	client *Client
	bot    *tele.Bot
}

// NewUpdater creates a graph updater.
func NewUpdater(db *database.DB, client *Client, bot *tele.Bot) *Updater {
	return &Updater{db: db, client: client, bot: bot}
}

// Start runs the hourly update loop. It fires once immediately, then every hour.
func (u *Updater) Start(ctx context.Context) {
	log.Println("[graph] updater started, waiting 30s for graph-service")
	select {
	case <-ctx.Done():
		return
	case <-time.After(30 * time.Second):
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

// UpdateSingle generates and sends/edits the graph for a single monitor.
// This is called externally (e.g., when a new monitor is created).
func (u *Updater) UpdateSingle(ctx context.Context, monitorID, channelID int64) error {
	now := time.Now().UTC()
	weekStart := currentWeekStart(now)

	// Fetch the current graph state from the DB.
	monitors, err := u.db.GetMonitorsWithChannels(ctx)
	if err != nil {
		return err
	}
	for _, m := range monitors {
		if m.ID == monitorID {
			return u.updateOne(ctx, m.ID, m.ChannelID, m.GraphMessageID, m.GraphWeekStart, weekStart, now)
		}
	}
	// Monitor just created, no graph yet.
	return u.updateOne(ctx, monitorID, channelID, 0, nil, weekStart, now)
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
		if err := u.updateOne(ctx, m.ID, m.ChannelID, m.GraphMessageID, m.GraphWeekStart, weekStart, now); err != nil {
			log.Printf("[graph] monitor %d: %v", m.ID, err)
		}
	}
}

// updateOne generates a graph PNG and sends or edits it in the channel.
func (u *Updater) updateOne(ctx context.Context, monitorID, channelID int64, oldMsgID int, oldWeekStart *time.Time, weekStart, now time.Time) error {
	// Determine if we need a new message (new week or first graph).
	needsNewMessage := oldMsgID == 0 || oldWeekStart == nil || !oldWeekStart.Equal(weekStart)

	// Fetch week events.
	events, err := u.db.GetStatusHistory(ctx, monitorID, weekStart, now)
	if err != nil {
		return fmt.Errorf("fetch events: %w", err)
	}

	// Prepend the last known event before week_start so the graph knows the
	// initial state for Monday regardless of when that event occurred.
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

	chat := &tele.Chat{ID: channelID}
	silent := &tele.SendOptions{DisableNotification: true}

	if needsNewMessage {
		// Send a brand-new photo message.
		photo := &tele.Photo{
			File:    tele.FromReader(pngReader(png)),
			Caption: fmt.Sprintf("📊 Тижневий графік (від %s)", weekStart.Format("02.01.2006")),
		}
		sent, err := u.bot.Send(chat, photo, silent)
		if err != nil {
			return fmt.Errorf("send photo: %w", err)
		}
		// Store the message ID so we can edit it later.
		if err := u.db.UpdateGraphMessage(ctx, monitorID, sent.ID, weekStart); err != nil {
			return fmt.Errorf("save message id: %w", err)
		}
		log.Printf("[graph] monitor %d: sent new graph (msg %d) for week %s", monitorID, sent.ID, weekStart.Format("2006-01-02"))
	} else {
		// Edit the existing photo in-place.
		editPhoto := &tele.Photo{
			File:    tele.FromReader(pngReader(png)),
			Caption: fmt.Sprintf("📊 Тижневий графік (від %s)", weekStart.Format("02.01.2006")),
		}
		editMsg := &tele.Message{
			ID:   oldMsgID,
			Chat: chat,
		}
		_, err := u.bot.EditMedia(editMsg, editPhoto)
		if err != nil {
			// "message is not modified" means the image is identical — not a real error.
			if strings.Contains(err.Error(), "message is not modified") {
				log.Printf("[graph] monitor %d: graph unchanged (msg %d)", monitorID, oldMsgID)
				return nil
			}
			// If edit fails (message deleted, etc.), send a new one with a fresh reader.
			log.Printf("[graph] monitor %d: edit failed (%v), sending new message", monitorID, err)
			fallbackPhoto := &tele.Photo{
				File:    tele.FromReader(pngReader(png)),
				Caption: fmt.Sprintf("📊 Тижневий графік (від %s)", weekStart.Format("02.01.2006")),
			}
			sent, sendErr := u.bot.Send(chat, fallbackPhoto, silent)
			if sendErr != nil {
				return fmt.Errorf("send fallback photo: %w", sendErr)
			}
			if err := u.db.UpdateGraphMessage(ctx, monitorID, sent.ID, weekStart); err != nil {
				return fmt.Errorf("save message id: %w", err)
			}
			log.Printf("[graph] monitor %d: sent fallback graph (msg %d)", monitorID, sent.ID)
		} else {
			log.Printf("[graph] monitor %d: updated graph (msg %d)", monitorID, oldMsgID)
		}
	}
	return nil
}

// namedReader wraps an io.Reader and adds a Name() so telebot can use it as a file.
type namedReader struct {
	io.Reader
	name string
}

func (nr *namedReader) Name() string { return nr.name }

func pngReader(data []byte) *namedReader {
	return &namedReader{Reader: bytes.NewReader(data), name: "graph.png"}
}
