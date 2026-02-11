package handlers

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"

	"no-lights-monitor/internal/database"
	"no-lights-monitor/internal/heartbeat"
	"no-lights-monitor/internal/models"
)

type Handlers struct {
	DB           *database.DB
	HeartbeatSvc *heartbeat.Service

	// In-memory response cache for /api/monitors.
	monitorCache   []byte
	monitorCacheAt time.Time
	monitorCacheMu sync.RWMutex
}

const monitorCacheTTL = 15 * time.Second

// Ping handles GET /api/ping/:token -- the core heartbeat endpoint.
func (h *Handlers) Ping(c *fiber.Ctx) error {
	token := c.Params("token")
	if token == "" {
		return c.SendStatus(fiber.StatusBadRequest)
	}

	ctx := context.Background()
	if ok := h.HeartbeatSvc.HandlePing(ctx, token); !ok {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "unknown token"})
	}

	return c.JSON(fiber.Map{"status": "ok"})
}

// GetMonitors returns all monitors with status. Response is cached server-side
// for 15 seconds so thousands of map visitors don't hit the DB.
func (h *Handlers) GetMonitors(c *fiber.Ctx) error {
	// Try serving from cache.
	h.monitorCacheMu.RLock()
	if h.monitorCache != nil && time.Since(h.monitorCacheAt) < monitorCacheTTL {
		data := h.monitorCache
		h.monitorCacheMu.RUnlock()
		c.Set("Content-Type", "application/json")
		c.Set("Cache-Control", "public, max-age=15")
		return c.Send(data)
	}
	h.monitorCacheMu.RUnlock()

	// Cache miss — refresh.
	h.monitorCacheMu.Lock()
	defer h.monitorCacheMu.Unlock()

	// Double-check after acquiring write lock.
	if h.monitorCache != nil && time.Since(h.monitorCacheAt) < monitorCacheTTL {
		c.Set("Content-Type", "application/json")
		c.Set("Cache-Control", "public, max-age=15")
		return c.Send(h.monitorCache)
	}

	ctx := context.Background()
	monitors, err := h.DB.GetAllMonitors(ctx)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to load monitors"})
	}

	now := time.Now()
	result := make([]fiber.Map, 0, len(monitors))
	for _, m := range monitors {
		dur := now.Sub(m.LastStatusChangeAt)
		result = append(result, fiber.Map{
			"id":              m.ID,
			"name":            m.Name,
			"address":         m.Address,
			"lat":             m.Latitude,
			"lng":             m.Longitude,
			"is_online":       m.IsOnline,
			"status_duration": database.FormatDuration(dur),
			"channel_name":    m.ChannelName,
		})
	}

	data, err := json.Marshal(result)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "marshal error"})
	}

	// Store in cache.
	h.monitorCache = data
	h.monitorCacheAt = now

	c.Set("Content-Type", "application/json")
	c.Set("Cache-Control", "public, max-age=15")
	return c.Send(data)
}

// GetHistory returns status change events for a monitor.
// Query params: ?from=2026-02-09T00:00:00Z&to=2026-02-10T00:00:00Z
// Defaults to the last 24 hours if not provided.
func (h *Handlers) GetHistory(c *fiber.Ctx) error {
	monitorID, err := c.ParamsInt("id")
	if err != nil || monitorID <= 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid monitor id"})
	}

	now := time.Now()
	from := now.Add(-24 * time.Hour)
	to := now

	if v := c.Query("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			from = t
		}
	}
	if v := c.Query("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			to = t
		}
	}

	// Cap to 30 days max.
	if to.Sub(from) > 30*24*time.Hour {
		from = to.Add(-30 * 24 * time.Hour)
	}

	ctx := context.Background()
	events, err := h.DB.GetStatusHistory(ctx, int64(monitorID), from, to)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to load history"})
	}

	if events == nil {
		events = make([]*models.StatusEvent, 0)
	}

	return c.JSON(fiber.Map{
		"monitor_id": monitorID,
		"from":       from.Format(time.RFC3339),
		"to":         to.Format(time.RFC3339),
		"events":     events,
	})
}

// GetStats returns global statistics.
func (h *Handlers) GetStats(c *fiber.Ctx) error {
	ctx := context.Background()
	stats, err := h.DB.GetStats(ctx)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to load stats"})
	}
	c.Set("Cache-Control", "public, max-age=15")
	return c.JSON(stats)
}
