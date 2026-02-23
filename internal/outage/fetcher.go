package outage

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

const (
	rawBaseURL = "https://raw.githubusercontent.com/Baskerville42/outage-data-ua/main/data"
)

var supportedRegions = []string{"kyiv", "kyiv-region", "odesa", "dnipro"}

// Fetcher periodically fetches outage data from GitHub and stores it in memory.
type Fetcher struct {
	client   *http.Client
	interval time.Duration

	mu   sync.RWMutex
	data map[string]*RegionData // keyed by regionId
}

// NewFetcher creates a new Fetcher with the given fetch interval.
func NewFetcher(intervalSec int) *Fetcher {
	return &Fetcher{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		interval: time.Duration(intervalSec) * time.Second,
		data:     make(map[string]*RegionData),
	}
}

// Start begins periodic fetching. It performs an initial fetch immediately,
// then fetches every interval. Blocks until ctx is cancelled.
func (f *Fetcher) Start(ctx context.Context) {
	f.fetchAll()

	ticker := time.NewTicker(f.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			f.fetchAll()
		}
	}
}

func (f *Fetcher) fetchAll() {
	for _, region := range supportedRegions {
		if err := f.fetchRegion(region); err != nil {
			log.Printf("[outage] failed to fetch %s: %v", region, err)
		}
	}
}

func (f *Fetcher) fetchRegion(region string) error {
	url := fmt.Sprintf("%s/%s.json", rawBaseURL, region)

	resp, err := f.client.Get(url)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	var rd RegionData
	if err := json.Unmarshal(body, &rd); err != nil {
		return fmt.Errorf("unmarshal %s: %w", region, err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	// Skip if data hasn't changed.
	if existing, ok := f.data[region]; ok && existing.LastUpdated == rd.LastUpdated {
		return nil
	}

	f.data[region] = &rd
	log.Printf("[outage] updated %s (lastUpdated: %s)", region, rd.LastUpdated)
	return nil
}

// GetRegionData returns a copy of the region data. Returns nil if not loaded.
func (f *Fetcher) GetRegionData(region string) *RegionData {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.data[region]
}

// GetAllRegions returns info about all loaded regions.
func (f *Fetcher) GetAllRegions() []RegionInfo {
	f.mu.RLock()
	defer f.mu.RUnlock()

	result := make([]RegionInfo, 0, len(f.data))
	for _, rd := range f.data {
		result = append(result, RegionInfo{
			RegionID:    rd.RegionID,
			LastUpdated: rd.LastUpdated,
		})
	}
	return result
}
