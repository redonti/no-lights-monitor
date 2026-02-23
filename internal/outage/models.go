package outage

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
	Data   map[string]map[string]map[string]string `json:"data"`
	Update string                                   `json:"update"`
	Today  int64                                    `json:"today"`
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
