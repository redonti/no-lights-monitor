package bot

import (
	"context"
	"fmt"
	"html"
	"log"
	"net"
	"strings"

	"no-lights-monitor/internal/geocode"

	tele "gopkg.in/telebot.v3"
)

// ── /create command ──────────────────────────────────────────────────

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

	return c.Send(msgCreateStep1, tele.ModeHTML, createTypeMenu)
}

// ── Back to menu ──────────────────────────────────────────────────────

func (b *Bot) handleBackButton(c tele.Context, conv *conversationData) error {
	b.mu.Lock()
	delete(b.conversations, c.Sender().ID)
	b.mu.Unlock()
	return c.Send(msgCancelled, mainMenu)
}

// ── Step 1: Monitor type (text) ───────────────────────────────────────

func (b *Bot) onCreateType(c tele.Context, conv *conversationData) error {
	var monitorType string
	switch c.Text() {
	case msgCreateBtnHeartbeat:
		monitorType = "heartbeat"
	case msgCreateBtnPing:
		monitorType = "ping"
	default:
		return c.Send(msgCreateStep1, tele.ModeHTML, createTypeMenu)
	}

	b.mu.Lock()
	conv.MonitorType = monitorType
	b.mu.Unlock()

	if monitorType == "ping" {
		b.mu.Lock()
		conv.State = stateAwaitingPingTarget
		b.mu.Unlock()

		return c.Send(msgPingTargetStep, tele.ModeHTML, backMenu)
	}

	// Heartbeat — go directly to address step.
	b.mu.Lock()
	conv.State = stateAwaitingAddress
	b.mu.Unlock()

	return c.Send(msgAddressStepHeartbeat, tele.ModeHTML, backMenu)
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

	return c.Send(msgAddressStepPing, tele.ModeHTML, backMenu)
}

// ── Step: Address ────────────────────────────────────────────────────

func (b *Bot) onAddress(c tele.Context, conv *conversationData) error {
	text := strings.TrimSpace(c.Text())
	if len(text) < 3 {
		return c.Send(msgAddressTooShort, htmlOpts)
	}

	// Check if user typed raw coordinates (lat, lng).
	if parts := strings.Split(text, ","); len(parts) == 2 {
		lat, err1 := parseCoord(parts[0])
		lng, err2 := parseCoord(parts[1])
		if err1 == nil && err2 == nil && lat >= -90 && lat <= 90 && lng >= -180 && lng <= 180 {
			b.mu.Lock()
			conv.Latitude = lat
			conv.Longitude = lng
			conv.State = stateAwaitingManualAddress
			b.mu.Unlock()
			return c.Send(msgManualAddressStep, tele.ModeHTML, backMenu)
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
	return c.Send(b.channelStepMessage(conv), tele.ModeHTML, backMenu)
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

	return c.Send(b.channelStepMessage(conv), tele.ModeHTML, backMenu)
}

// ── Step: Channel ────────────────────────────────────────────────────

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

	return c.Send(msg, tele.ModeHTML, mainMenu)
}
