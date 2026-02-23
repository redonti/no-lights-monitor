package outage

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/gofiber/fiber/v2"
)

// Handlers holds the outage service dependencies.
type Handlers struct {
	Fetcher *Fetcher
}

// RegisterRoutes registers outage API routes on the given Fiber app group.
func (h *Handlers) RegisterRoutes(api fiber.Router) {
	outage := api.Group("/outage")
	outage.Get("/regions", h.GetRegions)
	outage.Get("/:region/groups", h.GetGroups)
	outage.Get("/:region", h.GetRegionFact)
	outage.Get("/:region/:group", h.GetGroupFact)
}

// GetRegions returns a list of available regions.
func (h *Handlers) GetRegions(c *fiber.Ctx) error {
	regions := h.Fetcher.GetAllRegions()
	if len(regions) == 0 {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "outage data not yet loaded",
		})
	}
	return c.JSON(regions)
}

// GetGroups returns the list of available group IDs for a region.
func (h *Handlers) GetGroups(c *fiber.Ctx) error {
	region := c.Params("region")

	rd := h.Fetcher.GetRegionData(region)
	if rd == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": fmt.Sprintf("region %q not found", region),
		})
	}

	todayKey := strconv.FormatInt(rd.Fact.Today, 10)
	dayData, ok := rd.Fact.Data[todayKey]
	if !ok {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "no fact data for today",
		})
	}

	groups := make([]GroupInfo, 0, len(dayData))
	for g := range dayData {
		name := g
		if n, ok := rd.Preset.SchNames[g]; ok {
			name = n
		}
		groups = append(groups, GroupInfo{ID: g, Name: name})
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].ID < groups[j].ID })

	return c.JSON(fiber.Map{
		"region": rd.RegionID,
		"groups": groups,
	})
}

// GetRegionFact returns all groups' hourly fact data for a region.
func (h *Handlers) GetRegionFact(c *fiber.Ctx) error {
	region := c.Params("region")

	rd := h.Fetcher.GetRegionData(region)
	if rd == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": fmt.Sprintf("region %q not found", region),
		})
	}

	todayKey := strconv.FormatInt(rd.Fact.Today, 10)
	dayData, ok := rd.Fact.Data[todayKey]
	if !ok {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "no fact data for today",
		})
	}

	return c.JSON(RegionFactSummary{
		Region:      rd.RegionID,
		LastUpdated: rd.LastUpdated,
		FactUpdate:  rd.Fact.Update,
		Groups:      dayData,
	})
}

// GetGroupFact returns hourly fact data for a specific group in a region.
func (h *Handlers) GetGroupFact(c *fiber.Ctx) error {
	region := c.Params("region")
	group := c.Params("group")

	rd := h.Fetcher.GetRegionData(region)
	if rd == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": fmt.Sprintf("region %q not found", region),
		})
	}

	todayKey := strconv.FormatInt(rd.Fact.Today, 10)
	dayData, ok := rd.Fact.Data[todayKey]
	if !ok {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "no fact data for today",
		})
	}

	hours, ok := dayData[group]
	if !ok {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": fmt.Sprintf("group %q not found in region %q", group, region),
		})
	}

	return c.JSON(GroupHourlyFact{
		Region:      rd.RegionID,
		Group:       group,
		Date:        todayKey,
		LastUpdated: rd.LastUpdated,
		FactUpdate:  rd.Fact.Update,
		Hours:       hours,
	})
}
