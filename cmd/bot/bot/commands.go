package bot

import (
	"context"
	"fmt"
	"html"
	"log"
	"strings"

	"no-lights-monitor/internal/models"

	tele "gopkg.in/telebot.v3"
)

// ── Simple commands ──────────────────────────────────────────────────

func (b *Bot) handleStart(c tele.Context) error {
	log.Printf("[bot] /start from user %d (@%s)", c.Sender().ID, c.Sender().Username)
	return c.Send(fmt.Sprintf(msgStart, b.baseURL, b.chatUsername), tele.ModeHTML, mainMenu)
}

func (b *Bot) handleHelp(c tele.Context) error {
	log.Printf("[bot] /help from user %d (@%s)", c.Sender().ID, c.Sender().Username)
	return c.Send(fmt.Sprintf(msgHelp, b.baseURL, b.chatUsername), htmlOpts)
}

func (b *Bot) handleCancel(c tele.Context) error {
	log.Printf("[bot] /cancel from user %d (@%s)", c.Sender().ID, c.Sender().Username)
	b.mu.Lock()
	delete(b.conversations, c.Sender().ID)
	b.mu.Unlock()
	return c.Send(msgCancelled, mainMenu)
}

// ── /stop ────────────────────────────────────────────────────────────

func (b *Bot) handleStop(c tele.Context) error {
	log.Printf("[bot] /stop from user %d (@%s)", c.Sender().ID, c.Sender().Username)
	ctx := context.Background()
	monitors, err := b.db.GetMonitorsByTelegramID(ctx, c.Sender().ID)
	if err != nil {
		log.Printf("[bot] get monitors error: %v", err)
		return c.Send(msgError)
	}

	// Filter only active monitors.
	var active []*models.Monitor
	for _, m := range monitors {
		if m.IsActive {
			active = append(active, m)
		}
	}

	if len(active) == 0 {
		return c.Send(msgNoActiveMonitors)
	}

	var bld strings.Builder
	bld.WriteString(msgStopHeader)

	rows := make([][]tele.InlineButton, 0, len(active))
	for i, m := range active {
		bld.WriteString(fmt.Sprintf("%d. %s\n", i+1, html.EscapeString(m.Name)))
		rows = append(rows, []tele.InlineButton{
			{
				Text: fmt.Sprintf("%d. %s", i+1, m.Name),
				Data: fmt.Sprintf("stop:%d", m.ID),
			},
		})
	}

	keyboard := &tele.ReplyMarkup{InlineKeyboard: rows}
	return c.Send(bld.String(), tele.ModeHTML, keyboard)
}

// ── /resume ──────────────────────────────────────────────────────────

func (b *Bot) handleResume(c tele.Context) error {
	log.Printf("[bot] /resume from user %d (@%s)", c.Sender().ID, c.Sender().Username)
	ctx := context.Background()
	monitors, err := b.db.GetMonitorsByTelegramID(ctx, c.Sender().ID)
	if err != nil {
		log.Printf("[bot] get monitors error: %v", err)
		return c.Send(msgError)
	}

	// Filter only inactive monitors.
	var inactive []*models.Monitor
	for _, m := range monitors {
		if !m.IsActive {
			inactive = append(inactive, m)
		}
	}

	if len(inactive) == 0 {
		return c.Send(msgNoInactiveMonitors)
	}

	var bld strings.Builder
	bld.WriteString(msgResumeHeader)

	rows := make([][]tele.InlineButton, 0, len(inactive))
	for i, m := range inactive {
		bld.WriteString(fmt.Sprintf("%d. %s\n", i+1, html.EscapeString(m.Name)))
		rows = append(rows, []tele.InlineButton{
			{
				Text: fmt.Sprintf("%d. %s", i+1, m.Name),
				Data: fmt.Sprintf("resume:%d", m.ID),
			},
		})
	}

	keyboard := &tele.ReplyMarkup{InlineKeyboard: rows}
	return c.Send(bld.String(), tele.ModeHTML, keyboard)
}

// ── /info ────────────────────────────────────────────────────────────

func (b *Bot) handleInfo(c tele.Context) error {
	log.Printf("[bot] /info from user %d (@%s)", c.Sender().ID, c.Sender().Username)
	ctx := context.Background()
	monitors, err := b.db.GetMonitorsByTelegramID(ctx, c.Sender().ID)
	if err != nil {
		log.Printf("[bot] get monitors error: %v", err)
		return c.Send(msgError)
	}

	if len(monitors) == 0 {
		return c.Send(msgNoMonitors)
	}

	var bld strings.Builder
	bld.WriteString(msgInfoHeader)

	rows := make([][]tele.InlineButton, 0, len(monitors))
	for i, m := range monitors {
		status := msgInfoStatusOffline
		if m.IsOnline {
			status = msgInfoStatusOnline
		}
		if !m.IsActive {
			status = msgStatusPaused
		}

		bld.WriteString(fmt.Sprintf(msgInfoRow, i+1, html.EscapeString(m.Name), status))
		rows = append(rows, []tele.InlineButton{
			{
				Text: fmt.Sprintf("%d. %s", i+1, m.Name),
				Data: fmt.Sprintf("info:%d", m.ID),
			},
		})
	}

	keyboard := &tele.ReplyMarkup{InlineKeyboard: rows}
	return c.Send(bld.String(), tele.ModeHTML, keyboard)
}

// ── /test ────────────────────────────────────────────────────────────

func (b *Bot) handleTest(c tele.Context) error {
	log.Printf("[bot] /test from user %d (@%s)", c.Sender().ID, c.Sender().Username)
	ctx := context.Background()
	monitors, err := b.db.GetMonitorsByTelegramID(ctx, c.Sender().ID)
	if err != nil {
		log.Printf("[bot] get monitors error: %v", err)
		return c.Send(msgError)
	}

	// Filter monitors with channels
	var withChannels []*models.Monitor
	for _, m := range monitors {
		if m.ChannelID != 0 {
			withChannels = append(withChannels, m)
		}
	}

	if len(withChannels) == 0 {
		return c.Send(msgNoTestChannels)
	}

	var bld strings.Builder
	bld.WriteString(msgTestHeader)

	rows := make([][]tele.InlineButton, 0, len(withChannels))
	for i, m := range withChannels {
		bld.WriteString(fmt.Sprintf(msgTestRow, i+1, html.EscapeString(m.Name), html.EscapeString(m.ChannelName)))
		rows = append(rows, []tele.InlineButton{
			{
				Text: fmt.Sprintf("%d. %s", i+1, m.Name),
				Data: fmt.Sprintf("test:%d", m.ID),
			},
		})
	}

	keyboard := &tele.ReplyMarkup{InlineKeyboard: rows}
	return c.Send(bld.String(), tele.ModeHTML, keyboard)
}

// ── /delete ──────────────────────────────────────────────────────────

func (b *Bot) handleDelete(c tele.Context) error {
	log.Printf("[bot] /delete from user %d (@%s)", c.Sender().ID, c.Sender().Username)
	ctx := context.Background()
	monitors, err := b.db.GetMonitorsByTelegramID(ctx, c.Sender().ID)
	if err != nil {
		log.Printf("[bot] get monitors error: %v", err)
		return c.Send(msgError)
	}

	if len(monitors) == 0 {
		return c.Send(msgNoMonitorsDelete)
	}

	var bld strings.Builder
	bld.WriteString(msgDeleteHeader)

	rows := make([][]tele.InlineButton, 0, len(monitors))
	for i, m := range monitors {
		bld.WriteString(fmt.Sprintf("%d. %s\n", i+1, html.EscapeString(m.Name)))
		rows = append(rows, []tele.InlineButton{
			{
				Text: fmt.Sprintf("🗑 %d. %s", i+1, m.Name),
				Data: fmt.Sprintf("delete_confirm:%d", m.ID),
			},
		})
	}

	keyboard := &tele.ReplyMarkup{InlineKeyboard: rows}
	return c.Send(bld.String(), tele.ModeHTML, keyboard)
}

// ── /edit ────────────────────────────────────────────────────────────

func (b *Bot) handleEdit(c tele.Context) error {
	log.Printf("[bot] /edit from user %d (@%s)", c.Sender().ID, c.Sender().Username)
	ctx := context.Background()
	monitors, err := b.db.GetMonitorsByTelegramID(ctx, c.Sender().ID)
	if err != nil {
		log.Printf("[bot] get monitors error: %v", err)
		return c.Send(msgError)
	}

	if len(monitors) == 0 {
		return c.Send(msgNoMonitors)
	}

	var bld strings.Builder
	bld.WriteString(msgEditHeader)

	rows := make([][]tele.InlineButton, 0, len(monitors))
	for i, m := range monitors {
		bld.WriteString(fmt.Sprintf("%d. %s\n", i+1, html.EscapeString(m.Name)))
		rows = append(rows, []tele.InlineButton{
			{
				Text: fmt.Sprintf("%d. %s", i+1, m.Name),
				Data: fmt.Sprintf("edit:%d", m.ID),
			},
		})
	}

	keyboard := &tele.ReplyMarkup{InlineKeyboard: rows}
	return c.Send(bld.String(), tele.ModeHTML, keyboard)
}
