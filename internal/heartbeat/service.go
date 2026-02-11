package heartbeat

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

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
	IsActive   bool // whether monitoring is enabled
	LastChange time.Time
	mu         sync.Mutex
}

// Service handles heartbeat pings and offline detection.
type Service struct {
	monitors    sync.Map // token (string) -> *monitorInfo
	db          *database.DB
	cache       *cache.Cache
	notifier    Notifier
	threshold   time.Duration
	startupTime time.Time // when the service started, used for grace period
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
// It also records the startup time for grace period handling.
func (s *Service) LoadMonitors(ctx context.Context) error {
	monitors, err := s.db.GetAllMonitors(ctx)
	if err != nil {
		return err
	}

	// Record startup time for grace period.
	s.startupTime = time.Now()

	for _, m := range monitors {
		s.monitors.Store(m.Token, &monitorInfo{
			ID:         m.ID,
			ChannelID:  m.ChannelID,
			Name:       m.Name,
			Address:    m.Address,
			Latitude:   m.Latitude,
			Longitude:  m.Longitude,
			IsOnline:   m.IsOnline,
			IsActive:   m.IsActive,
			LastChange: m.LastStatusChangeAt,
		})
	}
	log.Printf("[heartbeat] loaded %d monitors into memory (grace period: %s)", len(monitors), s.threshold)
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
		IsActive:   m.IsActive,
		LastChange: m.LastStatusChangeAt,
	})
}

// SetMonitorActive updates the active status of a monitor in memory.
// Returns true if the monitor was found.
func (s *Service) SetMonitorActive(token string, isActive bool) bool {
	val, ok := s.monitors.Load(token)
	if !ok {
		return false
	}
	info := val.(*monitorInfo)
	info.mu.Lock()
	info.IsActive = isActive
	info.mu.Unlock()
	return true
}

// RemoveMonitor removes a monitor from the in-memory map.
// This should be called after deleting a monitor from the database.
func (s *Service) RemoveMonitor(token string) {
	s.monitors.Delete(token)
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

	// Check and update online status under lock.
	info.mu.Lock()
	wasOffline := !info.IsOnline
	var offlineDuration time.Duration
	if wasOffline {
		offlineDuration = now.Sub(info.LastChange)
		info.IsOnline = true
		info.LastChange = now
	}
	// Capture values needed for async operations while still under lock.
	monitorID := info.ID
	monitorName := info.Name
	channelID := info.ChannelID
	info.mu.Unlock()

	// Perform expensive operations outside the lock.
	if wasOffline {
		// Persist to DB.
		go func() {
			if err := s.db.UpdateMonitorStatus(context.Background(), monitorID, true); err != nil {
				log.Printf("[heartbeat] failed to update status for monitor %d: %v", monitorID, err)
			}
			if err := s.db.UpdateMonitorHeartbeat(context.Background(), monitorID, now); err != nil {
				log.Printf("[heartbeat] failed to update heartbeat for monitor %d: %v", monitorID, err)
			}
		}()

		// Notify Telegram channel.
		if s.notifier != nil && channelID != 0 {
			go s.notifier.NotifyStatusChange(channelID, monitorName, true, offlineDuration)
		}

		log.Printf("[heartbeat] monitor %d (%s) is now ONLINE (was off for %s)", monitorID, monitorName, database.FormatDuration(offlineDuration))
	} else {
		// Just update heartbeat timestamp in DB.
		go func() {
			if err := s.db.UpdateMonitorHeartbeat(context.Background(), monitorID, now); err != nil {
				log.Printf("[heartbeat] failed to update heartbeat for monitor %d: %v", monitorID, err)
			}
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
	inGracePeriod := now.Sub(s.startupTime) < s.threshold

	s.monitors.Range(func(key, value any) bool {
		info := value.(*monitorInfo)

		// Lock and check state.
		info.mu.Lock()
		// Skip inactive monitors (paused by user).
		if !info.IsActive {
			info.mu.Unlock()
			return true
		}

		// Capture monitor ID for cache lookup.
		monitorID := info.ID
		info.mu.Unlock()

		// Check heartbeat in cache (outside lock - this is an I/O operation).
		lastHB, err := s.cache.GetHeartbeat(ctx, monitorID)
		if err != nil {
			if errors.Is(err, redis.Nil) {
				// Redis key doesn't exist (new monitor, Redis restarted and lost data).
				// Treat as very old heartbeat so monitor will be marked offline if it hasn't pinged.
				lastHB = time.Time{} // Zero time (Unix epoch)
			} else {
				// Redis connection error or other issue.
				// Skip this monitor to avoid false offline notifications during Redis outages.
				log.Printf("[heartbeat] redis error for monitor %d: %v", monitorID, err)
				return true
			}
		}

		// Determine if heartbeat is fresh or stale.
		isFresh := now.Sub(lastHB) <= s.threshold

		// Lock again to update state if needed.
		info.mu.Lock()

		var statusChanged bool
		var isNowOnline bool
		var duration time.Duration

		// Re-check current state after re-acquiring lock (in case it changed during I/O).
		// In practice, checkAll is single-threaded, but this is more robust.
		if info.IsOnline && !isFresh && !inGracePeriod {
			// Online → Offline transition.
			// Skip during grace period to prevent false offline notifications after system restart.
			duration = now.Sub(info.LastChange)
			info.IsOnline = false
			info.LastChange = now
			statusChanged = true
			isNowOnline = false
		} else if !info.IsOnline && isFresh {
			// Offline → Online transition.
			// Allow during grace period - monitors coming online is always good!
			duration = now.Sub(info.LastChange)
			info.IsOnline = true
			info.LastChange = now
			statusChanged = true
			isNowOnline = true
		}

		// Capture values for async operations.
		monitorName := info.Name
		channelID := info.ChannelID
		info.mu.Unlock()

		// Perform expensive operations outside the lock.
		if statusChanged {
			go func() {
				if err := s.db.UpdateMonitorStatus(context.Background(), monitorID, isNowOnline); err != nil {
					log.Printf("[heartbeat] failed to update status for monitor %d: %v", monitorID, err)
				}
			}()

			if s.notifier != nil && channelID != 0 {
				go s.notifier.NotifyStatusChange(channelID, monitorName, isNowOnline, duration)
			}

			if isNowOnline {
				log.Printf("[heartbeat] monitor %d (%s) is now ONLINE (was off for %s)", monitorID, monitorName, database.FormatDuration(duration))
			} else {
				log.Printf("[heartbeat] monitor %d (%s) is now OFFLINE (was on for %s)", monitorID, monitorName, database.FormatDuration(duration))
			}
		}

		return true
	})
}
