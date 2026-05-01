package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// ── API ──────────────────────────────────────────────────────────────

	// PingTotal counts incoming heartbeat pings.
	// status: ok | paused | not_found
	PingTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "nlm", Name: "ping_total",
		Help: "Total heartbeat pings received by the API.",
	}, []string{"status"})

	// APIRequestDuration records HTTP request latency for /api/* routes.
	// route: Fiber route template (e.g. /api/ping/:token), status: HTTP status code
	APIRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "nlm", Name: "api_request_duration_seconds",
		Help:    "HTTP request duration for API routes.",
		Buckets: prometheus.DefBuckets,
	}, []string{"route", "status"})

	// ── Worker ────────────────────────────────────────────────────────────

	// StatusChangeTotal counts monitor online/offline transitions.
	// transition: online | offline
	StatusChangeTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "nlm", Name: "status_change_total",
		Help: "Total monitor status transitions detected by the worker.",
	}, []string{"transition"})

	// WorkerLastCheckUnix is the Unix timestamp of the last completed heartbeat check cycle.
	// Stops incrementing if the heartbeat loop is stuck or dead.
	WorkerLastCheckUnix = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "nlm", Name: "worker_last_check_unix",
		Help: "Unix timestamp of the last completed heartbeat check cycle.",
	})

	// ActiveMonitors is the number of monitors currently loaded in worker memory.
	ActiveMonitors = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "nlm", Name: "active_monitors",
		Help: "Number of monitors loaded in the worker's in-memory map.",
	})

	// MQPublishErrors counts failed RabbitMQ publish attempts.
	// routing_key: the target routing key that failed
	MQPublishErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "nlm", Name: "mq_publish_errors_total",
		Help: "Total failed RabbitMQ publish attempts.",
	}, []string{"routing_key"})

	// ── Bot ───────────────────────────────────────────────────────────────

	// BotMessagesProcessed counts messages consumed from RabbitMQ by the bot listener.
	// msg_type: status_change | graph | outage_photo | dtek_outage | inactive_pause | broadcast
	BotMessagesProcessed = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "nlm", Name: "bot_messages_processed_total",
		Help: "Total messages processed by the bot listener.",
	}, []string{"msg_type"})

	// BotNotificationErrors counts Telegram send/edit errors in the bot listener.
	// msg_type: same label values as BotMessagesProcessed
	BotNotificationErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "nlm", Name: "bot_notification_errors_total",
		Help: "Total Telegram notification errors in the bot listener.",
	}, []string{"msg_type"})
)
