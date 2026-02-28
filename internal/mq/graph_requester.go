package mq

import "context"

// GraphRequester implements bot.GraphUpdater by publishing to RabbitMQ.
type GraphRequester struct {
	pub *Publisher
}

// NewGraphRequester creates a requester that publishes graph requests to RabbitMQ.
func NewGraphRequester(pub *Publisher) *GraphRequester {
	return &GraphRequester{pub: pub}
}

// UpdateSingle publishes a request to generate a graph for a single monitor.
func (r *GraphRequester) UpdateSingle(ctx context.Context, monitorID, channelID int64) error {
	return r.pub.Publish(ctx, RoutingGraphRequest, GraphRequestMsg{
		MonitorID: monitorID,
		ChannelID: channelID,
	})
}
