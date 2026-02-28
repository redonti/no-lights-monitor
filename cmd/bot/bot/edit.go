package bot

import (
	"context"
	"fmt"
	"html"
	"log"
	"strconv"
	"strings"

	"no-lights-monitor/internal/geocode"
	"no-lights-monitor/internal/models"

	tele "gopkg.in/telebot.v3"
)

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

	return c.Send(fmt.Sprintf(msgEditNameDone, html.EscapeString(name)), tele.ModeHTML, mainMenu)
}

func (b *Bot) onEditAddress(c tele.Context, conv *conversationData) error {
	text := strings.TrimSpace(c.Text())
	if len(text) < 3 {
		return c.Send(msgAddressTooShort, htmlOpts)
	}

	// Raw coordinates.
	if parts := strings.Split(text, ","); len(parts) == 2 {
		lat, err1 := parseCoord(parts[0])
		lng, err2 := parseCoord(parts[1])
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

	return c.Send(fmt.Sprintf(msgEditAddressDone, html.EscapeString(result.DisplayName)), tele.ModeHTML, mainMenu)
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

	return c.Send(fmt.Sprintf(msgEditAddressDone, html.EscapeString(text)), tele.ModeHTML, mainMenu)
}

// parseCoord parses a trimmed string as a float64 coordinate.
func parseCoord(s string) (float64, error) {
	return strconv.ParseFloat(strings.TrimSpace(s), 64)
}
