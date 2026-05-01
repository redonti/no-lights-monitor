package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// PingTotal counts incoming heartbeat pings to the API.
	// Labels: status = ok | paused | not_found
	PingTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "nlm",
		Name:      "ping_total",
		Help:      "Total heartbeat pings received by the API.",
	}, []string{"status"})

	// StatusChangeTotal counts monitor online/offline transitions detected by the worker.
	// Labels: transition = online | offline
	StatusChangeTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "nlm",
		Name:      "status_change_total",
		Help:      "Total monitor status transitions detected by the worker.",
	}, []string{"transition"})
)
