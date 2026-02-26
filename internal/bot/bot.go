package bot

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"no-lights-monitor/internal/database"
	"no-lights-monitor/internal/heartbeat"
	"no-lights-monitor/internal/outage"

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
	outageClient  *outage.Client
	conversations map[int64]*conversationData
	mu            sync.RWMutex
}

var htmlOpts = &tele.SendOptions{ParseMode: tele.ModeHTML}

var removeMenu = &tele.ReplyMarkup{RemoveKeyboard: true}

var createTypeMenu = &tele.ReplyMarkup{
	ResizeKeyboard:  true,
	OneTimeKeyboard: true,
	ReplyKeyboard: [][]tele.ReplyButton{
		{{Text: msgCreateBtnHeartbeat}},
		{{Text: msgCreateBtnPing}},
	},
}

var backMenu = &tele.ReplyMarkup{
	ResizeKeyboard: true,
	ReplyKeyboard:  [][]tele.ReplyButton{{{Text: msgBtnBack}}},
}

var mainMenu = &tele.ReplyMarkup{
	ResizeKeyboard: true,
	ReplyKeyboard: [][]tele.ReplyButton{
		{{Text: menuBtnCreate}, {Text: menuBtnInfo}},
		{{Text: menuBtnEdit}, {Text: menuBtnTest}},
		{{Text: menuBtnStop}, {Text: menuBtnResume}, {Text: menuBtnDelete}},
	},
}

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
		{Text: "edit", Description: "Змінити налаштування монітора"},
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

// SetOutageClient wires the outage service client.
func (b *Bot) SetOutageClient(c *outage.Client) {
	b.outageClient = c
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

// ── Text handler (router) ────────────────────────────────────────────

func (b *Bot) handleText(c tele.Context) error {
	b.mu.RLock()
	conv, exists := b.conversations[c.Sender().ID]
	b.mu.RUnlock()

	if !exists || conv.State == stateIdle {
		return b.handleMenuButton(c)
	}

	if c.Text() == msgBtnBack {
		return b.handleBackButton(c, conv)
	}

	switch conv.State {
	case stateAwaitingType:
		return b.onCreateType(c, conv)
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

// ── Menu button handler ──────────────────────────────────────────────

func (b *Bot) handleMenuButton(c tele.Context) error {
	switch c.Text() {
	case menuBtnCreate:
		return b.handleCreate(c)
	case menuBtnInfo:
		return b.handleInfo(c)
	case menuBtnEdit:
		return b.handleEdit(c)
	case menuBtnTest:
		return b.handleTest(c)
	case menuBtnStop:
		return b.handleStop(c)
	case menuBtnResume:
		return b.handleResume(c)
	case menuBtnDelete:
		return b.handleDelete(c)
	}
	return nil
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
		return c.Send(msgManualAddressStep, tele.ModeHTML, backMenu)
	}

	if conv.State == stateAwaitingEditAddress {
		b.mu.Lock()
		conv.Latitude = float64(loc.Lat)
		conv.Longitude = float64(loc.Lng)
		conv.State = stateAwaitingEditManualAddress
		b.mu.Unlock()
		return c.Send(msgManualAddressStep, tele.ModeHTML, backMenu)
	}

	return nil
}
