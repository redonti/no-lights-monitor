package graph

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"no-lights-monitor/internal/models"
)

// Client talks to the external graph-generation service.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a new graph service client.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// weekGraphRequest is the JSON body for POST /generate-week-graph.
type weekGraphRequest struct {
	MonitorID int64                `json:"monitor_id"`
	WeekStart time.Time            `json:"week_start"`
	Events    []models.StatusEvent `json:"events"`
}

// GenerateWeekGraph calls the graph service and returns raw PNG bytes.
func (c *Client) GenerateWeekGraph(monitorID int64, weekStart time.Time, events []*models.StatusEvent) ([]byte, error) {
	// Convert pointer slice to value slice for JSON.
	evts := make([]models.StatusEvent, len(events))
	for i, e := range events {
		evts[i] = *e
	}

	body, err := json.Marshal(weekGraphRequest{
		MonitorID: monitorID,
		WeekStart: weekStart,
		Events:    evts,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	resp, err := c.httpClient.Post(
		c.baseURL+"/generate-week-graph",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("http post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("graph service returned %d: %s", resp.StatusCode, string(errBody))
	}

	png, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	return png, nil
}
