package bot

import (
	"context"
	"fmt"
	"html"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"no-lights-monitor/internal/database"
	"no-lights-monitor/internal/geocode"
	"no-lights-monitor/internal/heartbeat"
	"no-lights-monitor/internal/models"

	tele "gopkg.in/telebot.v3"
)

// conversationState tracks where a user is in the registration flow.
type conversationState int

const (
	stateIdle conversationState = iota
	stateAwaitingType
	stateAwaitingPingTarget
	stateAwaitingAddress
	stateAwaitingChannel
)

type conversationData struct {
	State       conversationState
	MonitorType string // "heartbeat" or "ping"
	PingTarget  string // IP/hostname for ping monitors
	Name        string
	Address     string
	Latitude    float64
	Longitude   float64
}

// GraphUpdater is used to trigger a graph update for a newly created monitor.
type GraphUpdater interface {
	UpdateSingle(ctx context.Context, monitorID, channelID int64) error
}

// Bot wraps the Telegram bot and registration conversation logic.
type Bot struct {
	bot           *tele.Bot
	db            *database.DB
	heartbeatSvc  *heartbeat.Service
	baseURL       string
	graphUpdater  GraphUpdater
	conversations map[int64]*conversationData
	mu            sync.RWMutex
}

var htmlOpts = &tele.SendOptions{ParseMode: tele.ModeHTML}

// New creates and configures the Telegram bot.
func New(token string, db *database.DB, hbSvc *heartbeat.Service, baseURL string) (*Bot, error) {
	pref := tele.Settings{
		Token:  token,
		Poller: &tele.LongPoller{Timeout: 10 * time.Second},
	}

	b, err := tele.NewBot(pref)
	if err != nil {
		return nil, fmt.Errorf("create bot: %w", err)
	}

	bot := &Bot{
		bot:           b,
		db:            db,
		heartbeatSvc:  hbSvc,
		baseURL:       baseURL,
		conversations: make(map[int64]*conversationData),
	}

	bot.registerHandlers()
	return bot, nil
}

// Start begins polling for Telegram updates. Call as a goroutine.
func (b *Bot) Start() {
	log.Println("[bot] starting Telegram bot polling...")
	b.bot.Start()
}

// Stop gracefully stops the bot.
func (b *Bot) Stop() {
	b.bot.Stop()
}

// SetGraphUpdater wires the graph updater after initialization (avoids circular deps).
func (b *Bot) SetGraphUpdater(g GraphUpdater) {
	b.graphUpdater = g
}

// TeleBot returns the underlying telebot instance (used by the notifier).
func (b *Bot) TeleBot() *tele.Bot {
	return b.bot
}

func (b *Bot) registerHandlers() {
	b.bot.Handle("/start", b.handleStart)
	b.bot.Handle("/create", b.handleCreate)
	b.bot.Handle("/status", b.handleStatus)
	b.bot.Handle("/info", b.handleInfo)
	b.bot.Handle("/stop", b.handleStop)
	b.bot.Handle("/resume", b.handleResume)
	b.bot.Handle("/test", b.handleTest)
	b.bot.Handle("/delete", b.handleDelete)
	b.bot.Handle("/help", b.handleHelp)
	b.bot.Handle("/cancel", b.handleCancel)

	// Callback queries for inline buttons.
	b.bot.Handle(tele.OnCallback, b.handleCallback)

	// Handle all text messages for conversation flow.
	b.bot.Handle(tele.OnText, b.handleText)

	// Handle location sharing.
	b.bot.Handle(tele.OnLocation, b.handleLocation)
}

// ── Commands ─────────────────────────────────────────────────────────

func (b *Bot) handleStart(c tele.Context) error {
	log.Printf("[bot] /start from user %d (@%s)", c.Sender().ID, c.Sender().Username)
	return c.Send(msgStart, htmlOpts)
}

func (b *Bot) handleHelp(c tele.Context) error {
	log.Printf("[bot] /help from user %d (@%s)", c.Sender().ID, c.Sender().Username)
	return c.Send(msgHelp, htmlOpts)
}

func (b *Bot) handleCancel(c tele.Context) error {
	log.Printf("[bot] /cancel from user %d (@%s)", c.Sender().ID, c.Sender().Username)
	b.mu.Lock()
	delete(b.conversations, c.Sender().ID)
	b.mu.Unlock()
	return c.Send(msgCancelled)
}

func (b *Bot) handleStatus(c tele.Context) error {
	log.Printf("[bot] /status from user %d (@%s)", c.Sender().ID, c.Sender().Username)
	ctx := context.Background()
	monitors, err := b.db.GetMonitorsByTelegramID(ctx, c.Sender().ID)
	if err != nil {
		log.Printf("[bot] get monitors by telegram_id error: %v", err)
		return c.Send(msgError)
	}

	if len(monitors) == 0 {
		return c.Send(msgNoMonitors)
	}

	now := time.Now()
	var bld strings.Builder
	bld.WriteString(msgStatusHeader)

	for i, m := range monitors {
		dur := now.Sub(m.LastStatusChangeAt)
		durStr := database.FormatDuration(dur)
		status := msgStatusOffline
		if m.IsOnline {
			status = msgStatusOnline
		}
		if !m.IsActive {
			status = msgStatusPaused
		}
		bld.WriteString(fmt.Sprintf("<b>%d.</b> %s\n", i+1, html.EscapeString(m.Name)))
		bld.WriteString(fmt.Sprintf("   %s\n", html.EscapeString(m.Address)))
		if m.IsActive {
			bld.WriteString(fmt.Sprintf("   %s — %s\n", status, durStr))
		} else {
			bld.WriteString(fmt.Sprintf("   %s\n", status))
		}
		if m.ChannelName != "" {
			bld.WriteString(fmt.Sprintf("   Канал: @%s\n", html.EscapeString(m.ChannelName)))
		}
		bld.WriteString("\n")
	}

	bld.WriteString(msgStatusFooter)

	return c.Send(bld.String(), htmlOpts)
}

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

func (b *Bot) handleCallback(c tele.Context) error {
	log.Printf("[bot] callback %q from user %d (@%s)", c.Callback().Data, c.Sender().ID, c.Sender().Username)
	data := c.Callback().Data
	parts := strings.Split(data, ":")
	if len(parts) != 2 {
		return c.Respond(&tele.CallbackResponse{Text: msgInvalidFormat})
	}

	action := parts[0]

	// Handle create_type callback (no monitor ID needed).
	if action == "create_type" {
		return b.onCreateType(c, parts[1])
	}

	monitorID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return c.Respond(&tele.CallbackResponse{Text: msgInvalidMonitor})
	}

	ctx := context.Background()

	// Get all monitors and find the right one
	monitors, err := b.db.GetMonitorsByTelegramID(ctx, c.Sender().ID)
	if err != nil {
		log.Printf("[bot] get monitors error: %v", err)
		return c.Respond(&tele.CallbackResponse{Text: msgFetchError})
	}

	var targetMonitor *models.Monitor
	for _, m := range monitors {
		if m.ID == monitorID {
			targetMonitor = m
			break
		}
	}

	if targetMonitor == nil {
		return c.Respond(&tele.CallbackResponse{Text: msgMonitorNotFound})
	}

	switch action {
	case "stop":
		if err := b.db.SetMonitorActive(ctx, monitorID, false); err != nil {
			log.Printf("[bot] set monitor inactive error: %v", err)
			return c.Respond(&tele.CallbackResponse{Text: msgStopError})
		}
		b.heartbeatSvc.SetMonitorActive(targetMonitor.Token, false)
		_ = c.Respond(&tele.CallbackResponse{Text: msgStopOK})
		return c.Send(fmt.Sprintf("%s <b>%s</b> призупинено.\n\nВідновити можна через /resume", msgStopOK, html.EscapeString(targetMonitor.Name)), htmlOpts)

	case "resume":
		if err := b.db.SetMonitorActive(ctx, monitorID, true); err != nil {
			log.Printf("[bot] set monitor active error: %v", err)
			return c.Respond(&tele.CallbackResponse{Text: msgResumeError})
		}
		b.heartbeatSvc.SetMonitorActive(targetMonitor.Token, true)
		_ = c.Respond(&tele.CallbackResponse{Text: msgResumeOK})
		return c.Send(fmt.Sprintf("%s <b>%s</b> відновлено.\n\nПризупинити можна через /stop", msgResumeOK, html.EscapeString(targetMonitor.Name)), htmlOpts)

	case "delete_confirm":
		if err := b.db.DeleteMonitor(ctx, monitorID); err != nil {
			log.Printf("[bot] delete monitor error: %v", err)
			return c.Respond(&tele.CallbackResponse{Text: msgDeleteError})
		}
		b.heartbeatSvc.RemoveMonitor(targetMonitor.Token)
		_ = c.Respond(&tele.CallbackResponse{Text: msgDeleteOK})
		return c.Send(fmt.Sprintf("%s <b>%s</b> успішно видалено.", msgDeleteOK, html.EscapeString(targetMonitor.Name)), htmlOpts)

	case "info":
		_ = c.Respond(&tele.CallbackResponse{})

		var bld strings.Builder
		bld.WriteString("<b>📊 Інформація про монітор</b>\n\n")
		bld.WriteString(fmt.Sprintf("🏷 <b>Назва:</b> %s\n", html.EscapeString(targetMonitor.Name)))
		bld.WriteString(fmt.Sprintf("📍 <b>Адреса:</b> %s\n", html.EscapeString(targetMonitor.Address)))
		bld.WriteString(fmt.Sprintf("🌐 <b>Координати:</b> %.6f, %.6f\n\n", targetMonitor.Latitude, targetMonitor.Longitude))

		status := msgInfoStatusOffline
		if targetMonitor.IsOnline {
			status = msgInfoStatusOnline
		}
		if !targetMonitor.IsActive {
			status = msgStatusPaused
		}
		bld.WriteString(fmt.Sprintf("<b>Статус:</b> %s\n", status))

		if targetMonitor.LastHeartbeatAt != nil {
			bld.WriteString(fmt.Sprintf("<b>Останній пінг:</b> %s\n", targetMonitor.LastHeartbeatAt.Format("2006-01-02 15:04:05")))
		}

		if targetMonitor.ChannelID != 0 {
			bld.WriteString(fmt.Sprintf("<b>Канал:</b> @%s\n\n", html.EscapeString(targetMonitor.ChannelName)))
		} else {
			bld.WriteString("\n")
		}

		if targetMonitor.MonitorType == "ping" {
			bld.WriteString(fmt.Sprintf("<b>🌐 Тип:</b> %s\n", msgInfoTypePing))
			bld.WriteString(fmt.Sprintf("<b>🎯 Ціль:</b> <code>%s</code>\n\n", html.EscapeString(targetMonitor.PingTarget)))
			bld.WriteString(msgInfoPingHint)
		} else {
			bld.WriteString(fmt.Sprintf("<b>📡 Тип:</b> %s\n", msgInfoTypeHeartbeat))
			bld.WriteString("<b>🔗 URL для пінгу:</b>\n")
			bld.WriteString(fmt.Sprintf("<code>%s/api/ping/%s</code>\n\n", b.baseURL, targetMonitor.Token))
			bld.WriteString(msgInfoHeartbeatHint)
		}

		return c.Send(bld.String(), htmlOpts)

	case "test":
		if targetMonitor.ChannelID == 0 {
			return c.Respond(&tele.CallbackResponse{Text: msgTestNoChannel})
		}

		testMsg := fmt.Sprintf(
			"🧪 <b>Тестове повідомлення</b>\n\n"+
				"Монітор: <b>%s</b>\n"+
				"Адреса: %s\n\n"+
				"Якщо ви бачите це повідомлення, то налаштування каналу працює коректно! ✅",
			html.EscapeString(targetMonitor.Name),
			html.EscapeString(targetMonitor.Address),
		)

		chat := &tele.Chat{ID: targetMonitor.ChannelID}
		if _, err := b.bot.Send(chat, testMsg, htmlOpts); err != nil {
			log.Printf("[bot] test notification error: %v", err)
			return c.Respond(&tele.CallbackResponse{Text: msgTestSendError})
		}

		_ = c.Respond(&tele.CallbackResponse{Text: msgTestOK})
		return c.Send(fmt.Sprintf("%s відправлено в канал <b>@%s</b>", msgTestOK, html.EscapeString(targetMonitor.ChannelName)), htmlOpts)

	default:
		return c.Respond(&tele.CallbackResponse{Text: msgUnknownAction})
	}
}

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

		bld.WriteString(fmt.Sprintf("<b>%d.</b> %s - %s\n", i+1, html.EscapeString(m.Name), status))
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
		bld.WriteString(fmt.Sprintf("%d. %s (@%s)\n", i+1, html.EscapeString(m.Name), html.EscapeString(m.ChannelName)))
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

// ── /create flow ─────────────────────────────────────────────────────

func (b *Bot) handleCreate(c tele.Context) error {
	log.Printf("[bot] /create from user %d (@%s)", c.Sender().ID, c.Sender().Username)
	ctx := context.Background()
	_, err := b.db.UpsertUser(ctx, c.Sender().ID, c.Sender().Username, c.Sender().FirstName)
	if err != nil {
		log.Printf("[bot] upsert user error: %v", err)
		return c.Send(msgErrorRetry)
	}

	b.mu.Lock()
	b.conversations[c.Sender().ID] = &conversationData{State: stateAwaitingType}
	b.mu.Unlock()

	keyboard := &tele.ReplyMarkup{InlineKeyboard: [][]tele.InlineButton{
		{
			{Text: msgCreateBtnHeartbeat, Data: "create_type:heartbeat"},
		},
		{
			{Text: msgCreateBtnPing, Data: "create_type:ping"},
		},
	}}

	return c.Send(msgCreateStep1, tele.ModeHTML, keyboard)
}

// ── Text handler (router) ────────────────────────────────────────────

func (b *Bot) handleText(c tele.Context) error {
	b.mu.RLock()
	conv, exists := b.conversations[c.Sender().ID]
	b.mu.RUnlock()

	if !exists || conv.State == stateIdle {
		return nil
	}

	switch conv.State {
	case stateAwaitingPingTarget:
		return b.onPingTarget(c, conv)
	case stateAwaitingAddress:
		return b.onAddress(c, conv)
	case stateAwaitingChannel:
		return b.onChannel(c, conv)
	}
	return nil
}

// ── Step 1: Monitor type (callback) ──────────────────────────────────

func (b *Bot) onCreateType(c tele.Context, monitorType string) error {
	b.mu.RLock()
	conv, exists := b.conversations[c.Sender().ID]
	b.mu.RUnlock()

	if !exists || conv.State != stateAwaitingType {
		return c.Respond(&tele.CallbackResponse{Text: msgStartOverRequired})
	}

	_ = c.Respond(&tele.CallbackResponse{})

	b.mu.Lock()
	conv.MonitorType = monitorType
	b.mu.Unlock()

	if monitorType == "ping" {
		b.mu.Lock()
		conv.State = stateAwaitingPingTarget
		b.mu.Unlock()

		return c.Send(msgPingTargetStep, htmlOpts)
	}

	// Heartbeat — go directly to address step.
	b.mu.Lock()
	conv.State = stateAwaitingAddress
	b.mu.Unlock()

	return c.Send(msgAddressStepHeartbeat, htmlOpts)
}

// ── Step 2 (ping only): Ping target ─────────────────────────────────

func (b *Bot) onPingTarget(c tele.Context, conv *conversationData) error {
	target := strings.TrimSpace(c.Text())
	if len(target) < 3 {
		return c.Send(msgPingTargetTooShort, htmlOpts)
	}

	// Validate: resolve the hostname to check it's reachable.
	ips, err := net.LookupHost(target)
	if err != nil {
		return c.Send(fmt.Sprintf("Не вдалося знайти хост <code>%s</code>. Перевірте адресу і спробуйте ще раз.", html.EscapeString(target)), htmlOpts)
	}

	// Check for private IPs.
	ip := net.ParseIP(ips[0])
	if ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()) {
		return c.Send(msgPingTargetPrivate, htmlOpts)
	}

	// Test ICMP ping to verify the host is reachable.
	_ = c.Send(fmt.Sprintf("🔍 Перевіряю доступність <code>%s</code>...", html.EscapeString(target)), htmlOpts)
	if !b.heartbeatSvc.PingHost(target) {
		return c.Send(fmt.Sprintf("❌ Хост <code>%s</code> не відповідає на ICMP ping.\nПереконайтесь, що роутер дозволяє ICMP і спробуйте ще раз.", html.EscapeString(target)), htmlOpts)
	}

	b.mu.Lock()
	conv.PingTarget = target
	conv.State = stateAwaitingAddress
	b.mu.Unlock()

	_ = c.Send(fmt.Sprintf("✅ Хост доступний: <code>%s</code> → <code>%s</code>", html.EscapeString(target), ips[0]), htmlOpts)

	return c.Send(msgAddressStepPing, htmlOpts)
}

// ── Step: Address ────────────────────────────────────────────────────

func (b *Bot) onAddress(c tele.Context, conv *conversationData) error {
	text := strings.TrimSpace(c.Text())
	if len(text) < 3 {
		return c.Send(msgAddressTooShort, htmlOpts)
	}

	// Check if user typed raw coordinates (lat, lng).
	if parts := strings.Split(text, ","); len(parts) == 2 {
		lat, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
		lng, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if err1 == nil && err2 == nil && lat >= -90 && lat <= 90 && lng >= -180 && lng <= 180 {
			// Looks like raw coordinates — use directly.
			b.mu.Lock()
			conv.Name = text
			conv.Address = text
			conv.Latitude = lat
			conv.Longitude = lng
			conv.State = stateAwaitingChannel
			b.mu.Unlock()
			return c.Send(b.channelStepMessage(conv), htmlOpts)
		}
	}

	// Geocode the address.
	_ = c.Send(msgSearchingAddress)

	result, err := geocode.Search(context.Background(), text)
	if err != nil {
		log.Printf("[bot] geocode error: %v", err)
		return c.Send(msgGeocodeError)
	}
	if result == nil {
		return c.Send(msgAddressNotFound, htmlOpts)
	}

	// Store geocoded data and proceed to channel step.
	b.mu.Lock()
	conv.Name = text
	conv.Address = result.DisplayName
	conv.Latitude = result.Latitude
	conv.Longitude = result.Longitude
	conv.State = stateAwaitingChannel
	b.mu.Unlock()

	_ = c.Send(fmt.Sprintf("Знайдено: <b>%s</b>", html.EscapeString(result.DisplayName)), htmlOpts)
	return c.Send(b.channelStepMessage(conv), htmlOpts)
}

// ── GPS location handler ─────────────────────────────────────────────

func (b *Bot) handleLocation(c tele.Context) error {
	b.mu.RLock()
	conv, exists := b.conversations[c.Sender().ID]
	b.mu.RUnlock()

	if !exists {
		return nil
	}

	if conv.State != stateAwaitingAddress {
		return nil
	}

	loc := c.Message().Location

	b.mu.Lock()
	if conv.Name == "" {
		conv.Name = fmt.Sprintf("%.4f, %.4f", loc.Lat, loc.Lng)
	}
	conv.Latitude = float64(loc.Lat)
	conv.Longitude = float64(loc.Lng)
	conv.State = stateAwaitingChannel
	b.mu.Unlock()

	return c.Send(b.channelStepMessage(conv), htmlOpts)
}

// ── Step 2: Channel ──────────────────────────────────────────────────

func (b *Bot) channelStepMessage(conv *conversationData) string {
	step := "3/3"
	if conv.MonitorType == "ping" {
		step = "4/4"
	}
	return fmt.Sprintf(`Геопозицію встановлено: <code>%.5f, %.5f</code>

<b>Крок %s:</b> Створіть Telegram-канал і додайте мене як адміністратора з правом "Публікація повідомлень".

Потім надішліть мені @username каналу (напр., @my_power_channel).`, conv.Latitude, conv.Longitude, step)
}

func (b *Bot) onChannel(c tele.Context, conv *conversationData) error {
	text := strings.TrimSpace(c.Text())

	if !strings.HasPrefix(text, "@") {
		text = "@" + text
	}

	chat, err := b.bot.ChatByUsername(text)
	if err != nil {
		return c.Send(fmt.Sprintf("Не вдалося знайти канал <b>%s</b>. Переконайтеся, що канал існує і має публічний username. Спробуйте ще раз.", html.EscapeString(text)), htmlOpts)
	}

	me := b.bot.Me
	member, err := b.bot.ChatMemberOf(chat, me)
	if err != nil {
		return c.Send(msgChannelCheckError)
	}

	if member.Role != tele.Administrator && member.Role != tele.Creator {
		return c.Send(msgChannelNotAdmin)
	}

	if !member.Rights.CanPostMessages {
		return c.Send(msgChannelNoPost)
	}

	ctx := context.Background()
	user, err := b.db.UpsertUser(ctx, c.Sender().ID, c.Sender().Username, c.Sender().FirstName)
	if err != nil {
		log.Printf("[bot] upsert user error: %v", err)
		return c.Send(msgErrorRetry)
	}

	monitorType := conv.MonitorType
	if monitorType == "" {
		monitorType = "heartbeat"
	}

	monitor, err := b.db.CreateMonitor(ctx, user.ID, conv.Name, conv.Address, conv.Latitude, conv.Longitude, chat.ID, chat.Username, monitorType, conv.PingTarget)
	if err != nil {
		log.Printf("[bot] create monitor error: %v", err)
		return c.Send(msgErrorRetry)
	}

	b.heartbeatSvc.RegisterMonitor(monitor)
	log.Printf("[bot] monitor created: id=%d type=%s name=%q user=%d (@%s)", monitor.ID, monitorType, monitor.Name, c.Sender().ID, c.Sender().Username)

	// Trigger initial weekly graph in the channel.
	if b.graphUpdater != nil && monitor.ChannelID != 0 {
		go func() {
			if err := b.graphUpdater.UpdateSingle(context.Background(), monitor.ID, monitor.ChannelID); err != nil {
				log.Printf("[bot] initial graph for monitor %d failed: %v", monitor.ID, err)
			}
		}()
	}

	b.mu.Lock()
	delete(b.conversations, c.Sender().ID)
	b.mu.Unlock()

	var msg string
	if monitorType == "ping" {
		msg = fmt.Sprintf(`<b>Монітор налаштовано!</b>

<b>Назва:</b> %s
<b>Тип:</b> Server Ping
<b>Ціль:</b> <code>%s</code>
<b>Координати:</b> %.5f, %.5f
<b>Канал:</b> @%s

Сервер пінгуватиме <code>%s</code> кожні 5 хвилин.

Коли пінги не проходять — я сповіщу канал, що світла немає. Коли відновляться — що світло повернулося.`,
			html.EscapeString(monitor.Name),
			html.EscapeString(monitor.PingTarget),
			conv.Latitude, conv.Longitude,
			html.EscapeString(chat.Username),
			html.EscapeString(monitor.PingTarget),
		)
	} else {
		pingURL := fmt.Sprintf("%s/api/ping/%s", b.baseURL, monitor.Token)
		msg = fmt.Sprintf(`<b>Монітор налаштовано!</b>

<b>Назва:</b> %s
<b>Тип:</b> ESP Heartbeat
<b>Координати:</b> %.5f, %.5f
<b>Канал:</b> @%s

<b>Посилання для пінгу:</b>
<code>%s</code>

Налаштуйте ваш пристрій надсилати GET-запит на це посилання кожні 5 хвилин.

Коли пінги зупиняться — я сповіщу канал, що світла немає. Коли відновляться — що світло повернулося.

💬 Інструкції з налаштування та допомога: @lights_monitor_chat`,
			html.EscapeString(monitor.Name),
			conv.Latitude, conv.Longitude,
			html.EscapeString(chat.Username),
			html.EscapeString(pingURL),
		)
	}

	return c.Send(msg, htmlOpts)
}

// ── Notifier ─────────────────────────────────────────────────────────

// TelegramNotifier implements heartbeat.Notifier using the Telegram bot.
type TelegramNotifier struct {
	bot *tele.Bot
}

func NewNotifier(b *tele.Bot) *TelegramNotifier {
	return &TelegramNotifier{bot: b}
}

// NotifyStatusChange sends a status message to the linked Telegram channel.
func (n *TelegramNotifier) NotifyStatusChange(channelID int64, name string, isOnline bool, duration time.Duration, when time.Time) {
	var msg string
	dur := database.FormatDuration(duration)
	kyiv, _ := time.LoadLocation("Europe/Kyiv")
	timeStr := when.In(kyiv).Format("15:04")

	if isOnline {
		msg = fmt.Sprintf(msgNotifyOnline, timeStr, dur)
	} else {
		msg = fmt.Sprintf(msgNotifyOffline, timeStr, dur)
	}

	chat := &tele.Chat{ID: channelID}
	_, err := n.bot.Send(chat, msg, htmlOpts)
	if err != nil {
		log.Printf("[bot] failed to send notification to channel %d: %v", channelID, err)
	}
}
