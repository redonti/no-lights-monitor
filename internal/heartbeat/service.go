package heartbeat

import (
	"context"
	"log"
	"sync"
	"time"

	"no-lights-monitor/internal/cache"
	"no-lights-monitor/internal/database"
	"no-lights-monitor/internal/models"
)

// Notifier sends Telegram messages on status changes.
type Notifier interface {
	NotifyStatusChange(channelID int64, name string, isOnline bool, duration time.Duration)
}

// monitorInfo is the in-memory representation used for fast ping lookups.
type monitorInfo struct {
	ID         int64
	ChannelID  int64
	Name       string
	Address    string
	Latitude   float64
	Longitude  float64
	IsOnline   bool
	LastChange time.Time
	mu         sync.Mutex
}

// Service handles heartbeat pings and offline detection.
type Service struct {
	monitors  sync.Map // token (string) -> *monitorInfo
	db        *database.DB
	cache     *cache.Cache
	notifier  Notifier
	threshold time.Duration
}

func NewService(db *database.DB, c *cache.Cache, notifier Notifier, thresholdSec int) *Service {
	return &Service{
		db:        db,
		cache:     c,
		notifier:  notifier,
		threshold: time.Duration(thresholdSec) * time.Second,
	}
}

// SetNotifier sets the notifier (used to break circular dependency at startup).
func (s *Service) SetNotifier(n Notifier) {
	s.notifier = n
}

// LoadMonitors reads all monitors from the DB into the in-memory map.
func (s *Service) LoadMonitors(ctx context.Context) error {
	monitors, err := s.db.GetAllMonitors(ctx)
	if err != nil {
		return err
	}
	for _, m := range monitors {
		s.monitors.Store(m.Token, &monitorInfo{
			ID:         m.ID,
			ChannelID:  m.ChannelID,
			Name:       m.Name,
			Address:    m.Address,
			Latitude:   m.Latitude,
			Longitude:  m.Longitude,
			IsOnline:   m.IsOnline,
			LastChange: m.LastStatusChangeAt,
		})
	}
	log.Printf("[heartbeat] loaded %d monitors into memory", len(monitors))
	return nil
}

// RegisterMonitor adds a new monitor to the in-memory map (called after DB insert).
func (s *Service) RegisterMonitor(m *models.Monitor) {
	s.monitors.Store(m.Token, &monitorInfo{
		ID:         m.ID,
		ChannelID:  m.ChannelID,
		Name:       m.Name,
		Address:    m.Address,
		Latitude:   m.Latitude,
		Longitude:  m.Longitude,
		IsOnline:   false,
		LastChange: m.LastStatusChangeAt,
	})
}

// HandlePing processes a heartbeat ping for the given token.
// Returns true if the token was valid.
func (s *Service) HandlePing(ctx context.Context, token string) bool {
	val, ok := s.monitors.Load(token)
	if !ok {
		return false
	}
	info := val.(*monitorInfo)
	now := time.Now()

	// Update heartbeat in Redis.
	if err := s.cache.SetHeartbeat(ctx, info.ID, now); err != nil {
		log.Printf("[heartbeat] redis set error for monitor %d: %v", info.ID, err)
	}

	// If monitor was offline, transition to online.
	info.mu.Lock()
	wasOffline := !info.IsOnline
	if wasOffline {
		duration := now.Sub(info.LastChange)
		info.IsOnline = true
		info.LastChange = now
		info.mu.Unlock()

		// Persist to DB.
		go func() {
			_ = s.db.UpdateMonitorStatus(context.Background(), info.ID, true)
			_ = s.db.UpdateMonitorHeartbeat(context.Background(), info.ID, now)
		}()

		// Notify Telegram channel.
		if s.notifier != nil && info.ChannelID != 0 {
			go s.notifier.NotifyStatusChange(info.ChannelID, info.Name, true, duration)
		}

		log.Printf("[heartbeat] monitor %d (%s) is now ONLINE (was off for %s)", info.ID, info.Name, database.FormatDuration(duration))
	} else {
		info.mu.Unlock()
		// Just update heartbeat timestamp in DB.
		go func() {
			_ = s.db.UpdateMonitorHeartbeat(context.Background(), info.ID, now)
		}()
	}

	return true
}

// StartChecker runs a background loop that marks monitors as offline
// when their heartbeats go stale.
func (s *Service) StartChecker(ctx context.Context, intervalSec int) {
	ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
	defer ticker.Stop()

	log.Printf("[heartbeat] checker started (interval=%ds, threshold=%s)", intervalSec, s.threshold)

	for {
		select {
		case <-ctx.Done():
			log.Println("[heartbeat] checker stopped")
			return
		case <-ticker.C:
			s.checkAll(ctx)
		}
	}
}

func (s *Service) checkAll(ctx context.Context) {
	now := time.Now()

	s.monitors.Range(func(key, value any) bool {
		info := value.(*monitorInfo)
		info.mu.Lock()

		if !info.IsOnline {
			info.mu.Unlock()
			return true
		}

		lastHB, err := s.cache.GetHeartbeat(ctx, info.ID)
		if err != nil {
			info.mu.Unlock()
			return true
		}

		if now.Sub(lastHB) > s.threshold {
			duration := now.Sub(info.LastChange)
			info.IsOnline = false
			info.LastChange = now
			info.mu.Unlock()

			go func() {
				_ = s.db.UpdateMonitorStatus(context.Background(), info.ID, false)
			}()

			if s.notifier != nil && info.ChannelID != 0 {
				go s.notifier.NotifyStatusChange(info.ChannelID, info.Name, false, duration)
			}

			log.Printf("[heartbeat] monitor %d (%s) is now OFFLINE (was on for %s)", info.ID, info.Name, database.FormatDuration(duration))
		} else {
			info.mu.Unlock()
		}

		return true
	})
}
