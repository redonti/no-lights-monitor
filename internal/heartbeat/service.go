package heartbeat

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	probing "github.com/prometheus-community/pro-bing"
	"github.com/redis/go-redis/v9"

	"no-lights-monitor/internal/cache"
	"no-lights-monitor/internal/database"
	"no-lights-monitor/internal/models"
)

// Notifier sends Telegram messages on status changes.
type Notifier interface {
	NotifyStatusChange(monitorID, channelID int64, name, address string, notifyAddress, isOnline bool, duration time.Duration, when time.Time, outageRegion, outageGroup string, notifyOutage bool)
}

// monitorInfo is the in-memory representation used for fast ping lookups.
type monitorInfo struct {
	ID          int64
	ChannelID   int64
	Name        string
	Address     string
	Latitude    float64
	Longitude   float64
	MonitorType string // "heartbeat" or "ping"
	PingTarget  string // IP/hostname for ping monitors
	IsOnline       bool
	IsActive       bool // whether monitoring is enabled
	NotifyAddress  bool
	OutageRegion   string
	OutageGroup    string
	NotifyOutage   bool
	LastChange  time.Time
	mu          sync.Mutex
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
			ID:          m.ID,
			ChannelID:   m.ChannelID,
			Name:        m.Name,
			Address:     m.Address,
			Latitude:    m.Latitude,
			Longitude:   m.Longitude,
			MonitorType: m.MonitorType,
			PingTarget:  m.PingTarget,
			IsOnline:      m.IsOnline,
			IsActive:      m.IsActive,
			NotifyAddress: m.NotifyAddress,
			OutageRegion:  m.OutageRegion,
			OutageGroup:   m.OutageGroup,
			NotifyOutage:  m.NotifyOutage,
			LastChange:    m.LastStatusChangeAt,
		})
	}
	log.Printf("[heartbeat] loaded %d monitors into memory (grace period: %s)", len(monitors), s.threshold)
	return nil
}

// RegisterMonitor adds a new monitor to the in-memory map (called after DB insert).
func (s *Service) RegisterMonitor(m *models.Monitor) {
	s.monitors.Store(m.Token, &monitorInfo{
		ID:          m.ID,
		ChannelID:   m.ChannelID,
		Name:        m.Name,
		Address:     m.Address,
		Latitude:    m.Latitude,
		Longitude:   m.Longitude,
		MonitorType: m.MonitorType,
		PingTarget:  m.PingTarget,
		IsOnline:      false,
		IsActive:      m.IsActive,
		NotifyAddress: m.NotifyAddress,
		OutageRegion:  m.OutageRegion,
		OutageGroup:   m.OutageGroup,
		NotifyOutage:  m.NotifyOutage,
		LastChange:    m.LastStatusChangeAt,
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

// SetMonitorNotifyAddress updates the notify_address flag of a monitor in memory.
// Returns true if the monitor was found.
func (s *Service) SetMonitorNotifyAddress(token string, notifyAddress bool) bool {
	val, ok := s.monitors.Load(token)
	if !ok {
		return false
	}
	info := val.(*monitorInfo)
	info.mu.Lock()
	info.NotifyAddress = notifyAddress
	info.mu.Unlock()
	return true
}

// SetMonitorOutageGroup updates the outage region and group of a monitor in memory.
func (s *Service) SetMonitorOutageGroup(token, region, group string) bool {
	val, ok := s.monitors.Load(token)
	if !ok {
		return false
	}
	info := val.(*monitorInfo)
	info.mu.Lock()
	info.OutageRegion = region
	info.OutageGroup = group
	info.mu.Unlock()
	return true
}

// SetMonitorNotifyOutage updates the notify_outage flag of a monitor in memory.
func (s *Service) SetMonitorNotifyOutage(token string, notifyOutage bool) bool {
	val, ok := s.monitors.Load(token)
	if !ok {
		return false
	}
	info := val.(*monitorInfo)
	info.mu.Lock()
	info.NotifyOutage = notifyOutage
	info.mu.Unlock()
	return true
}

// RemoveMonitor removes a monitor from the in-memory map.
// This should be called after deleting a monitor from the database.
func (s *Service) RemoveMonitor(token string) {
	s.monitors.Delete(token)
}

// StartHeartbeatChecker runs a background loop that checks heartbeat monitors
// (devices that send pings to the API) for stale heartbeats.
func (s *Service) StartHeartbeatChecker(ctx context.Context, intervalSec int) {
	ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
	defer ticker.Stop()

	log.Printf("[heartbeat] heartbeat checker started (interval=%ds, threshold=%s)", intervalSec, s.threshold)

	for {
		select {
		case <-ctx.Done():
			log.Println("[heartbeat] heartbeat checker stopped")
			return
		case <-ticker.C:
			s.checkHeartbeatMonitors(ctx)
		}
	}
}

// StartPingChecker runs a background loop that actively ICMP-pings targets
// and checks ping monitors for status changes.
func (s *Service) StartPingChecker(ctx context.Context, intervalSec int) {
	ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
	defer ticker.Stop()

	log.Printf("[heartbeat] ping checker started (interval=%ds, threshold=%s)", intervalSec, s.threshold)

	for {
		select {
		case <-ctx.Done():
			log.Println("[heartbeat] ping checker stopped")
			return
		case <-ticker.C:
			s.checkPingMonitors(ctx)
		}
	}
}

// checkHeartbeatMonitors checks all heartbeat-type monitors for stale heartbeats
// and triggers status change notifications when needed.
func (s *Service) checkHeartbeatMonitors(ctx context.Context) {
	now := time.Now()
	inGracePeriod := now.Sub(s.startupTime) < s.threshold

	s.monitors.Range(func(key, value any) bool {
		info := value.(*monitorInfo)

		info.mu.Lock()
		if !info.IsActive || info.MonitorType != "heartbeat" {
			info.mu.Unlock()
			return true
		}
		monitorID := info.ID
		info.mu.Unlock()

		s.checkAndTransition(ctx, info, monitorID, now, inGracePeriod)
		return true
	})
}

// checkPingMonitors first executes all ICMP pings concurrently, then checks
// ping monitors for status changes.
func (s *Service) checkPingMonitors(ctx context.Context) {
	now := time.Now()
	inGracePeriod := now.Sub(s.startupTime) < s.threshold

	// Phase 1: Execute all ICMP pings concurrently.
	// This ensures even 100 ping monitors complete within ~5 seconds (ping timeout).
	var wg sync.WaitGroup
	s.monitors.Range(func(key, value any) bool {
		info := value.(*monitorInfo)
		info.mu.Lock()
		if !info.IsActive || info.MonitorType != "ping" || info.PingTarget == "" {
			info.mu.Unlock()
			return true
		}
		monitorID := info.ID
		pingTarget := info.PingTarget
		info.mu.Unlock()

		wg.Add(1)
		go func() {
			defer wg.Done()
			if s.PingHost(pingTarget) {
				if err := s.cache.SetHeartbeat(ctx, monitorID, now); err != nil {
					log.Printf("[heartbeat] redis set error for ping monitor %d: %v", monitorID, err)
				}
				if err := s.db.UpdateMonitorHeartbeat(ctx, monitorID, now); err != nil {
					log.Printf("[heartbeat] db heartbeat update error for ping monitor %d: %v", monitorID, err)
				}
			}
		}()
		return true
	})
	wg.Wait()

	// Phase 2: Check all ping monitors for status changes.
	s.monitors.Range(func(key, value any) bool {
		info := value.(*monitorInfo)

		info.mu.Lock()
		if !info.IsActive || info.MonitorType != "ping" {
			info.mu.Unlock()
			return true
		}
		monitorID := info.ID
		info.mu.Unlock()

		s.checkAndTransition(ctx, info, monitorID, now, inGracePeriod)
		return true
	})
}

// checkAndTransition reads the heartbeat from Redis and updates the monitor's
// online/offline state, firing notifications on transitions.
func (s *Service) checkAndTransition(ctx context.Context, info *monitorInfo, monitorID int64, now time.Time, inGracePeriod bool) {
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
			return
		}
	}

	isFresh := now.Sub(lastHB) <= s.threshold

	info.mu.Lock()

	var statusChanged bool
	var isNowOnline bool
	var duration time.Duration

	if info.IsOnline && !isFresh && !inGracePeriod {
		// Online → Offline transition.
		duration = now.Sub(info.LastChange)
		info.IsOnline = false
		offlineAt := lastHB
		if offlineAt.IsZero() {
			offlineAt = now.Add(-s.threshold)
		}
		info.LastChange = offlineAt
		statusChanged = true
		isNowOnline = false
	} else if !info.IsOnline && isFresh {
		// Offline → Online transition.
		duration = now.Sub(info.LastChange)
		info.IsOnline = true
		info.LastChange = now
		statusChanged = true
		isNowOnline = true
	}

	// Capture values for async operations.
	monitorName := info.Name
	monitorAddress := info.Address
	notifyAddress := info.NotifyAddress
	outageRegion := info.OutageRegion
	outageGroup := info.OutageGroup
	notifyOutage := info.NotifyOutage
	channelID := info.ChannelID
	info.mu.Unlock()

	if statusChanged {
		go func() {
			if err := s.db.UpdateMonitorStatus(context.Background(), monitorID, isNowOnline); err != nil {
				log.Printf("[heartbeat] failed to update status for monitor %d: %v", monitorID, err)
			}
		}()

		if s.notifier != nil && channelID != 0 {
			when := now
			if !isNowOnline {
				when = info.LastChange
			}
			go s.notifier.NotifyStatusChange(monitorID, channelID, monitorName, monitorAddress, notifyAddress, isNowOnline, duration, when, outageRegion, outageGroup, notifyOutage)
		}

		if isNowOnline {
			log.Printf("[heartbeat] monitor %d (%s) is now ONLINE (was off for %s)", monitorID, monitorName, database.FormatDuration(duration))
		} else {
			log.Printf("[heartbeat] monitor %d (%s) is now OFFLINE (was on for %s)", monitorID, monitorName, database.FormatDuration(duration))
		}
	}
}

// PingHost sends ICMP pings to the target and returns true if reachable.
func (s *Service) PingHost(target string) bool {
	pinger, err := probing.NewPinger(target)
	if err != nil {
		log.Printf("[heartbeat] failed to create pinger for %s: %v", target, err)
		return false
	}
	pinger.Count = 3
	pinger.Timeout = 5 * time.Second
	pinger.SetPrivileged(true) // required in Docker (raw ICMP sockets)
	if err := pinger.Run(); err != nil {
		return false
	}
	return pinger.Statistics().PacketsRecv > 0
}
