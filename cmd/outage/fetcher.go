package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"no-lights-monitor/internal/outage"
)

const (
	rawBaseURL   = "https://raw.githubusercontent.com/Baskerville42/outage-data-ua/main/data"
	rawImagesURL = "https://raw.githubusercontent.com/Baskerville42/outage-data-ua/refs/heads/main/images"
)

var supportedRegions = []string{"kyiv", "kyiv-region", "odesa", "dnipro"}

// Fetcher periodically fetches outage data from GitHub and stores it in memory.
type Fetcher struct {
	client   *http.Client
	interval time.Duration

	mu   sync.RWMutex
	data map[string]*outage.RegionData // keyed by regionId
}

func newFetcher(intervalSec int) *Fetcher {
	return &Fetcher{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		interval: time.Duration(intervalSec) * time.Second,
		data:     make(map[string]*outage.RegionData),
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
	log.Printf("[outage] fetching data for %d regions...", len(supportedRegions))
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

	var rd outage.RegionData
	if err := json.Unmarshal(body, &rd); err != nil {
		return fmt.Errorf("unmarshal %s: %w", region, err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	// Skip if data hasn't changed.
	if existing, ok := f.data[region]; ok && existing.LastUpdated == rd.LastUpdated {
		log.Printf("[outage] %s unchanged (lastUpdated: %s, factUpdate: %s, today: %d)",
			region, rd.LastUpdated, rd.Fact.Update, rd.Fact.Today)
		return nil
	}

	f.data[region] = &rd
	log.Printf("[outage] updated %s (lastUpdated: %s, factUpdate: %s, today: %d)",
		region, rd.LastUpdated, rd.Fact.Update, rd.Fact.Today)
	return nil
}

func (f *Fetcher) getRegionData(region string) *outage.RegionData {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.data[region]
}

func (f *Fetcher) getAllRegions() []outage.RegionInfo {
	f.mu.RLock()
	defer f.mu.RUnlock()

	result := make([]outage.RegionInfo, 0, len(f.data))
	for _, rd := range f.data {
		result = append(result, outage.RegionInfo{
			RegionID:    rd.RegionID,
			LastUpdated: rd.LastUpdated,
		})
	}
	return result
}

// getPhoto proxies a photo request to GitHub, forwarding the If-None-Match header.
// Returns (nil, "", true, nil) when the image is unchanged (304 Not Modified).
func (f *Fetcher) getPhoto(region, filename, ifNoneMatch string) (data []byte, etag string, notModified bool, err error) {
	imageURL := fmt.Sprintf("%s/%s/%s", rawImagesURL, region, filename)

	req, err := http.NewRequest(http.MethodGet, imageURL, nil)
	if err != nil {
		return nil, "", false, fmt.Errorf("build request: %w", err)
	}
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, "", false, fmt.Errorf("GET %s: %w", imageURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return nil, "", true, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, "", false, fmt.Errorf("GET %s: status %d", imageURL, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", false, fmt.Errorf("read body: %w", err)
	}

	return body, resp.Header.Get("ETag"), false, nil
}
