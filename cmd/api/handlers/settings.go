package handlers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gofiber/fiber/v2"

	"no-lights-monitor/internal/database"
	"no-lights-monitor/internal/geocode"
)

var proxyHTTPClient = &http.Client{Timeout: 10 * time.Second}

// ProxyOutage forwards outage API requests to the outage service.
// Handles /api/outage/* routes.
func (h *Handlers) ProxyOutage(c *fiber.Ctx) error {
	if h.OutageServiceURL == "" {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "outage service not configured"})
	}
	// Build target URL: take everything after /api/outage
	path := c.Params("*")
	url := fmt.Sprintf("%s/api/outage/%s", h.OutageServiceURL, path)

	resp, err := proxyHTTPClient.Get(url)
	if err != nil {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "outage service unavailable"})
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "failed to read outage response"})
	}

	c.Set("Content-Type", resp.Header.Get("Content-Type"))
	return c.Status(resp.StatusCode).Send(body)
}

// GetSettings returns the full monitor configuration for the settings page.
func (h *Handlers) GetSettings(c *fiber.Ctx) error {
	token := c.Params("token")
	if token == "" {
		return c.SendStatus(fiber.StatusBadRequest)
	}

	ctx := context.Background()
	m, err := h.DB.GetMonitorBySettingsToken(ctx, token)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "monitor not found"})
	}

	dur := time.Since(m.LastStatusChangeAt)

	return c.JSON(fiber.Map{
		"id":              m.ID,
		"name":            m.Name,
		"address":         m.Address,
		"latitude":        m.Latitude,
		"longitude":       m.Longitude,
		"is_online":       m.IsOnline,
		"is_active":       m.IsActive,
		"is_public":       m.IsPublic,
		"notify_address":  m.NotifyAddress,
		"outage_region":   m.OutageRegion,
		"outage_group":    m.OutageGroup,
		"notify_outage":        m.NotifyOutage,
		"outage_photo_enabled": m.OutagePhotoEnabled,
		"graph_enabled":        m.GraphEnabled,
		"channel_name":         m.ChannelName,
		"monitor_type":    m.MonitorType,
		"ping_target":     m.PingTarget,
		"status_duration": database.FormatDuration(dur),
	})
}

const (
	maxNameLen         = 100
	maxAddressLen      = 300
	maxOutageRegionLen = 50
	maxOutageGroupLen  = 100
)

// settingsUpdateRequest is the JSON body for updating monitor settings.
type settingsUpdateRequest struct {
	Name          *string  `json:"name"`
	Address       *string  `json:"address"`
	Latitude      *float64 `json:"latitude"`
	Longitude     *float64 `json:"longitude"`
	IsPublic      *bool    `json:"is_public"`
	NotifyAddress *bool    `json:"notify_address"`
	OutageRegion  *string  `json:"outage_region"`
	OutageGroup   *string  `json:"outage_group"`
	NotifyOutage       *bool `json:"notify_outage"`
	OutagePhotoEnabled *bool `json:"outage_photo_enabled"`
	GraphEnabled       *bool `json:"graph_enabled"`
}

// UpdateSettings updates editable fields of a monitor.
func (h *Handlers) UpdateSettings(c *fiber.Ctx) error {
	token := c.Params("token")
	if token == "" {
		return c.SendStatus(fiber.StatusBadRequest)
	}

	ctx := context.Background()
	m, err := h.DB.GetMonitorBySettingsToken(ctx, token)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "monitor not found"})
	}

	var req settingsUpdateRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	// Update name.
	if req.Name != nil && *req.Name != m.Name && len(*req.Name) >= 2 && len(*req.Name) <= maxNameLen {
		if err := h.DB.UpdateMonitorName(ctx, m.ID, *req.Name); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update name"})
		}
	}

	// Update address — either with provided coordinates or geocode.
	if req.Address != nil && len(*req.Address) >= 3 && len(*req.Address) <= maxAddressLen {
		lat, lng := m.Latitude, m.Longitude
		if req.Latitude != nil && req.Longitude != nil {
			lat, lng = *req.Latitude, *req.Longitude
		} else {
			// Geocode the address.
			result, err := geocode.Search(ctx, *req.Address)
			if err == nil && result != nil {
				lat, lng = result.Latitude, result.Longitude
				req.Address = &result.DisplayName
			}
		}
		if err := h.DB.UpdateMonitorAddress(ctx, m.ID, *req.Address, lat, lng); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update address"})
		}
	}

	// Update map visibility.
	if req.IsPublic != nil && *req.IsPublic != m.IsPublic {
		if err := h.DB.SetMonitorPublic(ctx, m.ID, *req.IsPublic); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update map visibility"})
		}
	}

	// Update notify address.
	if req.NotifyAddress != nil && *req.NotifyAddress != m.NotifyAddress {
		if err := h.DB.SetMonitorNotifyAddress(ctx, m.ID, *req.NotifyAddress); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update notify_address"})
		}
	}

	// Update outage group.
	if req.OutageRegion != nil && req.OutageGroup != nil &&
		len(*req.OutageRegion) <= maxOutageRegionLen && len(*req.OutageGroup) <= maxOutageGroupLen {
		if *req.OutageRegion != m.OutageRegion || *req.OutageGroup != m.OutageGroup {
			if err := h.DB.SetMonitorOutageGroup(ctx, m.ID, *req.OutageRegion, *req.OutageGroup); err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update outage group"})
			}
		}
	}

	// Update notify outage.
	if req.NotifyOutage != nil && *req.NotifyOutage != m.NotifyOutage {
		if err := h.DB.SetMonitorNotifyOutage(ctx, m.ID, *req.NotifyOutage); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update notify_outage"})
		}
	}

	// Update outage photo enabled.
	if req.OutagePhotoEnabled != nil && *req.OutagePhotoEnabled != m.OutagePhotoEnabled {
		if err := h.DB.SetMonitorOutagePhotoEnabled(ctx, m.ID, *req.OutagePhotoEnabled); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update outage_photo_enabled"})
		}
	}

	// Update graph enabled.
	if req.GraphEnabled != nil && *req.GraphEnabled != m.GraphEnabled {
		if err := h.DB.SetMonitorGraphEnabled(ctx, m.ID, *req.GraphEnabled); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update graph_enabled"})
		}
	}

	return c.JSON(fiber.Map{"status": "ok"})
}

// StopMonitor pauses monitoring via settings page.
func (h *Handlers) StopMonitor(c *fiber.Ctx) error {
	token := c.Params("token")
	if token == "" {
		return c.SendStatus(fiber.StatusBadRequest)
	}

	ctx := context.Background()
	m, err := h.DB.GetMonitorBySettingsToken(ctx, token)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "monitor not found"})
	}

	if !m.IsActive {
		return c.JSON(fiber.Map{"status": "already_stopped"})
	}

	if err := h.DB.SetMonitorActive(ctx, m.ID, false); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to stop monitor"})
	}

	return c.JSON(fiber.Map{"status": "ok"})
}

// ResumeMonitor resumes monitoring via settings page.
func (h *Handlers) ResumeMonitor(c *fiber.Ctx) error {
	token := c.Params("token")
	if token == "" {
		return c.SendStatus(fiber.StatusBadRequest)
	}

	ctx := context.Background()
	m, err := h.DB.GetMonitorBySettingsToken(ctx, token)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "monitor not found"})
	}

	if m.IsActive {
		return c.JSON(fiber.Map{"status": "already_active"})
	}

	if err := h.DB.SetMonitorActive(ctx, m.ID, true); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to resume monitor"})
	}

	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteMonitorWeb deletes a monitor via settings page.
func (h *Handlers) DeleteMonitorWeb(c *fiber.Ctx) error {
	token := c.Params("token")
	if token == "" {
		return c.SendStatus(fiber.StatusBadRequest)
	}

	ctx := context.Background()
	m, err := h.DB.GetMonitorBySettingsToken(ctx, token)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "monitor not found"})
	}

	if err := h.DB.DeleteMonitor(ctx, m.ID); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to delete monitor"})
	}

	return c.JSON(fiber.Map{"status": "ok"})
}
