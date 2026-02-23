package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"

	"no-lights-monitor/internal/bot"
	"no-lights-monitor/internal/cache"
	"no-lights-monitor/internal/config"
	"no-lights-monitor/internal/database"
	"no-lights-monitor/internal/graph"
	"no-lights-monitor/internal/heartbeat"
	"no-lights-monitor/internal/outage"
	"no-lights-monitor/internal/outagephoto"
)

const (
	// HeartbeatCheckIntervalSec is how often we check for stale heartbeats.
	HeartbeatCheckIntervalSec = 30
)

func main() {
	// Load .env if present.
	_ = godotenv.Load()

	cfg := config.Load()

	if cfg.BotToken == "" {
		log.Fatal("BOT_TOKEN is required. Get one from @BotFather on Telegram.")
	}

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

	// --- Heartbeat Service ---
	hbService := heartbeat.NewService(db, redisCache, nil, cfg.OfflineThreshold)

	if err := hbService.LoadMonitors(ctx); err != nil {
		log.Fatalf("load monitors: %v", err)
	}

	// --- Telegram Bot ---
	tgBot, err := bot.New(cfg.BotToken, db, hbService, cfg.BaseURL)
	if err != nil {
		log.Fatalf("bot: %v", err)
	}

	// --- Outage Client ---
	outageClient := outage.NewClient(cfg.OutageServiceURL)
	tgBot.SetOutageClient(outageClient)

	// Wire up the notifier now that the bot exists.
	notifier := bot.NewNotifier(tgBot.TeleBot(), db, outageClient)
	hbService.SetNotifier(notifier)

	go tgBot.Start()
	defer tgBot.Stop()
	log.Println("telegram bot started")

	// --- Start heartbeat checker ---
	go hbService.StartChecker(ctx, HeartbeatCheckIntervalSec)

	// --- Graph updater (hourly) ---
	graphClient := graph.NewClient(cfg.GraphServiceURL)
	graphUpdater := graph.NewUpdater(db, graphClient, tgBot.TeleBot())
	tgBot.SetGraphUpdater(graphUpdater)
	go graphUpdater.Start(ctx)
	log.Println("graph updater started")

	// --- Outage photo updater (hourly) ---
	photoUpdater := outagephoto.NewUpdater(db, tgBot.TeleBot())
	go photoUpdater.Start(ctx)
	log.Println("outage photo updater started")

	// --- Graceful shutdown ---
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down worker...")
	cancel()
}
