package outagephoto

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
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

// fetchedImage holds a downloaded image and its Last-Modified date.
type fetchedImage struct {
	data         []byte
	lastModified time.Time
}

// runCache holds per-run cached data to avoid duplicate downloads.
type runCache struct {
	images map[string]*fetchedImage // key: "region/filename"
	errs   map[string]error
}

func newRunCache() *runCache {
	return &runCache{
		images: make(map[string]*fetchedImage),
		errs:   make(map[string]error),
	}
}

func (u *Updater) runAll(ctx context.Context) {
	monitors, err := u.db.GetMonitorsWithChannels(ctx)
	if err != nil {
		log.Printf("[outage-photo] failed to list monitors: %v", err)
		return
	}

	cache := newRunCache()

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

		if !m.NotifyOutage {
			if m.OutagePhotoMessageID != 0 {
				u.deleteOldPhoto(m)
				if err := u.db.ClearOutagePhoto(ctx, m.ID); err != nil {
					log.Printf("[outage-photo] monitor %d: failed to clear photo: %v", m.ID, err)
				}
			}
			continue
		}

		if err := u.updateOne(ctx, m, cache); err != nil {
			log.Printf("[outage-photo] monitor %d: %v", m.ID, err)
		}
	}
}

func (u *Updater) updateOne(ctx context.Context, m *models.Monitor, cache *runCache) error {
	filename := groupToFilename(m.OutageGroup)
	cacheKey := m.OutageRegion + "/" + filename

	// Fetch image + Last-Modified (cached per region/group per run).
	img, err := u.getCachedImage(cache, cacheKey, m.OutageRegion, filename)
	if err != nil {
		return fmt.Errorf("fetch image: %w", err)
	}

	// If Last-Modified matches stored date, nothing changed.
	if m.OutagePhotoUpdatedAt != nil && m.OutagePhotoUpdatedAt.Equal(img.lastModified) {
		return nil
	}

	// Check if image is from today (Europe/Kyiv).
	kyiv, _ := time.LoadLocation("Europe/Kyiv")
	now := time.Now().In(kyiv)
	modKyiv := img.lastModified.In(kyiv)
	if modKyiv.Year() != now.Year() || modKyiv.YearDay() != now.YearDay() {
		// Image is stale (not from today) — delete old photo if exists.
		if m.OutagePhotoMessageID != 0 {
			u.deleteOldPhoto(m)
			if err := u.db.ClearOutagePhoto(ctx, m.ID); err != nil {
				return fmt.Errorf("clear stale photo: %w", err)
			}
			log.Printf("[outage-photo] monitor %d: deleted stale photo", m.ID)
		}
		return nil
	}

	chat := &tele.Chat{ID: m.ChannelID}
	silent := &tele.SendOptions{DisableNotification: true}

	if m.OutagePhotoMessageID != 0 {
		// Try to edit existing photo in-place.
		editPhoto := &tele.Photo{
			File: tele.FromReader(newPNGReader(img.data, filename)),
		}
		editMsg := &tele.Message{
			ID:   m.OutagePhotoMessageID,
			Chat: chat,
		}
		_, err := u.bot.EditMedia(editMsg, editPhoto)
		if err != nil {
			if strings.Contains(err.Error(), "message is not modified") {
				if err := u.db.UpdateOutagePhoto(ctx, m.ID, m.OutagePhotoMessageID, img.lastModified); err != nil {
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
			if err := u.db.UpdateOutagePhoto(ctx, m.ID, m.OutagePhotoMessageID, img.lastModified); err != nil {
				return fmt.Errorf("save photo id: %w", err)
			}
			log.Printf("[outage-photo] monitor %d: updated photo (msg %d)", m.ID, m.OutagePhotoMessageID)
			return nil
		}
	}

	// Send new photo.
	photo := &tele.Photo{
		File: tele.FromReader(newPNGReader(img.data, filename)),
	}
	sent, err := u.bot.Send(chat, photo, silent)
	if err != nil {
		if u.handleChannelError(ctx, m, err) {
			return nil
		}
		return fmt.Errorf("send photo: %w", err)
	}
	if err := u.db.UpdateOutagePhoto(ctx, m.ID, sent.ID, img.lastModified); err != nil {
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

// getCachedImage downloads an image and parses Last-Modified, caching per run.
func (u *Updater) getCachedImage(cache *runCache, key, region, filename string) (*fetchedImage, error) {
	if err, ok := cache.errs[key]; ok {
		return nil, err
	}
	if img, ok := cache.images[key]; ok {
		return img, nil
	}

	imageURL := fmt.Sprintf("%s/%s/%s", ghRawImageURL, region, filename)
	resp, err := u.client.Get(imageURL)
	if err != nil {
		cache.errs[key] = err
		return nil, fmt.Errorf("GET %s: %w", imageURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("GET %s: status %d", imageURL, resp.StatusCode)
		cache.errs[key] = err
		return nil, err
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		cache.errs[key] = err
		return nil, fmt.Errorf("read body: %w", err)
	}

	// Parse Last-Modified header for freshness check.
	var lastModified time.Time
	if lm := resp.Header.Get("Last-Modified"); lm != "" {
		lastModified, _ = time.Parse(time.RFC1123, lm)
	}
	if lastModified.IsZero() {
		// Fallback: use current time (will be treated as fresh today).
		lastModified = time.Now()
	}

	img := &fetchedImage{data: data, lastModified: lastModified}
	cache.images[key] = img
	return img, nil
}

// groupToFilename converts a group ID like "GPV1.1" to "gpv-1-1-emergency.png".
func groupToFilename(group string) string {
	s := strings.ToLower(group)
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
