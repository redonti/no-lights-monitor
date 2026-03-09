package geocode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Result holds a geocoding result.
type Result struct {
	DisplayName string
	Latitude    float64
	Longitude   float64
}

type nominatimResult struct {
	Lat     string          `json:"lat"`
	Lon     string          `json:"lon"`
	Display string          `json:"display_name"`
	Address nominatimAddr   `json:"address"`
}

type nominatimAddr struct {
	HouseNumber  string `json:"house_number"`
	Road         string `json:"road"`
	Suburb       string `json:"suburb"`
	CityDistrict string `json:"city_district"`
	City         string `json:"city"`
	Town         string `json:"town"`
	Village      string `json:"village"`
	State        string `json:"state"`
	Country      string `json:"country"`
}

// Search queries Nominatim for the given address string.
// Returns nil (no error) if nothing was found.
func Search(ctx context.Context, query string) (*Result, error) {
	u := fmt.Sprintf(
		"https://nominatim.openstreetmap.org/search?q=%s&format=json&limit=1&addressdetails=1&accept-language=uk",
		url.QueryEscape(query),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "no-lights-monitor/1.0")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("nominatim request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nominatim returned status %d", resp.StatusCode)
	}

	var results []nominatimResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, fmt.Errorf("decode nominatim response: %w", err)
	}

	if len(results) == 0 {
		return nil, nil
	}

	r := results[0]

	lat, err := strconv.ParseFloat(r.Lat, 64)
	if err != nil {
		return nil, fmt.Errorf("parse lat: %w", err)
	}
	lon, err := strconv.ParseFloat(r.Lon, 64)
	if err != nil {
		return nil, fmt.Errorf("parse lon: %w", err)
	}

	return &Result{
		DisplayName: formatAddress(r.Address),
		Latitude:    lat,
		Longitude:   lon,
	}, nil
}

// formatAddress builds a clean human-readable address from structured fields.
func formatAddress(a nominatimAddr) string {
	// Pick the settlement name: city > town > village.
	city := a.City
	if city == "" {
		city = a.Town
	}
	if city == "" {
		city = a.Village
	}

	var parts []string

	// Street + house number.
	if a.Road != "" {
		street := a.Road
		if a.HouseNumber != "" {
			street += ", " + a.HouseNumber
		}
		parts = append(parts, street)
	}

	// District (if different from city and not empty).
	if a.CityDistrict != "" && a.CityDistrict != city {
		parts = append(parts, a.CityDistrict)
	}

	// City / town / village.
	if city != "" {
		parts = append(parts, city)
	}

	// Country.
	if a.Country != "" {
		parts = append(parts, a.Country)
	}

	if len(parts) == 0 {
		return "—"
	}

	return strings.Join(parts, ", ")
}
