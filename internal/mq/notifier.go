package mq

import (
	"context"
	"log"
	"time"
)

// StatusNotifier implements heartbeat.Notifier by publishing to RabbitMQ.
type StatusNotifier struct {
	pub *Publisher
}

// NewStatusNotifier creates a notifier that publishes status changes to RabbitMQ.
func NewStatusNotifier(pub *Publisher) *StatusNotifier {
	return &StatusNotifier{pub: pub}
}

// NotifyStatusChange publishes a status change message to the queue.
func (n *StatusNotifier) NotifyStatusChange(monitorID, channelID int64, name, address string, notifyAddress, isOnline bool, duration time.Duration, when time.Time, outageRegion, outageGroup string, notifyOutage bool) {
	msg := StatusChangeMsg{
		MonitorID:     monitorID,
		ChannelID:     channelID,
		Name:          name,
		Address:       address,
		NotifyAddress: notifyAddress,
		IsOnline:      isOnline,
		DurationSec:   duration.Seconds(),
		When:          when,
		OutageRegion:  outageRegion,
		OutageGroup:   outageGroup,
		NotifyOutage:  notifyOutage,
	}
	if err := n.pub.Publish(context.Background(), RoutingStatusChange, msg); err != nil {
		log.Printf("[mq] failed to publish status change for monitor %d: %v", monitorID, err)
	}
}
