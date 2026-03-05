package channeldesc

import (
	"context"
	"log"
	"strings"
	"time"

	"no-lights-monitor/internal/database"

	tele "gopkg.in/telebot.v3"
)

// Checker runs daily and ensures every active monitor's Telegram channel
// description contains the service URL. If missing, it appends it.
// Runs daily at 14:00 Kyiv time.
type Checker struct {
	bot     *tele.Bot
	db      *database.DB
	baseURL string
}

func NewChecker(bot *tele.Bot, db *database.DB, baseURL string) *Checker {
	return &Checker{bot: bot, db: db, baseURL: baseURL}
}

// Start runs the checker loop, firing daily at 14:00 Kyiv time.
func (c *Checker) Start(ctx context.Context) {
	kyiv, _ := time.LoadLocation("Europe/Kyiv")
	log.Println("[channeldesc] checker started, running initial check")
	c.run(ctx)

	for {
		delay := timeUntilNext(14, 0, kyiv)
		log.Printf("[channeldesc] next check in %s", delay.Round(time.Second))
		select {
		case <-ctx.Done():
			log.Println("[channeldesc] checker stopped")
			return
		case <-time.After(delay):
			c.run(ctx)
		}
	}
}

func (c *Checker) run(ctx context.Context) {
	monitors, err := c.db.GetMonitorsWithChannels(ctx)
	if err != nil {
		log.Printf("[channeldesc] failed to query monitors: %v", err)
		return
	}
	log.Printf("[channeldesc] checking %d monitors with channels", len(monitors))

	for _, m := range monitors {
		chat, err := c.bot.ChatByID(m.ChannelID)
		if err != nil {
			log.Printf("[channeldesc] monitor %d: failed to get channel %d info: %v", m.ID, m.ChannelID, err)
			continue
		}

		if strings.Contains(chat.Description, c.baseURL) {
			continue
		}

		newDesc := chat.Description
		if newDesc != "" {
			newDesc += "\n"
		}
		newDesc += c.baseURL

		if err := c.bot.SetGroupDescription(chat, newDesc); err != nil {
			log.Printf("[channeldesc] monitor %d: failed to update channel %d description: %v", m.ID, m.ChannelID, err)
			continue
		}
		log.Printf("[channeldesc] monitor %d: appended base URL to channel %d description", m.ID, m.ChannelID)
	}
}

func timeUntilNext(hour, minute int, loc *time.Location) time.Duration {
	now := time.Now().In(loc)
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, loc)
	if !now.Before(next) {
		next = next.Add(24 * time.Hour)
	}
	return next.Sub(time.Now())
}
