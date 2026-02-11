package bot

import (
	"context"
	"fmt"
	"html"
	"log"
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
	stateAwaitingAddress
	stateAwaitingChannel
)

type conversationData struct {
	State     conversationState
	Name      string
	Address   string
	Latitude  float64
	Longitude float64
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
	msg := `<b>Вітаю в No-Lights Monitor!</b>

Я допоможу моніторити стан електроенергії у вашому домі та сповіщати Telegram-канал, коли світло зникає або повертається.

/create - Налаштувати новий монітор
/status - Перевірити стан моніторів
/info - Детальна інформація та URL для пінгу
/test - Відправити тестове повідомлення
/stop - Призупинити моніторинг
/resume - Відновити моніторинг
/delete - Видалити монітор
/help - Детальніше`

	return c.Send(msg, htmlOpts)
}

func (b *Bot) handleHelp(c tele.Context) error {
	msg := `<b>Як це працює:</b>

1. Використайте /create для реєстрації нового монітора
2. Вкажіть адресу — я автоматично знайду координати
3. Створіть Telegram-канал і додайте мене як адміністратора
4. Я дам вам унікальне посилання для пінгу
5. Ваш пристрій пінгує це посилання кожні 5 хвилин
6. Якщо пінги зупиняються — я сповіщаю канал, що світла немає
7. Коли пінги відновлюються — сповіщаю, що світло є

<b>Команди:</b>
/status — переглянути всі монітори
/info — детальна інформація та URL для пінгу
/test — відправити тестове повідомлення в канал
/stop — призупинити моніторинг (не буде сповіщень)
/resume — відновити призупинений монітор
/delete — видалити монітор назавжди
/cancel — скасувати поточну операцію`

	return c.Send(msg, htmlOpts)
}

func (b *Bot) handleCancel(c tele.Context) error {
	b.mu.Lock()
	delete(b.conversations, c.Sender().ID)
	b.mu.Unlock()
	return c.Send("Операцію скасовано.")
}

func (b *Bot) handleStatus(c tele.Context) error {
	ctx := context.Background()
	monitors, err := b.db.GetMonitorsByTelegramID(ctx, c.Sender().ID)
	if err != nil {
		log.Printf("[bot] get monitors by telegram_id error: %v", err)
		return c.Send("Щось пішло не так. Спробуйте пізніше.")
	}

	if len(monitors) == 0 {
		return c.Send("У вас ще немає моніторів.\n\nСтворіть перший через /create")
	}

	now := time.Now()
	var bld strings.Builder
	bld.WriteString("<b>Ваші монітори</b>\n\n")

	for i, m := range monitors {
		dur := now.Sub(m.LastStatusChangeAt)
		durStr := database.FormatDuration(dur)
		status := "🔴 Світла немає"
		if m.IsOnline {
			status = "⚡ Світло є"
		}
		if !m.IsActive {
			status = "⏸ Призупинено"
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

	bld.WriteString("/create — додати новий монітор")

	return c.Send(bld.String(), htmlOpts)
}

func (b *Bot) handleStop(c tele.Context) error {
	ctx := context.Background()
	monitors, err := b.db.GetMonitorsByTelegramID(ctx, c.Sender().ID)
	if err != nil {
		log.Printf("[bot] get monitors error: %v", err)
		return c.Send("Щось пішло не так. Спробуйте пізніше.")
	}

	// Filter only active monitors.
	var active []*models.Monitor
	for _, m := range monitors {
		if m.IsActive {
			active = append(active, m)
		}
	}

	if len(active) == 0 {
		return c.Send("У вас немає активних моніторів для зупинки.\n\nВикористайте /resume, щоб відновити призупинені монітори.")
	}

	var bld strings.Builder
	bld.WriteString("<b>Призупинити моніторинг</b>\n\n")
	bld.WriteString("Оберіть монітор для зупинки:\n\n")

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
	return c.Send(bld.String(), keyboard, htmlOpts)
}

func (b *Bot) handleResume(c tele.Context) error {
	ctx := context.Background()
	monitors, err := b.db.GetMonitorsByTelegramID(ctx, c.Sender().ID)
	if err != nil {
		log.Printf("[bot] get monitors error: %v", err)
		return c.Send("Щось пішло не так. Спробуйте пізніше.")
	}

	// Filter only inactive monitors.
	var inactive []*models.Monitor
	for _, m := range monitors {
		if !m.IsActive {
			inactive = append(inactive, m)
		}
	}

	if len(inactive) == 0 {
		return c.Send("У вас немає призупинених моніторів.\n\nВикористайте /stop, щоб призупинити монітор.")
	}

	var bld strings.Builder
	bld.WriteString("<b>Відновити моніторинг</b>\n\n")
	bld.WriteString("Оберіть монітор для відновлення:\n\n")

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
	return c.Send(bld.String(), keyboard, htmlOpts)
}

func (b *Bot) handleCallback(c tele.Context) error {
	data := c.Callback().Data
	parts := strings.Split(data, ":")
	if len(parts) != 2 {
		return c.Respond(&tele.CallbackResponse{Text: "Невірний формат"})
	}

	action := parts[0]
	monitorID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return c.Respond(&tele.CallbackResponse{Text: "Невірний ID монітора"})
	}

	ctx := context.Background()

	// Get all monitors and find the right one
	monitors, err := b.db.GetMonitorsByTelegramID(ctx, c.Sender().ID)
	if err != nil {
		log.Printf("[bot] get monitors error: %v", err)
		return c.Respond(&tele.CallbackResponse{Text: "Помилка отримання даних"})
	}

	var targetMonitor *models.Monitor
	for _, m := range monitors {
		if m.ID == monitorID {
			targetMonitor = m
			break
		}
	}

	if targetMonitor == nil {
		return c.Respond(&tele.CallbackResponse{Text: "Монітор не знайдено"})
	}

	switch action {
	case "stop":
		if err := b.db.SetMonitorActive(ctx, monitorID, false); err != nil {
			log.Printf("[bot] set monitor inactive error: %v", err)
			return c.Respond(&tele.CallbackResponse{Text: "Помилка зупинки моніторингу"})
		}
		b.heartbeatSvc.SetMonitorActive(targetMonitor.Token, false)
		_ = c.Respond(&tele.CallbackResponse{Text: "✅ Моніторинг призупинено"})
		return c.Send(fmt.Sprintf("✅ Моніторинг <b>%s</b> призупинено.\n\nВідновити можна через /resume", html.EscapeString(targetMonitor.Name)), htmlOpts)

	case "resume":
		if err := b.db.SetMonitorActive(ctx, monitorID, true); err != nil {
			log.Printf("[bot] set monitor active error: %v", err)
			return c.Respond(&tele.CallbackResponse{Text: "Помилка відновлення моніторингу"})
		}
		b.heartbeatSvc.SetMonitorActive(targetMonitor.Token, true)
		_ = c.Respond(&tele.CallbackResponse{Text: "✅ Моніторинг відновлено"})
		return c.Send(fmt.Sprintf("✅ Моніторинг <b>%s</b> відновлено.\n\nПризупинити можна через /stop", html.EscapeString(targetMonitor.Name)), htmlOpts)

	case "delete_confirm":
		// Delete the monitor from database
		if err := b.db.DeleteMonitor(ctx, monitorID); err != nil {
			log.Printf("[bot] delete monitor error: %v", err)
			return c.Respond(&tele.CallbackResponse{Text: "Помилка видалення монітора"})
		}
		// Remove from heartbeat service memory
		b.heartbeatSvc.RemoveMonitor(targetMonitor.Token)
		_ = c.Respond(&tele.CallbackResponse{Text: "✅ Монітор видалено"})
		return c.Send(fmt.Sprintf("✅ Монітор <b>%s</b> успішно видалено.", html.EscapeString(targetMonitor.Name)), htmlOpts)

	case "info":
		// Show detailed information about the monitor
		_ = c.Respond(&tele.CallbackResponse{})

		var bld strings.Builder
		bld.WriteString(fmt.Sprintf("<b>📊 Інформація про монітор</b>\n\n"))
		bld.WriteString(fmt.Sprintf("🏷 <b>Назва:</b> %s\n", html.EscapeString(targetMonitor.Name)))
		bld.WriteString(fmt.Sprintf("📍 <b>Адреса:</b> %s\n", html.EscapeString(targetMonitor.Address)))
		bld.WriteString(fmt.Sprintf("🌐 <b>Координати:</b> %.6f, %.6f\n\n", targetMonitor.Latitude, targetMonitor.Longitude))

		status := "🔴 Офлайн"
		if targetMonitor.IsOnline {
			status = "⚡ Онлайн"
		}
		if !targetMonitor.IsActive {
			status = "⏸ Призупинено"
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

		bld.WriteString(fmt.Sprintf("<b>🔗 URL для пінгу:</b>\n"))
		bld.WriteString(fmt.Sprintf("<code>%s/api/ping/%s</code>\n\n", b.baseURL, targetMonitor.Token))
		bld.WriteString("<i>Налаштуйте ваш пристрій відправляти GET-запити на цей URL кожні 5 хвилин.</i>")

		return c.Send(bld.String(), htmlOpts)

	case "test":
		// Send test notification to channel
		if targetMonitor.ChannelID == 0 {
			return c.Respond(&tele.CallbackResponse{Text: "У цього монітора немає каналу"})
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
			return c.Respond(&tele.CallbackResponse{Text: "Помилка відправки тестового повідомлення"})
		}

		_ = c.Respond(&tele.CallbackResponse{Text: "✅ Тест відправлено"})
		return c.Send(fmt.Sprintf("✅ Тестове повідомлення відправлено в канал <b>@%s</b>", html.EscapeString(targetMonitor.ChannelName)), htmlOpts)

	default:
		return c.Respond(&tele.CallbackResponse{Text: "Невідома дія"})
	}
}

func (b *Bot) handleInfo(c tele.Context) error {
	ctx := context.Background()
	monitors, err := b.db.GetMonitorsByTelegramID(ctx, c.Sender().ID)
	if err != nil {
		log.Printf("[bot] get monitors error: %v", err)
		return c.Send("Щось пішло не так. Спробуйте пізніше.")
	}

	if len(monitors) == 0 {
		return c.Send("У вас ще немає моніторів.\n\nСтворіть перший через /create")
	}

	var bld strings.Builder
	bld.WriteString("<b>Детальна інформація про монітори</b>\n\n")

	rows := make([][]tele.InlineButton, 0, len(monitors))
	for i, m := range monitors {
		status := "🔴 Офлайн"
		if m.IsOnline {
			status = "⚡ Онлайн"
		}
		if !m.IsActive {
			status = "⏸ Призупинено"
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
	return c.Send(bld.String(), keyboard, htmlOpts)
}

func (b *Bot) handleTest(c tele.Context) error {
	ctx := context.Background()
	monitors, err := b.db.GetMonitorsByTelegramID(ctx, c.Sender().ID)
	if err != nil {
		log.Printf("[bot] get monitors error: %v", err)
		return c.Send("Щось пішло не так. Спробуйте пізніше.")
	}

	// Filter monitors with channels
	var withChannels []*models.Monitor
	for _, m := range monitors {
		if m.ChannelID != 0 {
			withChannels = append(withChannels, m)
		}
	}

	if len(withChannels) == 0 {
		return c.Send("У вас немає моніторів з налаштованими каналами.\n\nСпочатку створіть монітор через /create та вкажіть канал.")
	}

	var bld strings.Builder
	bld.WriteString("<b>Надіслати тестове повідомлення</b>\n\n")
	bld.WriteString("Оберіть монітор для відправки тесту:\n\n")

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
	return c.Send(bld.String(), keyboard, htmlOpts)
}

func (b *Bot) handleDelete(c tele.Context) error {
	ctx := context.Background()
	monitors, err := b.db.GetMonitorsByTelegramID(ctx, c.Sender().ID)
	if err != nil {
		log.Printf("[bot] get monitors error: %v", err)
		return c.Send("Щось пішло не так. Спробуйте пізніше.")
	}

	if len(monitors) == 0 {
		return c.Send("У вас немає моніторів для видалення.")
	}

	var bld strings.Builder
	bld.WriteString("<b>⚠️ Видалення монітора</b>\n\n")
	bld.WriteString("Оберіть монітор для видалення:\n\n")
	bld.WriteString("<i>Увага: ця дія незворотна! Всі дані про історію статусу будуть втрачені.</i>\n\n")

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
	return c.Send(bld.String(), keyboard, htmlOpts)
}

// ── /create flow ─────────────────────────────────────────────────────

func (b *Bot) handleCreate(c tele.Context) error {
	ctx := context.Background()
	_, err := b.db.UpsertUser(ctx, c.Sender().ID, c.Sender().Username, c.Sender().FirstName)
	if err != nil {
		log.Printf("[bot] upsert user error: %v", err)
		return c.Send("Щось пішло не так. Спробуйте ще раз.")
	}

	b.mu.Lock()
	b.conversations[c.Sender().ID] = &conversationData{State: stateAwaitingAddress}
	b.mu.Unlock()

	msg := `Налаштуємо новий монітор!

<b>Крок 1/2:</b> Введіть адресу вашої локації.
Наприклад: <code>Київ, Хрещатик 1</code>

Або надішліть геопозицію через 📎 → Геопозиція.`

	return c.Send(msg, htmlOpts)
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
	case stateAwaitingAddress:
		return b.onAddress(c, conv)
	case stateAwaitingChannel:
		return b.onChannel(c, conv)
	}
	return nil
}

// ── Step 1: Address ──────────────────────────────────────────────────

func (b *Bot) onAddress(c tele.Context, conv *conversationData) error {
	text := strings.TrimSpace(c.Text())
	if len(text) < 3 {
		return c.Send("Занадто коротко. Введіть адресу, наприклад: <code>Київ, Хрещатик 1</code>", htmlOpts)
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
			return c.Send(b.channelStepMessage(lat, lng), htmlOpts)
		}
	}

	// Geocode the address.
	_ = c.Send("🔍 Шукаю адресу...")

	result, err := geocode.Search(context.Background(), text)
	if err != nil {
		log.Printf("[bot] geocode error: %v", err)
		return c.Send("Не вдалося знайти адресу. Спробуйте ввести інакше або надішліть геопозицію через 📎.")
	}
	if result == nil {
		return c.Send("Адресу не знайдено. Спробуйте ввести точнішу адресу, наприклад: <code>Київ, вул. Хрещатик, 1</code>", htmlOpts)
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
	return c.Send(b.channelStepMessage(result.Latitude, result.Longitude), htmlOpts)
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

	return c.Send(b.channelStepMessage(float64(loc.Lat), float64(loc.Lng)), htmlOpts)
}

// ── Step 2: Channel ──────────────────────────────────────────────────

func (b *Bot) channelStepMessage(lat, lng float64) string {
	return fmt.Sprintf(`Геопозицію встановлено: <code>%.5f, %.5f</code>

<b>Крок 2/2:</b> Створіть Telegram-канал і додайте мене як адміністратора з правом "Публікація повідомлень".

Потім надішліть мені @username каналу (напр., @my_power_channel).`, lat, lng)
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
		return c.Send("Не вдалося перевірити мої права в цьому каналі. Переконайтеся, що я доданий як адміністратор.")
	}

	if member.Role != tele.Administrator && member.Role != tele.Creator {
		return c.Send("Я не адміністратор цього каналу. Додайте мене як адміна з правом \"Публікація повідомлень\" і спробуйте ще раз.")
	}

	if !member.Rights.CanPostMessages {
		return c.Send("У мене немає права \"Публікація повідомлень\" в цьому каналі. Оновіть мої права адміна і спробуйте ще раз.")
	}

	ctx := context.Background()
	user, err := b.db.UpsertUser(ctx, c.Sender().ID, c.Sender().Username, c.Sender().FirstName)
	if err != nil {
		log.Printf("[bot] upsert user error: %v", err)
		return c.Send("Щось пішло не так. Спробуйте ще раз.")
	}

	monitor, err := b.db.CreateMonitor(ctx, user.ID, conv.Name, conv.Address, conv.Latitude, conv.Longitude, chat.ID, chat.Username)
	if err != nil {
		log.Printf("[bot] create monitor error: %v", err)
		return c.Send("Не вдалося створити монітор. Спробуйте ще раз.")
	}

	b.heartbeatSvc.RegisterMonitor(monitor)

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

	pingURL := fmt.Sprintf("%s/api/ping/%s", b.baseURL, monitor.Token)

	msg := fmt.Sprintf(`<b>Монітор налаштовано!</b>

<b>Назва:</b> %s
<b>Координати:</b> %.5f, %.5f
<b>Канал:</b> @%s

<b>Посилання для пінгу:</b>
<code>%s</code>

Налаштуйте ваш пристрій надсилати GET-запит на це посилання кожні 5 хвилин.

Коли пінги зупиняться — я сповіщу канал, що світла немає. Коли відновляться — що світло повернулося.`,
		html.EscapeString(monitor.Name),
		conv.Latitude, conv.Longitude,
		html.EscapeString(chat.Username),
		html.EscapeString(pingURL),
	)

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
func (n *TelegramNotifier) NotifyStatusChange(channelID int64, name string, isOnline bool, duration time.Duration) {
	var msg string
	dur := database.FormatDuration(duration)
	escapedName := html.EscapeString(name)

	if isOnline {
		msg = fmt.Sprintf("⚡ <b>Світло є</b>\n%s\n<i>(не було %s)</i>", escapedName, dur)
	} else {
		msg = fmt.Sprintf("🔴 <b>Світла немає</b>\n%s\n<i>(було %s)</i>", escapedName, dur)
	}

	chat := &tele.Chat{ID: channelID}
	_, err := n.bot.Send(chat, msg, htmlOpts)
	if err != nil {
		log.Printf("[bot] failed to send notification to channel %d: %v", channelID, err)
	}
}
