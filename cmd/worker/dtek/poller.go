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
	"no-lights-monitor/internal/models"
	"no-lights-monitor/internal/mq"
)

// recheckSnooze is how far ahead to set dtek_outage_recheck_at when the outage end
// time is unavailable or unparseable, preventing immediate re-polling.
const recheckSnooze = 1 * time.Hour

// Poller periodically checks DTEK for unplanned outages on monitors that are
// currently offline and have DTEK monitoring enabled. On first detection it
// sends a notification; when the stored recheck time passes it re-checks and
// edits the existing message if the outage details changed.
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
		if err := p.check(ctx, m); err != nil {
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

// isUpdateCheck returns true if the monitor was already notified for this offline period
// (i.e. this poll is a re-check for an extended/changed outage, not a first detection).
func isUpdateCheck(m *models.Monitor) bool {
	return m.DtekOutageNotifiedAt != nil && !m.DtekOutageNotifiedAt.Before(m.LastStatusChangeAt)
}

// parseRecheckAt parses the DTEK end_date string (format: "15:04 02.01.2006")
// and returns it as a time.Time. Returns now+recheckSnooze and logs an error if unparseable.
func parseRecheckAt(endDate string) time.Time {
	if endDate != "" {
		if t, err := time.ParseInLocation("15:04 02.01.2006", endDate, time.Local); err == nil {
			return t
		}
		log.Printf("[dtek] failed to parse end_date %q", endDate)
	}
	return time.Now().Add(recheckSnooze)
}

func (p *Poller) check(ctx context.Context, m *models.Monitor) error {
	q := url.Values{}
	q.Set("region", m.DtekRegion)
	if m.DtekCity != "" {
		q.Set("city", m.DtekCity)
	}
	q.Set("street", m.DtekStreet)
	q.Set("house", m.DtekHouse)

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

	isUpdate := isUpdateCheck(m)

	if !result.IsOutage {
		log.Printf("[dtek] monitor %d: no outage found at DTEK", m.ID)
		if isUpdate {
			// Snooze the re-check so we don't hammer DTEK on every poll.
			snooze := time.Now().Add(recheckSnooze)
			if err := p.db.UpdateDtekOutageRecheck(ctx, m.ID, snooze); err != nil {
				log.Printf("[dtek] monitor %d: failed to snooze recheck: %v", m.ID, err)
			}
		}
		return nil
	}

	ownerID, err := p.db.GetOwnerTelegramIDByMonitorID(ctx, m.ID)
	if err != nil {
		log.Printf("[dtek] monitor %d: failed to get owner: %v", m.ID, err)
	}

	recheckAt := parseRecheckAt(result.Data.EndDate)

	if !isUpdate {
		// First detection — save state and send a new message.
		now := time.Now()
		if err := p.db.SaveDtekOutageDetected(ctx, m.ID, now, recheckAt); err != nil {
			log.Printf("[dtek] monitor %d: failed to save detected state: %v", m.ID, err)
		}

		msg := mq.DtekOutageMsg{
			Action:          mq.DtekOutageSend,
			MonitorID:       m.ID,
			ChannelID:       m.ChannelID,
			OwnerTelegramID: ownerID,
			MonitorName:     m.Name,
			SubType:         result.Data.SubType,
			StartDate:       result.Data.StartDate,
			EndDate:         result.Data.EndDate,
		}
		if err := p.publisher.Publish(ctx, mq.RoutingDtekOutage, msg); err != nil {
			log.Printf("[dtek] monitor %d: failed to publish outage alert: %v", m.ID, err)
			return err
		}
		log.Printf("[dtek] monitor %d (%s): outage confirmed, notification published", m.ID, m.Name)
		return nil
	}

	// Re-check after recheck_at passed — advance the recheck time and publish an update.
	if err := p.db.UpdateDtekOutageRecheck(ctx, m.ID, recheckAt); err != nil {
		log.Printf("[dtek] monitor %d: failed to update recheck time: %v", m.ID, err)
	}

	msg := mq.DtekOutageMsg{
		Action:          mq.DtekOutageUpdate,
		OldMsgID:        m.DtekOutageMessageID,
		MonitorID:       m.ID,
		ChannelID:       m.ChannelID,
		OwnerTelegramID: ownerID,
		MonitorName:     m.Name,
		SubType:         result.Data.SubType,
		StartDate:       result.Data.StartDate,
		EndDate:         result.Data.EndDate,
	}
	if err := p.publisher.Publish(ctx, mq.RoutingDtekOutage, msg); err != nil {
		log.Printf("[dtek] monitor %d: failed to publish outage update: %v", m.ID, err)
		return err
	}
	log.Printf("[dtek] monitor %d (%s): outage update published (recheck at %s)", m.ID, m.Name, recheckAt.Format(time.RFC3339))
	return nil
}
