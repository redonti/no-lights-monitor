package bot

import (
	"context"
	"errors"
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
	stateAwaitingManualAddress
	stateAwaitingChannel
	stateAwaitingEditName
	stateAwaitingEditAddress
	stateAwaitingEditManualAddress
)

type conversationData struct {
	State         conversationState
	MonitorType   string // "heartbeat" or "ping"
	PingTarget    string // IP/hostname for ping monitors
	Name          string
	Address       string
	Latitude      float64
	Longitude     float64
	EditMonitorID int64 // ID of monitor being edited
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

	if err := b.SetCommands([]tele.Command{
		{Text: "create", Description: "Налаштувати новий монітор"},
		{Text: "info", Description: "Детальна інформація та URL для пінгу"},
		{Text: "edit", Description: "Змінити назву або адресу монітора"},
		{Text: "test", Description: "Відправити тестове повідомлення"},
		{Text: "stop", Description: "Призупинити моніторинг"},
		{Text: "resume", Description: "Відновити моніторинг"},
		{Text: "delete", Description: "Видалити монітор"},
		{Text: "help", Description: "Довідка про команди"},
	}); err != nil {
		log.Printf("[bot] failed to set commands: %v", err)
	}

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
	b.bot.Handle("/info", b.handleInfo)
	b.bot.Handle("/stop", b.handleStop)
	b.bot.Handle("/resume", b.handleResume)
	b.bot.Handle("/test", b.handleTest)
	b.bot.Handle("/delete", b.handleDelete)
	b.bot.Handle("/edit", b.handleEdit)
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
		if targetMonitor.ChannelID != 0 {
			if _, err := b.bot.Send(&tele.Chat{ID: targetMonitor.ChannelID}, msgChannelPaused, htmlOpts); err != nil {
				log.Printf("[bot] failed to send pause notice to channel %d: %v", targetMonitor.ChannelID, err)
			}
		}
		_ = c.Respond(&tele.CallbackResponse{Text: msgStopOK})
		return c.Send(fmt.Sprintf(msgStopDone, msgStopOK, html.EscapeString(targetMonitor.Name)), htmlOpts)

	case "resume":
		// If there's a linked channel, verify the bot still has access before resuming.
		if targetMonitor.ChannelID != 0 {
			chat := &tele.Chat{ID: targetMonitor.ChannelID}
			me := b.bot.Me
			member, err := b.bot.ChatMemberOf(chat, me)
			if err != nil || (member.Role != tele.Administrator && member.Role != tele.Creator) || !member.Rights.CanPostMessages {
				_ = c.Respond(&tele.CallbackResponse{Text: msgResumeNoAccess})
				return c.Send(fmt.Sprintf(msgResumeNoAccessDetail, html.EscapeString(targetMonitor.ChannelName)), htmlOpts)
			}
		}
		if err := b.db.SetMonitorActive(ctx, monitorID, true); err != nil {
			log.Printf("[bot] set monitor active error: %v", err)
			return c.Respond(&tele.CallbackResponse{Text: msgResumeError})
		}
		b.heartbeatSvc.SetMonitorActive(targetMonitor.Token, true)
		if targetMonitor.ChannelID != 0 {
			if _, err := b.bot.Send(&tele.Chat{ID: targetMonitor.ChannelID}, msgChannelResumed, htmlOpts); err != nil {
				log.Printf("[bot] failed to send resume notice to channel %d: %v", targetMonitor.ChannelID, err)
			}
		}
		_ = c.Respond(&tele.CallbackResponse{Text: msgResumeOK})
		return c.Send(fmt.Sprintf(msgResumeDone, msgResumeOK, html.EscapeString(targetMonitor.Name)), htmlOpts)

	case "delete_confirm":
		if err := b.db.DeleteMonitor(ctx, monitorID); err != nil {
			log.Printf("[bot] delete monitor error: %v", err)
			return c.Respond(&tele.CallbackResponse{Text: msgDeleteError})
		}
		b.heartbeatSvc.RemoveMonitor(targetMonitor.Token)
		_ = c.Respond(&tele.CallbackResponse{Text: msgDeleteOK})
		return c.Send(fmt.Sprintf(msgDeleteDone, msgDeleteOK, html.EscapeString(targetMonitor.Name)), htmlOpts)

	case "info":
		_ = c.Respond(&tele.CallbackResponse{})

		var bld strings.Builder
		bld.WriteString(msgInfoDetailHeader)
		bld.WriteString(fmt.Sprintf(msgInfoDetailName, html.EscapeString(targetMonitor.Name)))
		bld.WriteString(fmt.Sprintf(msgInfoDetailAddress, html.EscapeString(targetMonitor.Address)))
		bld.WriteString(fmt.Sprintf(msgInfoDetailCoords, targetMonitor.Latitude, targetMonitor.Longitude))

		status := msgInfoStatusOffline
		if targetMonitor.IsOnline {
			status = msgInfoStatusOnline
		}
		if !targetMonitor.IsActive {
			status = msgStatusPaused
		}
		bld.WriteString(fmt.Sprintf(msgInfoDetailStatus, status))

		if targetMonitor.LastHeartbeatAt != nil {
			bld.WriteString(fmt.Sprintf(msgInfoDetailLastPing, targetMonitor.LastHeartbeatAt.Format("2006-01-02 15:04:05")))
		}

		if targetMonitor.ChannelID != 0 {
			bld.WriteString(fmt.Sprintf(msgInfoDetailChannel, html.EscapeString(targetMonitor.ChannelName)))
		} else {
			bld.WriteString("\n")
		}

		if targetMonitor.MonitorType == "ping" {
			bld.WriteString(fmt.Sprintf(msgInfoDetailTypePing, msgInfoTypePing))
			bld.WriteString(fmt.Sprintf(msgInfoDetailTarget, html.EscapeString(targetMonitor.PingTarget)))
			bld.WriteString(msgInfoPingHint)
		} else {
			bld.WriteString(fmt.Sprintf(msgInfoDetailTypeHB, msgInfoTypeHeartbeat))
			bld.WriteString(msgInfoDetailURLLabel)
			bld.WriteString(fmt.Sprintf(msgInfoDetailURL, b.baseURL, targetMonitor.Token))
			bld.WriteString(msgInfoHeartbeatHint)
		}

		mapBtn := tele.InlineButton{
			Text: msgMapBtnHide,
			Data: fmt.Sprintf("map_hide:%d", monitorID),
		}
		if !targetMonitor.IsPublic {
			mapBtn = tele.InlineButton{
				Text: msgMapBtnShow,
				Data: fmt.Sprintf("map_show:%d", monitorID),
			}
		}
		keyboard := &tele.ReplyMarkup{InlineKeyboard: [][]tele.InlineButton{{mapBtn}}}
		return c.Send(bld.String(), htmlOpts, keyboard)

	case "edit":
		_ = c.Respond(&tele.CallbackResponse{})
		rows := [][]tele.InlineButton{
			{{Text: msgEditBtnName, Data: fmt.Sprintf("edit_name:%d", monitorID)}},
			{{Text: msgEditBtnAddress, Data: fmt.Sprintf("edit_address:%d", monitorID)}},
		}
		if targetMonitor.ChannelID != 0 {
			rows = append(rows, []tele.InlineButton{
				{Text: msgEditBtnRefreshChannel, Data: fmt.Sprintf("edit_channel_refresh:%d", monitorID)},
			})
		}
		keyboard := &tele.ReplyMarkup{InlineKeyboard: rows}
		return c.Send(fmt.Sprintf(msgEditChoose, html.EscapeString(targetMonitor.Name)), htmlOpts, keyboard)

	case "edit_name":
		_ = c.Respond(&tele.CallbackResponse{})
		b.mu.Lock()
		b.conversations[c.Sender().ID] = &conversationData{
			State:         stateAwaitingEditName,
			EditMonitorID: monitorID,
		}
		b.mu.Unlock()
		return c.Send(fmt.Sprintf(msgEditNamePrompt, html.EscapeString(targetMonitor.Name)), htmlOpts)

	case "edit_address":
		_ = c.Respond(&tele.CallbackResponse{})
		b.mu.Lock()
		b.conversations[c.Sender().ID] = &conversationData{
			State:         stateAwaitingEditAddress,
			EditMonitorID: monitorID,
		}
		b.mu.Unlock()
		return c.Send(fmt.Sprintf(msgEditAddressPrompt, html.EscapeString(targetMonitor.Address)), htmlOpts)

	case "edit_channel_refresh":
		_ = c.Respond(&tele.CallbackResponse{})
		chat, err := b.bot.ChatByID(targetMonitor.ChannelID)
		if err != nil {
			log.Printf("[bot] failed to fetch channel info for monitor %d: %v", targetMonitor.ID, err)
			return c.Send(msgEditChannelRefreshError, htmlOpts)
		}
		newName := chat.Username
		if newName == targetMonitor.ChannelName {
			return c.Send(fmt.Sprintf(msgEditChannelRefreshNoChange, newName), htmlOpts)
		}
		if err := b.db.UpdateMonitorChannelName(ctx, targetMonitor.ID, newName); err != nil {
			log.Printf("[bot] failed to update channel name for monitor %d: %v", targetMonitor.ID, err)
			return c.Send(msgError, htmlOpts)
		}
		return c.Send(fmt.Sprintf(msgEditChannelRefreshDone, newName), htmlOpts)

	case "map_hide":
		if err := b.db.SetMonitorPublic(ctx, monitorID, false); err != nil {
			log.Printf("[bot] set monitor public error: %v", err)
			return c.Respond(&tele.CallbackResponse{Text: msgMapHideError})
		}
		_ = c.Respond(&tele.CallbackResponse{Text: msgMapHidden})
		return c.Send(msgMapHidden)

	case "map_show":
		if err := b.db.SetMonitorPublic(ctx, monitorID, true); err != nil {
			log.Printf("[bot] set monitor public error: %v", err)
			return c.Respond(&tele.CallbackResponse{Text: msgMapHideError})
		}
		_ = c.Respond(&tele.CallbackResponse{Text: msgMapShown})
		return c.Send(msgMapShown)

	case "test":
		if targetMonitor.ChannelID == 0 {
			return c.Respond(&tele.CallbackResponse{Text: msgTestNoChannel})
		}

		testMsg := fmt.Sprintf(msgTestNotification,
			html.EscapeString(targetMonitor.Name),
			html.EscapeString(targetMonitor.Address),
		)

		chat := &tele.Chat{ID: targetMonitor.ChannelID}
		if _, err := b.bot.Send(chat, testMsg, htmlOpts); err != nil {
			log.Printf("[bot] test notification error: %v", err)
			return c.Respond(&tele.CallbackResponse{Text: msgTestSendError})
		}

		_ = c.Respond(&tele.CallbackResponse{Text: msgTestOK})
		return c.Send(fmt.Sprintf(msgTestSentTo, msgTestOK, html.EscapeString(targetMonitor.ChannelName)), htmlOpts)

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
	case stateAwaitingManualAddress:
		return b.onManualAddress(c, conv)
	case stateAwaitingChannel:
		return b.onChannel(c, conv)
	case stateAwaitingEditName:
		return b.onEditName(c, conv)
	case stateAwaitingEditAddress:
		return b.onEditAddress(c, conv)
	case stateAwaitingEditManualAddress:
		return b.onEditManualAddress(c, conv)
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
		return c.Send(fmt.Sprintf(msgPingHostNotFound, html.EscapeString(target)), htmlOpts)
	}

	// Check for private IPs.
	ip := net.ParseIP(ips[0])
	if ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()) {
		return c.Send(msgPingTargetPrivate, htmlOpts)
	}

	// Test ICMP ping to verify the host is reachable.
	_ = c.Send(fmt.Sprintf(msgPingChecking, html.EscapeString(target)), htmlOpts)
	if !b.heartbeatSvc.PingHost(target) {
		return c.Send(fmt.Sprintf(msgPingHostUnreachable, html.EscapeString(target)), htmlOpts)
	}

	b.mu.Lock()
	conv.PingTarget = target
	conv.State = stateAwaitingAddress
	b.mu.Unlock()

	_ = c.Send(fmt.Sprintf(msgPingHostOK, html.EscapeString(target), ips[0]), htmlOpts)

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
			b.mu.Lock()
			conv.Latitude = lat
			conv.Longitude = lng
			conv.State = stateAwaitingManualAddress
			b.mu.Unlock()
			return c.Send(msgManualAddressStep, htmlOpts)
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

	_ = c.Send(fmt.Sprintf(msgAddressFound, html.EscapeString(result.DisplayName)), htmlOpts)
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

	loc := c.Message().Location

	if conv.State == stateAwaitingAddress {
		b.mu.Lock()
		conv.Latitude = float64(loc.Lat)
		conv.Longitude = float64(loc.Lng)
		conv.State = stateAwaitingManualAddress
		b.mu.Unlock()
		return c.Send(msgManualAddressStep, htmlOpts)
	}

	if conv.State == stateAwaitingEditAddress {
		b.mu.Lock()
		conv.Latitude = float64(loc.Lat)
		conv.Longitude = float64(loc.Lng)
		conv.State = stateAwaitingEditManualAddress
		b.mu.Unlock()
		return c.Send(msgManualAddressStep, htmlOpts)
	}

	return nil
}

// ── Edit handlers ────────────────────────────────────────────────────

func (b *Bot) onEditName(c tele.Context, conv *conversationData) error {
	name := strings.TrimSpace(c.Text())
	if len(name) < 2 {
		return c.Send(msgEditNameTooShort, htmlOpts)
	}

	ctx := context.Background()

	// Verify the monitor still belongs to this user.
	monitors, err := b.db.GetMonitorsByTelegramID(ctx, c.Sender().ID)
	if err != nil {
		log.Printf("[bot] get monitors error: %v", err)
		return c.Send(msgError)
	}
	var target *models.Monitor
	for _, m := range monitors {
		if m.ID == conv.EditMonitorID {
			target = m
			break
		}
	}
	if target == nil {
		b.mu.Lock()
		delete(b.conversations, c.Sender().ID)
		b.mu.Unlock()
		return c.Send(msgMonitorNotFound)
	}

	if err := b.db.UpdateMonitorName(ctx, conv.EditMonitorID, name); err != nil {
		log.Printf("[bot] update monitor name error: %v", err)
		return c.Send(msgErrorRetry)
	}

	b.mu.Lock()
	delete(b.conversations, c.Sender().ID)
	b.mu.Unlock()

	return c.Send(fmt.Sprintf(msgEditNameDone, html.EscapeString(name)), htmlOpts)
}

func (b *Bot) onEditAddress(c tele.Context, conv *conversationData) error {
	text := strings.TrimSpace(c.Text())
	if len(text) < 3 {
		return c.Send(msgAddressTooShort, htmlOpts)
	}

	// Raw coordinates.
	if parts := strings.Split(text, ","); len(parts) == 2 {
		lat, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
		lng, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if err1 == nil && err2 == nil && lat >= -90 && lat <= 90 && lng >= -180 && lng <= 180 {
			b.mu.Lock()
			conv.Latitude = lat
			conv.Longitude = lng
			conv.State = stateAwaitingEditManualAddress
			b.mu.Unlock()
			return c.Send(msgManualAddressStep, htmlOpts)
		}
	}

	_ = c.Send(msgSearchingAddress)

	result, err := geocode.Search(context.Background(), text)
	if err != nil {
		log.Printf("[bot] geocode error: %v", err)
		return c.Send(msgGeocodeError)
	}
	if result == nil {
		return c.Send(msgAddressNotFound, htmlOpts)
	}

	ctx := context.Background()
	if err := b.db.UpdateMonitorAddress(ctx, conv.EditMonitorID, result.DisplayName, result.Latitude, result.Longitude); err != nil {
		log.Printf("[bot] update monitor address error: %v", err)
		return c.Send(msgErrorRetry)
	}

	b.mu.Lock()
	delete(b.conversations, c.Sender().ID)
	b.mu.Unlock()

	return c.Send(fmt.Sprintf(msgEditAddressDone, html.EscapeString(result.DisplayName)), htmlOpts)
}

func (b *Bot) onEditManualAddress(c tele.Context, conv *conversationData) error {
	text := strings.TrimSpace(c.Text())
	if len(text) < 3 {
		return c.Send(msgManualAddressTooShort, htmlOpts)
	}

	ctx := context.Background()
	if err := b.db.UpdateMonitorAddress(ctx, conv.EditMonitorID, text, conv.Latitude, conv.Longitude); err != nil {
		log.Printf("[bot] update monitor address error: %v", err)
		return c.Send(msgErrorRetry)
	}

	b.mu.Lock()
	delete(b.conversations, c.Sender().ID)
	b.mu.Unlock()

	return c.Send(fmt.Sprintf(msgEditAddressDone, html.EscapeString(text)), htmlOpts)
}

// ── Step: Manual address (after raw coordinates / GPS) ───────────────

func (b *Bot) onManualAddress(c tele.Context, conv *conversationData) error {
	text := strings.TrimSpace(c.Text())
	if len(text) < 3 {
		return c.Send(msgManualAddressTooShort, htmlOpts)
	}

	b.mu.Lock()
	conv.Name = text
	conv.Address = text
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
	return fmt.Sprintf(msgChannelStep, conv.Latitude, conv.Longitude, step)
}

func (b *Bot) onChannel(c tele.Context, conv *conversationData) error {
	text := strings.TrimSpace(c.Text())

	if !strings.HasPrefix(text, "@") {
		text = "@" + text
	}

	chat, err := b.bot.ChatByUsername(text)
	if err != nil {
		return c.Send(fmt.Sprintf(msgChannelNotFound, html.EscapeString(text)), htmlOpts)
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
		msg = fmt.Sprintf(msgCreateDonePing,
			html.EscapeString(monitor.Name),
			html.EscapeString(monitor.PingTarget),
			conv.Latitude, conv.Longitude,
			html.EscapeString(chat.Username),
			html.EscapeString(monitor.PingTarget),
		)
	} else {
		pingURL := fmt.Sprintf("%s/api/ping/%s", b.baseURL, monitor.Token)
		msg = fmt.Sprintf(msgCreateDoneHeartbeat,
			html.EscapeString(monitor.Name),
			conv.Latitude, conv.Longitude,
			html.EscapeString(chat.Username),
			html.EscapeString(pingURL),
		)
	}

	return c.Send(msg, htmlOpts)
}

// ── Channel error helpers ─────────────────────────────────────────────

// isChannelError reports whether a Telegram API error means the bot lost access to a channel.
func isChannelError(err error) bool {
	return errors.Is(err, tele.ErrChatNotFound) ||
		errors.Is(err, tele.ErrKickedFromGroup) ||
		errors.Is(err, tele.ErrKickedFromSuperGroup) ||
		errors.Is(err, tele.ErrKickedFromChannel) ||
		errors.Is(err, tele.ErrNotChannelMember) ||
		errors.Is(err, tele.ErrNoRightsToSend) ||
		errors.Is(err, tele.ErrNoRightsToSendPhoto)
}

// SendToUser sends an HTML message directly to a Telegram user by their Telegram ID.
func SendToUser(b *tele.Bot, userTelegramID int64, msg string) {
	chat := &tele.Chat{ID: userTelegramID}
	if _, err := b.Send(chat, msg, htmlOpts); err != nil {
		log.Printf("[bot] failed to send DM to user %d: %v", userTelegramID, err)
	}
}

// NotifyChannelError checks whether err is a channel access error and, if so,
// pauses the monitor in the DB and notifies the owner.
// Returns true if the error was a channel error and was handled.
func NotifyChannelError(ctx context.Context, b *tele.Bot, db *database.DB, err error, userTelegramID int64, monitor *models.Monitor) bool {
	if !isChannelError(err) {
		return false
	}
	log.Printf("[bot] channel access lost for monitor %d (%s), pausing", monitor.ID, monitor.Name)
	// Attempt to notify the channel — may succeed for partial-access errors (e.g. no photo rights).
	if monitor.ChannelID != 0 {
		chat := &tele.Chat{ID: monitor.ChannelID}
		if _, sendErr := b.Send(chat, msgChannelPausedBySystem, htmlOpts); sendErr != nil {
			log.Printf("[bot] could not send system-pause notice to channel %d: %v", monitor.ChannelID, sendErr)
		}
	}
	if dbErr := db.SetMonitorActive(ctx, monitor.ID, false); dbErr != nil {
		log.Printf("[bot] failed to pause monitor %d: %v", monitor.ID, dbErr)
	}
	msg := fmt.Sprintf(msgChannelError, html.EscapeString(monitor.Name))
	SendToUser(b, userTelegramID, msg)
	return true
}

// ── Notifier ─────────────────────────────────────────────────────────

// TelegramNotifier implements heartbeat.Notifier using the Telegram bot.
type TelegramNotifier struct {
	bot *tele.Bot
	db  *database.DB
}

func NewNotifier(b *tele.Bot, db *database.DB) *TelegramNotifier {
	return &TelegramNotifier{bot: b, db: db}
}

// NotifyStatusChange sends a status message to the linked Telegram channel.
// On channel access errors the monitor is paused and the owner is notified via DM.
func (n *TelegramNotifier) NotifyStatusChange(monitorID, channelID int64, name string, isOnline bool, duration time.Duration, when time.Time) {
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
		ctx := context.Background()
		ownerID, dbErr := n.db.GetOwnerTelegramIDByMonitorID(ctx, monitorID)
		if dbErr != nil {
			log.Printf("[bot] failed to get owner for monitor %d: %v", monitorID, dbErr)
			return
		}
		monitor := &models.Monitor{ID: monitorID, Name: name}
		if !NotifyChannelError(ctx, n.bot, n.db, err, ownerID, monitor) {
			log.Printf("[bot] failed to send notification to channel %d: %v", channelID, err)
		}
	}
}
