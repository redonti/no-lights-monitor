package inactivity

import (
	"context"
	"log"
	"time"

	"no-lights-monitor/internal/database"
	"no-lights-monitor/internal/mq"
)

// Checker auto-pauses monitors that have never been active since creation
// (last_status_change_at == created_at). Runs daily at 13:00 Kyiv time.
type Checker struct {
	db        *database.DB
	publisher *mq.Publisher
}

func NewChecker(db *database.DB, publisher *mq.Publisher) *Checker {
	return &Checker{db: db, publisher: publisher}
}

// Start runs the checker loop, firing daily at 13:00 Kyiv time.
func (c *Checker) Start(ctx context.Context) {
	kyiv, _ := time.LoadLocation("Europe/Kyiv")
	log.Println("[inactivity] checker started, will run daily at 13:00 Kyiv")

	for {
		delay := timeUntilNext(13, 0, kyiv)
		log.Printf("[inactivity] next check in %s", delay.Round(time.Second))
		select {
		case <-ctx.Done():
			log.Println("[inactivity] checker stopped")
			return
		case <-time.After(delay):
			c.run(ctx)
		}
	}
}

func (c *Checker) run(ctx context.Context) {
	monitors, err := c.db.GetNeverActiveMonitors(ctx)
	if err != nil {
		log.Printf("[inactivity] failed to query monitors: %v", err)
		return
	}
	log.Printf("[inactivity] found %d never-active monitors to pause", len(monitors))

	for _, m := range monitors {
		if err := c.db.SetMonitorActive(ctx, m.ID, false); err != nil {
			log.Printf("[inactivity] monitor %d: failed to pause: %v", m.ID, err)
			continue
		}

		ownerID, err := c.db.GetOwnerTelegramIDByMonitorID(ctx, m.ID)
		if err != nil {
			log.Printf("[inactivity] monitor %d: failed to get owner: %v", m.ID, err)
		}

		msg := mq.InactivePauseMsg{
			MonitorID:       m.ID,
			ChannelID:       m.ChannelID,
			OwnerTelegramID: ownerID,
			MonitorName:     m.Name,
		}
		if err := c.publisher.Publish(ctx, mq.RoutingInactivePause, msg); err != nil {
			log.Printf("[inactivity] monitor %d: failed to publish pause msg: %v", m.ID, err)
		}
		log.Printf("[inactivity] monitor %d (%s): paused due to no activity", m.ID, m.Name)
	}
}

// timeUntilNext returns the duration until the next occurrence of hour:minute in loc.
func timeUntilNext(hour, minute int, loc *time.Location) time.Duration {
	now := time.Now().In(loc)
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, loc)
	if !now.Before(next) {
		next = next.Add(24 * time.Hour)
	}
	return next.Sub(time.Now())
}
