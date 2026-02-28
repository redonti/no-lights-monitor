package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"

	"no-lights-monitor/internal/cache"
	"no-lights-monitor/internal/config"
	"no-lights-monitor/internal/database"
	"no-lights-monitor/internal/graph"
	"no-lights-monitor/internal/heartbeat"
	"no-lights-monitor/internal/mq"
	"no-lights-monitor/internal/outagephoto"
)

const (
	// HeartbeatCheckIntervalSec is how often we check for stale heartbeats.
	HeartbeatCheckIntervalSec = 15
	// PingCheckIntervalSec is how often we ICMP-ping targets for ping monitors.
	PingCheckIntervalSec = 60
)

func main() {
	// Load .env if present.
	_ = godotenv.Load()

	cfg := config.Load()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// --- Database ---
	db, err := database.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer db.Close()

	if err := db.Migrate(ctx); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	log.Println("database connected and migrated")

	// --- Redis ---
	redisCache, err := cache.New(cfg.RedisURL)
	if err != nil {
		log.Fatalf("redis: %v", err)
	}
	defer redisCache.Close()
	log.Println("redis connected")

	// --- RabbitMQ ---
	publisher, err := mq.NewPublisher(cfg.RabbitMQURL)
	if err != nil {
		log.Fatalf("rabbitmq publisher: %v", err)
	}
	defer publisher.Close()

	consumer, err := mq.NewConsumer(cfg.RabbitMQURL)
	if err != nil {
		log.Fatalf("rabbitmq consumer: %v", err)
	}
	defer consumer.Close()
	log.Println("rabbitmq connected")

	// --- Heartbeat Service ---
	notifier := mq.NewStatusNotifier(publisher)
	hbService := heartbeat.NewService(db, redisCache, notifier, cfg.OfflineThreshold)

	if err := hbService.LoadMonitors(ctx); err != nil {
		log.Fatalf("load monitors: %v", err)
	}

	// --- Start heartbeat and ping checkers ---
	go hbService.StartHeartbeatChecker(ctx, HeartbeatCheckIntervalSec)
	go hbService.StartPingChecker(ctx, PingCheckIntervalSec)

	// --- Uptime Graph updater (hourly) ---
	graphClient := graph.NewClient(cfg.GraphServiceURL)
	graphUpdater := graph.NewUpdater(db, graphClient, publisher)
	go graphUpdater.Start(ctx, consumer)
	log.Println("graph updater started")

	// --- Outage photo updater (hourly) ---
	photoUpdater := outagephoto.NewUpdater(db, publisher)
	go photoUpdater.Start(ctx)
	log.Println("outage photo updater started")

	// --- Graceful shutdown ---
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down worker...")
	cancel()
}
