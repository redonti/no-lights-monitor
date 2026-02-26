package outagephoto

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	tele "gopkg.in/telebot.v3"

	"no-lights-monitor/internal/bot"
	"no-lights-monitor/internal/database"
	"no-lights-monitor/internal/models"
)

const (
	ghRawImageURL = "https://raw.githubusercontent.com/Baskerville42/outage-data-ua/refs/heads/main/images"
)

// Updater is a background service that posts/updates outage schedule
// images in each monitor's Telegram channel. Similar to graph.Updater.
type Updater struct {
	db     *database.DB
	bot    *tele.Bot
	client *http.Client
}

// NewUpdater creates a new outage photo updater.
func NewUpdater(db *database.DB, b *tele.Bot) *Updater {
	return &Updater{
		db:  db,
		bot: b,
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
				u.deleteOldPhoto(m)
				if err := u.db.ClearOutagePhoto(ctx, m.ID); err != nil {
					log.Printf("[outage-photo] monitor %d: failed to clear photo: %v", m.ID, err)
				}
			}
			continue
		}

		if !m.OutagePhotoEnabled {
			if m.OutagePhotoMessageID != 0 {
				u.deleteOldPhoto(m)
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

	// If the existing photo is from a previous day, delete it and force a plain
	// GET (no If-None-Match) so we always get fresh image bytes for the new day.
	storedETag := m.OutagePhotoETag
	if m.OutagePhotoMessageID != 0 && m.OutagePhotoUpdatedAt != nil {
		kyiv, _ := time.LoadLocation("Europe/Kyiv")
		now := time.Now().In(kyiv)
		seenAt := m.OutagePhotoUpdatedAt.In(kyiv)
		if seenAt.Year() != now.Year() || seenAt.YearDay() != now.YearDay() {
			u.deleteOldPhoto(m)
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

	// New image received — post or update Telegram.
	chat := &tele.Chat{ID: m.ChannelID}
	sendOpts := &tele.SendOptions{DisableNotification: bot.IsQuietHour()}

	if m.OutagePhotoMessageID != 0 {
		// Try to edit existing photo in-place.
		editPhoto := &tele.Photo{
			File: tele.FromReader(newPNGReader(result.data, filename)),
		}
		editMsg := &tele.Message{
			ID:   m.OutagePhotoMessageID,
			Chat: chat,
		}
		_, err := u.bot.EditMedia(editMsg, editPhoto)
		if err != nil {
			if strings.Contains(err.Error(), "message is not modified") {
				if err := u.db.UpdateOutagePhoto(ctx, m.ID, m.OutagePhotoMessageID, result.etag, time.Now()); err != nil {
					return fmt.Errorf("save photo timestamp: %w", err)
				}
				return nil
			}
			if u.handleChannelError(ctx, m, err) {
				return nil
			}
			log.Printf("[outage-photo] monitor %d: edit failed (%v), sending new", m.ID, err)
			u.deleteOldPhoto(m)
		} else {
			if err := u.db.UpdateOutagePhoto(ctx, m.ID, m.OutagePhotoMessageID, result.etag, time.Now()); err != nil {
				return fmt.Errorf("save photo id: %w", err)
			}
			log.Printf("[outage-photo] monitor %d: updated photo (msg %d)", m.ID, m.OutagePhotoMessageID)
			return nil
		}
	}

	// Send new photo.
	photo := &tele.Photo{
		File: tele.FromReader(newPNGReader(result.data, filename)),
	}
	sent, err := u.bot.Send(chat, photo, sendOpts)
	if err != nil {
		if u.handleChannelError(ctx, m, err) {
			return nil
		}
		return fmt.Errorf("send photo: %w", err)
	}
	if err := u.db.UpdateOutagePhoto(ctx, m.ID, sent.ID, result.etag, time.Now()); err != nil {
		return fmt.Errorf("save photo id: %w", err)
	}
	log.Printf("[outage-photo] monitor %d: sent new photo (msg %d)", m.ID, sent.ID)
	return nil
}

func (u *Updater) deleteOldPhoto(m *models.Monitor) {
	msg := &tele.Message{
		ID:   m.OutagePhotoMessageID,
		Chat: &tele.Chat{ID: m.ChannelID},
	}
	if err := u.bot.Delete(msg); err != nil {
		log.Printf("[outage-photo] monitor %d: failed to delete old photo (msg %d): %v", m.ID, m.OutagePhotoMessageID, err)
	}
}

// handleChannelError queries the monitor owner and delegates to bot.NotifyChannelError.
func (u *Updater) handleChannelError(ctx context.Context, m *models.Monitor, err error) bool {
	ownerID, dbErr := u.db.GetOwnerTelegramIDByMonitorID(ctx, m.ID)
	if dbErr != nil {
		log.Printf("[outage-photo] failed to get owner for monitor %d: %v", m.ID, dbErr)
		return false
	}
	return bot.NotifyChannelError(ctx, u.bot, u.db, err, ownerID, m)
}

// fetchImage downloads an image using a conditional GET (If-None-Match).
// Returns notModified=true if the server responded with 304.
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

// reLetterDigit matches the boundary between letters and digits (e.g. "gpv1" → "gpv-1").
var reLetterDigit = regexp.MustCompile(`([a-z])(\d)`)

// groupToFilename converts a group ID like "GPV1.1" to "gpv-1-1-emergency.png".
func groupToFilename(group string) string {
	s := strings.ToLower(group)
	s = reLetterDigit.ReplaceAllString(s, "${1}-${2}")
	s = strings.ReplaceAll(s, ".", "-")
	return s + "-emergency.png"
}

// namedReader wraps an io.Reader with a Name() for telebot file uploads.
type namedReader struct {
	io.Reader
	name string
}

func (r *namedReader) Name() string { return r.name }

func newPNGReader(data []byte, name string) *namedReader {
	return &namedReader{Reader: bytes.NewReader(data), name: name}
}
