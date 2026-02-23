package outage

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client talks to the outage data service.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a new outage service client.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// GetGroupFact fetches the hourly fact status for a group in a region.
func (c *Client) GetGroupFact(region, group string) (*GroupHourlyFact, error) {
	url := fmt.Sprintf("%s/api/outage/%s/%s", c.baseURL, region, group)
	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("outage service returned %d: %s", resp.StatusCode, string(body))
	}

	var result GroupHourlyFact
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &result, nil
}

// GroupsResponse is the response from the /groups endpoint.
type GroupsResponse struct {
	Region string      `json:"region"`
	Groups []GroupInfo  `json:"groups"`
}

// GetGroups fetches the list of available groups for a region.
func (c *Client) GetGroups(region string) ([]GroupInfo, error) {
	url := fmt.Sprintf("%s/api/outage/%s/groups", c.baseURL, region)
	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("outage service returned %d: %s", resp.StatusCode, string(body))
	}

	var result GroupsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return result.Groups, nil
}

// RegionsResponse wraps the regions list response.
type RegionsResponse []RegionInfo

// GetRegions fetches the list of available regions.
func (c *Client) GetRegions() ([]RegionInfo, error) {
	url := fmt.Sprintf("%s/api/outage/regions", c.baseURL)
	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("outage service returned %d: %s", resp.StatusCode, string(body))
	}

	var result []RegionInfo
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return result, nil
}
