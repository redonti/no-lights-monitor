package outage

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
)

// reLetterDigit matches the boundary between letters and digits.
var reLetterDigit = regexp.MustCompile(`([a-z])(\d)`)

// GroupToFilename converts a group ID like "GPV1.1" to "gpv-1-1-emergency.png".
func GroupToFilename(group string) string {
	s := strings.ToLower(group)
	s = reLetterDigit.ReplaceAllString(s, "${1}-${2}")
	s = strings.ReplaceAll(s, ".", "-")
	return s + "-emergency.png"
}

// RegionData is the top-level JSON structure from the outage-data-ua repo.
type RegionData struct {
	RegionID    string `json:"regionId"`
	LastUpdated string `json:"lastUpdated"`
	Fact        Fact   `json:"fact"`
	Preset      Preset `json:"preset"`
}

// Preset contains the weekly schedule metadata (we only use sch_names for display names).
type Preset struct {
	SchNames map[string]string `json:"sch_names"`
}

// Fact contains actual/emergency outage data for today.
type Fact struct {
	// Data is keyed by unix timestamp string, then group ID, then hour (1-24).
	// Values: "yes" (power on), "no" (power off), "first" (off first 30min), "second" (off second 30min).
	// Never nil after unmarshalling; empty when the source reports no outages.
	Data   map[string]map[string]map[string]string `json:"data"`
	Update string                                   `json:"update"`
	Today  int64                                    `json:"today"`
}

// UnmarshalJSON decodes a Fact, tolerating the two shapes the source uses for
// "data": an object keyed by unix timestamp when outages exist, and an empty
// JSON array when there are none. The array form is decoded as an empty map so
// a quiet day does not fail the whole region.
func (f *Fact) UnmarshalJSON(b []byte) error {
	var raw struct {
		Data   json.RawMessage `json:"data"`
		Update string          `json:"update"`
		Today  int64           `json:"today"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}

	f.Update = raw.Update
	f.Today = raw.Today
	f.Data = make(map[string]map[string]map[string]string)

	trimmed := bytes.TrimSpace(raw.Data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || trimmed[0] == '[' {
		return nil
	}
	return json.Unmarshal(trimmed, &f.Data)
}

// GroupHourlyFact is the API response for a group's hourly fact status.
type GroupHourlyFact struct {
	Region      string            `json:"region"`
	Group       string            `json:"group"`
	Date        string            `json:"date"`
	LastUpdated string            `json:"last_updated"`
	FactUpdate  string            `json:"fact_update"`
	Hours       map[string]string `json:"hours"`
}

// RegionFactSummary is the API response for all groups' current status in a region.
type RegionFactSummary struct {
	Region      string                    `json:"region"`
	LastUpdated string                    `json:"last_updated"`
	FactUpdate  string                    `json:"fact_update"`
	Groups      map[string]map[string]string `json:"groups"`
}

// RegionInfo is a short summary of a region for the regions list endpoint.
type RegionInfo struct {
	RegionID    string `json:"region_id"`
	LastUpdated string `json:"last_updated"`
}

// GroupInfo is an entry in the groups list with ID and human-readable name.
type GroupInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
