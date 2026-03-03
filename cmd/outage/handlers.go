package main

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/gofiber/fiber/v2"

	"no-lights-monitor/internal/outage"
)

type handlers struct {
	fetcher *Fetcher
}

func (h *handlers) registerRoutes(api fiber.Router) {
	g := api.Group("/outage")
	g.Get("/regions", h.getRegions)
	g.Get("/:region/groups", h.getGroups)
	g.Get("/:region", h.getRegionFact)
	g.Get("/:region/:group/photo", h.getGroupPhoto)
	g.Get("/:region/:group", h.getGroupFact)
}

func (h *handlers) getRegions(c *fiber.Ctx) error {
	regions := h.fetcher.getAllRegions()
	if len(regions) == 0 {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "outage data not yet loaded",
		})
	}
	return c.JSON(regions)
}

func (h *handlers) getGroups(c *fiber.Ctx) error {
	region := c.Params("region")

	rd := h.fetcher.getRegionData(region)
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

	groups := make([]outage.GroupInfo, 0, len(dayData))
	for g := range dayData {
		name := g
		if n, ok := rd.Preset.SchNames[g]; ok {
			name = n
		}
		groups = append(groups, outage.GroupInfo{ID: g, Name: name})
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].ID < groups[j].ID })

	return c.JSON(fiber.Map{
		"region": rd.RegionID,
		"groups": groups,
	})
}

func (h *handlers) getRegionFact(c *fiber.Ctx) error {
	region := c.Params("region")

	rd := h.fetcher.getRegionData(region)
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

	return c.JSON(outage.RegionFactSummary{
		Region:      rd.RegionID,
		LastUpdated: rd.LastUpdated,
		FactUpdate:  rd.Fact.Update,
		Groups:      dayData,
	})
}

func (h *handlers) getGroupFact(c *fiber.Ctx) error {
	region := c.Params("region")
	group := c.Params("group")

	rd := h.fetcher.getRegionData(region)
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

	return c.JSON(outage.GroupHourlyFact{
		Region:      rd.RegionID,
		Group:       group,
		Date:        todayKey,
		LastUpdated: rd.LastUpdated,
		FactUpdate:  rd.Fact.Update,
		Hours:       hours,
	})
}

func (h *handlers) getGroupPhoto(c *fiber.Ctx) error {
	region := c.Params("region")
	group := c.Params("group")
	filename := outage.GroupToFilename(group)

	data, etag, notModified, err := h.fetcher.getPhoto(region, filename, c.Get("If-None-Match"))
	if err != nil {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": err.Error()})
	}
	if notModified {
		return c.SendStatus(fiber.StatusNotModified)
	}

	c.Set("ETag", etag)
	c.Set("Content-Type", "image/png")
	return c.Send(data)
}
