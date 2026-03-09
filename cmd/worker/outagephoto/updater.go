package outagephoto

import (
	"context"
	"fmt"
	"log"
	"time"

	"no-lights-monitor/internal/database"
	"no-lights-monitor/internal/models"
	"no-lights-monitor/internal/mq"
	"no-lights-monitor/internal/outage"
)

// Updater is a background service that fetches outage schedule images
// and publishes them to RabbitMQ for the bot service to post to Telegram.
type Updater struct {
	db     *database.DB
	pub    *mq.Publisher
	outage *outage.Client
}

// NewUpdater creates a new outage photo updater.
func NewUpdater(db *database.DB, pub *mq.Publisher, outageClient *outage.Client) *Updater {
	return &Updater{
		db:     db,
		pub:    pub,
		outage: outageClient,
	}
}

// Start runs the periodic update loop. Fires once after a delay, then every hour.
func (u *Updater) Start(ctx context.Context) {
	log.Println("[outage-photo] updater started, waiting 60s")
	select {
	case <-ctx.Done():
		return
	case <-time.After(60 * time.Second):
	}
	log.Println("[outage-photo] running initial pass")
	u.runAll(ctx)

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("[outage-photo] updater stopped")
			return
		case <-ticker.C:
			u.runAll(ctx)
		}
	}
}

func (u *Updater) runAll(ctx context.Context) {
	monitors, err := u.db.GetMonitorsWithChannels(ctx)
	if err != nil {
		log.Printf("[outage-photo] failed to list monitors: %v", err)
		return
	}

	for _, m := range monitors {
		if m.OutageRegion == "" || m.OutageGroup == "" {
			if m.OutagePhotoMessageID != 0 {
				// Publish delete action for the bot service.
				msg := mq.OutagePhotoMsg{
					MonitorID:   m.ID,
					ChannelID:   m.ChannelID,
					MonitorName: m.Name,
					Action:      mq.OutagePhotoDelete,
					OldMsgID:    m.OutagePhotoMessageID,
				}
				if err := u.pub.Publish(ctx, mq.RoutingOutagePhoto, msg); err != nil {
					log.Printf("[outage-photo] monitor %d: failed to publish delete: %v", m.ID, err)
				}
				if err := u.db.ClearOutagePhoto(ctx, m.ID); err != nil {
					log.Printf("[outage-photo] monitor %d: failed to clear photo: %v", m.ID, err)
				}
			}
			continue
		}

		if !m.OutagePhotoEnabled {
			if m.OutagePhotoMessageID != 0 {
				msg := mq.OutagePhotoMsg{
					MonitorID:   m.ID,
					ChannelID:   m.ChannelID,
					MonitorName: m.Name,
					Action:      mq.OutagePhotoDelete,
					OldMsgID:    m.OutagePhotoMessageID,
				}
				if err := u.pub.Publish(ctx, mq.RoutingOutagePhoto, msg); err != nil {
					log.Printf("[outage-photo] monitor %d: failed to publish delete: %v", m.ID, err)
				}
				if err := u.db.ClearOutagePhoto(ctx, m.ID); err != nil {
					log.Printf("[outage-photo] monitor %d: failed to clear photo: %v", m.ID, err)
				}
			}
			continue
		}

		if err := u.updateOne(ctx, m); err != nil {
			log.Printf("[outage-photo] monitor %d: %v", m.ID, err)
		}
	}
}

func (u *Updater) updateOne(ctx context.Context, m *models.Monitor) error {
	// If the existing photo is from a previous day, delete it and force a fresh fetch.
	storedETag := m.OutagePhotoETag
	if m.OutagePhotoMessageID != 0 && m.OutagePhotoUpdatedAt != nil {
		kyiv, _ := time.LoadLocation("Europe/Kyiv")
		now := time.Now().In(kyiv)
		seenAt := m.OutagePhotoUpdatedAt.In(kyiv)
		if seenAt.Year() != now.Year() || seenAt.YearDay() != now.YearDay() {
			// Publish delete for the old photo.
			msg := mq.OutagePhotoMsg{
				MonitorID:   m.ID,
				ChannelID:   m.ChannelID,
				MonitorName: m.Name,
				Action:      mq.OutagePhotoDelete,
				OldMsgID:    m.OutagePhotoMessageID,
			}
			if err := u.pub.Publish(ctx, mq.RoutingOutagePhoto, msg); err != nil {
				return fmt.Errorf("publish delete stale photo: %w", err)
			}
			if err := u.db.ClearOutagePhoto(ctx, m.ID); err != nil {
				return fmt.Errorf("clear stale photo: %w", err)
			}
			log.Printf("[outage-photo] monitor %d: deleted stale photo, fetching new", m.ID)
			m.OutagePhotoMessageID = 0
			storedETag = ""
		}
	}

	data, etag, notModified, err := u.outage.GetGroupPhoto(m.OutageRegion, m.OutageGroup, storedETag)
	if err != nil {
		return fmt.Errorf("fetch photo: %w", err)
	}

	if notModified {
		return nil
	}

	filename := outage.GroupToFilename(m.OutageGroup)

	// Build caption from today's outage schedule.
	caption := ""
	if fact, factErr := u.outage.GetGroupFact(m.OutageRegion, m.OutageGroup); factErr == nil {
		caption = outage.BuildPhotoCaption(m.OutageGroup, fact, time.Now())
	} else {
		log.Printf("[outage-photo] monitor %d: failed to get fact for caption: %v", m.ID, factErr)
	}

	// Determine action: edit existing or send new.
	action := mq.OutagePhotoSend
	if m.OutagePhotoMessageID != 0 {
		action = mq.OutagePhotoEdit
	}

	msg := mq.OutagePhotoMsg{
		MonitorID:   m.ID,
		ChannelID:   m.ChannelID,
		MonitorName: m.Name,
		Action:      action,
		OldMsgID:    m.OutagePhotoMessageID,
		ImageData:   data,
		Filename:    filename,
		ETag:        etag,
		Caption:     caption,
	}
	if err := u.pub.Publish(ctx, mq.RoutingOutagePhoto, msg); err != nil {
		return fmt.Errorf("publish outage photo: %w", err)
	}

	log.Printf("[outage-photo] monitor %d: published %s action", m.ID, action)
	return nil
}
