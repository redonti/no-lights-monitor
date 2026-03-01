package dtek

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"time"

	"no-lights-monitor/internal/database"
	"no-lights-monitor/internal/mq"
)

// Poller periodically checks DTEK for unplanned outages on monitors that are
// currently offline and have DTEK monitoring enabled. When an outage is
// confirmed, it publishes a DtekOutageMsg and marks the monitor as notified so
// subsequent polls within the same offline period don't spam.
type Poller struct {
	db         *database.DB
	publisher  *mq.Publisher
	serviceURL string
	client     *http.Client
}

func NewPoller(db *database.DB, publisher *mq.Publisher, serviceURL string) *Poller {
	return &Poller{
		db:         db,
		publisher:  publisher,
		serviceURL: serviceURL,
		client:     &http.Client{Timeout: 35 * time.Second},
	}
}

// Start runs the polling loop. intervalSec controls how often it fires.
func (p *Poller) Start(ctx context.Context, intervalSec int) {
	ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
	defer ticker.Stop()

	// Run once immediately on start, then on each tick.
	p.run(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.run(ctx)
		}
	}
}

func (p *Poller) run(ctx context.Context) {
	monitors, err := p.db.GetDtekPendingMonitors(ctx)
	if err != nil {
		log.Printf("[dtek] failed to query pending monitors: %v", err)
		return
	}
	for _, m := range monitors {
		if err := p.check(ctx, m.ID, m.ChannelID, m.Name, m.DtekRegion, m.DtekCity, m.DtekStreet, m.DtekHouse); err != nil {
			log.Printf("[dtek] monitor %d check error: %v", m.ID, err)
		}
	}
}

type outageResponse struct {
	IsOutage bool `json:"isOutage"`
	Data     struct {
		SubType   string `json:"sub_type"`
		StartDate string `json:"start_date"`
		EndDate   string `json:"end_date"`
	} `json:"data"`
}

func (p *Poller) check(ctx context.Context, monitorID, channelID int64, name, region, city, street, house string) error {
	q := url.Values{}
	q.Set("region", region)
	if city != "" {
		q.Set("city", city)
	}
	q.Set("street", street)
	q.Set("house", house)

	reqURL := fmt.Sprintf("%s/outage?%s", p.serviceURL, q.Encode())
	resp, err := p.client.Get(reqURL)
	if err != nil {
		return fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("dtek service returned HTTP %d", resp.StatusCode)
	}

	var result outageResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if !result.IsOutage {
		log.Printf("[dtek] monitor %d: no outage found at DTEK", monitorID)
		return nil
	}

	// Outage confirmed — mark as notified so we don't send again this offline period.
	now := time.Now()
	if err := p.db.SetMonitorDtekOutageNotifiedAt(ctx, monitorID, now); err != nil {
		log.Printf("[dtek] monitor %d: failed to set notified_at: %v", monitorID, err)
	}

	ownerID, err := p.db.GetOwnerTelegramIDByMonitorID(ctx, monitorID)
	if err != nil {
		log.Printf("[dtek] monitor %d: failed to get owner: %v", monitorID, err)
	}

	msg := mq.DtekOutageMsg{
		MonitorID:       monitorID,
		ChannelID:       channelID,
		OwnerTelegramID: ownerID,
		MonitorName:     name,
		SubType:         result.Data.SubType,
		StartDate:       result.Data.StartDate,
		EndDate:         result.Data.EndDate,
	}
	if err := p.publisher.Publish(ctx, mq.RoutingDtekOutage, msg); err != nil {
		log.Printf("[dtek] monitor %d: failed to publish outage alert: %v", monitorID, err)
		return err
	}

	log.Printf("[dtek] monitor %d (%s): outage confirmed, notification published", monitorID, name)
	return nil
}
