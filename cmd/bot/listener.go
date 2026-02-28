package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"strings"
	"time"

	tele "gopkg.in/telebot.v3"

	"no-lights-monitor/internal/bot"
	"no-lights-monitor/internal/database"
	"no-lights-monitor/internal/models"
	"no-lights-monitor/internal/mq"
	"no-lights-monitor/internal/outage"
)

// listener consumes messages from RabbitMQ and handles them
// by sending Telegram messages, editing photos, etc.
type listener struct {
	bot          *tele.Bot
	db           *database.DB
	consumer     *mq.Consumer
	notifier     *bot.TelegramNotifier
}

func newListener(b *tele.Bot, db *database.DB, oc *outage.Client, consumer *mq.Consumer) *listener {
	return &listener{
		bot:      b,
		db:       db,
		consumer: consumer,
		notifier: bot.NewNotifier(b, db, oc),
	}
}

func (l *listener) start(ctx context.Context) {
	statusCh, err := l.consumer.Consume(mq.QueueStatusChange)
	if err != nil {
		log.Fatalf("[listener] failed to consume %s: %v", mq.QueueStatusChange, err)
	}
	graphCh, err := l.consumer.Consume(mq.QueueGraphReady)
	if err != nil {
		log.Fatalf("[listener] failed to consume %s: %v", mq.QueueGraphReady, err)
	}
	photoCh, err := l.consumer.Consume(mq.QueueOutagePhoto)
	if err != nil {
		log.Fatalf("[listener] failed to consume %s: %v", mq.QueueOutagePhoto, err)
	}

	log.Println("[listener] consuming from status_change, graph_ready, outage_photo")

	for {
		select {
		case <-ctx.Done():
			log.Println("[listener] stopped")
			return
		case d, ok := <-statusCh:
			if !ok {
				return
			}
			l.handleStatusChange(d.Body)
			d.Ack(false)
		case d, ok := <-graphCh:
			if !ok {
				return
			}
			l.handleGraphReady(ctx, d.Body)
			d.Ack(false)
		case d, ok := <-photoCh:
			if !ok {
				return
			}
			l.handleOutagePhoto(ctx, d.Body)
			d.Ack(false)
		}
	}
}

// ── Status change handler ────────────────────────────────────────────

func (l *listener) handleStatusChange(payload []byte) {
	var msg mq.StatusChangeMsg
	if err := json.Unmarshal(payload, &msg); err != nil {
		log.Printf("[listener] bad status_change message: %v", err)
		return
	}
	duration := time.Duration(msg.DurationSec * float64(time.Second))
	l.notifier.NotifyStatusChange(
		msg.MonitorID, msg.ChannelID, msg.Name, msg.Address,
		msg.NotifyAddress, msg.IsOnline, duration, msg.When,
		msg.OutageRegion, msg.OutageGroup, msg.NotifyOutage,
	)
}

// ── Graph ready handler ──────────────────────────────────────────────

func (l *listener) handleGraphReady(ctx context.Context, payload []byte) {
	var msg mq.GraphReadyMsg
	if err := json.Unmarshal(payload, &msg); err != nil {
		log.Printf("[listener] bad graph_ready message: %v", err)
		return
	}

	chat := &tele.Chat{ID: msg.ChannelID}
	silent := &tele.SendOptions{DisableNotification: bot.IsQuietHour()}

	if msg.NeedsNewMsg {
		photo := &tele.Photo{
			File:    tele.FromReader(namedReader(msg.ImagePNG, "graph.png")),
			Caption: msg.Caption,
		}
		sent, err := l.bot.Send(chat, photo, silent)
		if err != nil {
			l.handleChannelError(ctx, msg.MonitorID, msg.MonitorName, err)
			return
		}
		if err := l.db.UpdateGraphMessage(ctx, msg.MonitorID, sent.ID, msg.WeekStart); err != nil {
			log.Printf("[listener] graph monitor %d: failed to save message id: %v", msg.MonitorID, err)
		}
		log.Printf("[listener] graph monitor %d: sent new (msg %d)", msg.MonitorID, sent.ID)
	} else {
		editPhoto := &tele.Photo{
			File:    tele.FromReader(namedReader(msg.ImagePNG, "graph.png")),
			Caption: msg.Caption,
		}
		editMsg := &tele.Message{ID: msg.OldMsgID, Chat: chat}
		_, err := l.bot.EditMedia(editMsg, editPhoto)
		if err != nil {
			if strings.Contains(err.Error(), "message is not modified") {
				return
			}
			if l.handleChannelError(ctx, msg.MonitorID, msg.MonitorName, err) {
				return
			}
			// Fallback: send new message.
			log.Printf("[listener] graph monitor %d: edit failed (%v), sending new", msg.MonitorID, err)
			fallback := &tele.Photo{
				File:    tele.FromReader(namedReader(msg.ImagePNG, "graph.png")),
				Caption: msg.Caption,
			}
			sent, sendErr := l.bot.Send(chat, fallback, silent)
			if sendErr != nil {
				l.handleChannelError(ctx, msg.MonitorID, msg.MonitorName, sendErr)
				return
			}
			if err := l.db.UpdateGraphMessage(ctx, msg.MonitorID, sent.ID, msg.WeekStart); err != nil {
				log.Printf("[listener] graph monitor %d: failed to save message id: %v", msg.MonitorID, err)
			}
			log.Printf("[listener] graph monitor %d: sent fallback (msg %d)", msg.MonitorID, sent.ID)
		} else {
			log.Printf("[listener] graph monitor %d: updated (msg %d)", msg.MonitorID, msg.OldMsgID)
		}
	}
}

// ── Outage photo handler ─────────────────────────────────────────────

func (l *listener) handleOutagePhoto(ctx context.Context, payload []byte) {
	var msg mq.OutagePhotoMsg
	if err := json.Unmarshal(payload, &msg); err != nil {
		log.Printf("[listener] bad outage_photo message: %v", err)
		return
	}

	switch msg.Action {
	case mq.OutagePhotoDelete:
		l.deletePhoto(msg)
	case mq.OutagePhotoEdit:
		l.editPhoto(ctx, msg)
	case mq.OutagePhotoSend:
		l.sendPhoto(ctx, msg)
	default:
		log.Printf("[listener] outage_photo monitor %d: unknown action %q", msg.MonitorID, msg.Action)
	}
}

func (l *listener) deletePhoto(msg mq.OutagePhotoMsg) {
	if msg.OldMsgID == 0 {
		return
	}
	delMsg := &tele.Message{
		ID:   msg.OldMsgID,
		Chat: &tele.Chat{ID: msg.ChannelID},
	}
	if err := l.bot.Delete(delMsg); err != nil {
		log.Printf("[listener] outage_photo monitor %d: failed to delete (msg %d): %v", msg.MonitorID, msg.OldMsgID, err)
	}
}

func (l *listener) editPhoto(ctx context.Context, msg mq.OutagePhotoMsg) {
	chat := &tele.Chat{ID: msg.ChannelID}
	editPhoto := &tele.Photo{
		File: tele.FromReader(namedReader(msg.ImageData, msg.Filename)),
	}
	editTeleMsg := &tele.Message{ID: msg.OldMsgID, Chat: chat}

	_, err := l.bot.EditMedia(editTeleMsg, editPhoto)
	if err != nil {
		if strings.Contains(err.Error(), "message is not modified") {
			if err := l.db.UpdateOutagePhoto(ctx, msg.MonitorID, msg.OldMsgID, msg.ETag, time.Now()); err != nil {
				log.Printf("[listener] outage_photo monitor %d: failed to save timestamp: %v", msg.MonitorID, err)
			}
			return
		}
		if l.handleChannelError(ctx, msg.MonitorID, msg.MonitorName, err) {
			return
		}
		// Edit failed — delete old and send new.
		log.Printf("[listener] outage_photo monitor %d: edit failed (%v), sending new", msg.MonitorID, err)
		l.deletePhoto(msg)
		l.sendPhoto(ctx, msg)
		return
	}

	if err := l.db.UpdateOutagePhoto(ctx, msg.MonitorID, msg.OldMsgID, msg.ETag, time.Now()); err != nil {
		log.Printf("[listener] outage_photo monitor %d: failed to save photo id: %v", msg.MonitorID, err)
	}
	log.Printf("[listener] outage_photo monitor %d: updated (msg %d)", msg.MonitorID, msg.OldMsgID)
}

func (l *listener) sendPhoto(ctx context.Context, msg mq.OutagePhotoMsg) {
	chat := &tele.Chat{ID: msg.ChannelID}
	sendOpts := &tele.SendOptions{DisableNotification: bot.IsQuietHour()}
	photo := &tele.Photo{
		File: tele.FromReader(namedReader(msg.ImageData, msg.Filename)),
	}

	sent, err := l.bot.Send(chat, photo, sendOpts)
	if err != nil {
		l.handleChannelError(ctx, msg.MonitorID, msg.MonitorName, err)
		return
	}

	if err := l.db.UpdateOutagePhoto(ctx, msg.MonitorID, sent.ID, msg.ETag, time.Now()); err != nil {
		log.Printf("[listener] outage_photo monitor %d: failed to save photo id: %v", msg.MonitorID, err)
	}
	log.Printf("[listener] outage_photo monitor %d: sent new (msg %d)", msg.MonitorID, sent.ID)
}

// ── Helpers ──────────────────────────────────────────────────────────

// handleChannelError delegates to bot.NotifyChannelError.
// Returns true if the error was a channel error and was handled.
func (l *listener) handleChannelError(ctx context.Context, monitorID int64, monitorName string, err error) bool {
	ownerID, dbErr := l.db.GetOwnerTelegramIDByMonitorID(ctx, monitorID)
	if dbErr != nil {
		log.Printf("[listener] failed to get owner for monitor %d: %v", monitorID, dbErr)
		return false
	}
	monitor := &models.Monitor{ID: monitorID, Name: monitorName}
	return bot.NotifyChannelError(ctx, l.bot, l.db, err, ownerID, monitor)
}

// namedReaderImpl wraps an io.Reader with a Name() for telebot file uploads.
type namedReaderImpl struct {
	io.Reader
	name string
}

func (r *namedReaderImpl) Name() string { return r.name }

func namedReader(data []byte, name string) *namedReaderImpl {
	return &namedReaderImpl{Reader: bytes.NewReader(data), name: name}
}
