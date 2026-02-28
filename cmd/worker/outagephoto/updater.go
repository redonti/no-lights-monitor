package outagephoto

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"no-lights-monitor/internal/database"
	"no-lights-monitor/internal/models"
	"no-lights-monitor/internal/mq"
)

const (
	ghRawImageURL = "https://raw.githubusercontent.com/Baskerville42/outage-data-ua/refs/heads/main/images"
)

// Updater is a background service that fetches outage schedule images
// and publishes them to RabbitMQ for the bot service to post to Telegram.
type Updater struct {
	db     *database.DB
	pub    *mq.Publisher
	client *http.Client
}

// NewUpdater creates a new outage photo updater.
func NewUpdater(db *database.DB, pub *mq.Publisher) *Updater {
	return &Updater{
		db:  db,
		pub: pub,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
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

// fetchResult holds a downloaded image and its ETag, or signals not-modified.
type fetchResult struct {
	notModified bool
	data        []byte
	etag        string
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
	filename := groupToFilename(m.OutageGroup)

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

	result, err := u.fetchImage(m.OutageRegion, filename, storedETag)
	if err != nil {
		return fmt.Errorf("fetch image: %w", err)
	}

	if result.notModified {
		return nil
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
		ImageData:   result.data,
		Filename:    filename,
		ETag:        result.etag,
	}
	if err := u.pub.Publish(ctx, mq.RoutingOutagePhoto, msg); err != nil {
		return fmt.Errorf("publish outage photo: %w", err)
	}

	log.Printf("[outage-photo] monitor %d: published %s action", m.ID, action)
	return nil
}

// fetchImage downloads an image using a conditional GET (If-None-Match).
func (u *Updater) fetchImage(region, filename, storedETag string) (*fetchResult, error) {
	imageURL := fmt.Sprintf("%s/%s/%s", ghRawImageURL, region, filename)

	req, err := http.NewRequest(http.MethodGet, imageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if storedETag != "" {
		req.Header.Set("If-None-Match", storedETag)
	}

	resp, err := u.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", imageURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return &fetchResult{notModified: true}, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: status %d", imageURL, resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	return &fetchResult{data: data, etag: resp.Header.Get("ETag")}, nil
}

// reLetterDigit matches the boundary between letters and digits.
var reLetterDigit = regexp.MustCompile(`([a-z])(\d)`)

// groupToFilename converts a group ID like "GPV1.1" to "gpv-1-1-emergency.png".
func groupToFilename(group string) string {
	s := strings.ToLower(group)
	s = reLetterDigit.ReplaceAllString(s, "${1}-${2}")
	s = strings.ReplaceAll(s, ".", "-")
	return s + "-emergency.png"
}
