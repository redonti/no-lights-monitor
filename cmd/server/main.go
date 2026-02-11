package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/joho/godotenv"

	"no-lights-monitor/internal/bot"
	"no-lights-monitor/internal/cache"
	"no-lights-monitor/internal/config"
	"no-lights-monitor/internal/database"
	"no-lights-monitor/internal/graph"
	"no-lights-monitor/internal/handlers"
	"no-lights-monitor/internal/heartbeat"
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

	// Wire up the notifier now that the bot exists.
	notifier := bot.NewNotifier(tgBot.TeleBot())
	hbService.SetNotifier(notifier)

	go tgBot.Start()
	defer tgBot.Stop()
	log.Println("telegram bot started")

	// --- Start heartbeat checker ---
	go hbService.StartChecker(ctx, 30)

	// --- Graph updater (hourly) ---
	graphClient := graph.NewClient(cfg.GraphServiceURL)
	graphUpdater := graph.NewUpdater(db, graphClient, tgBot.TeleBot())
	tgBot.SetGraphUpdater(graphUpdater)
	go graphUpdater.Start(ctx)
	log.Println("graph updater started")

	// --- Fiber HTTP Server ---
	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
	})

	app.Use(logger.New(logger.Config{
		Format: "${time} ${status} ${method} ${path} ${latency}\n",
	}))
	app.Use(cors.New())

	// API routes
	h := &handlers.Handlers{DB: db, HeartbeatSvc: hbService}
	api := app.Group("/api")
	api.Get("/ping/:token", h.Ping)
	api.Get("/monitors", h.GetMonitors)
	api.Get("/monitors/:id/history", h.GetHistory)
	api.Get("/stats", h.GetStats)

	// Serve static frontend files
	app.Static("/", "./web")

	// --- Graceful shutdown ---
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit
		log.Println("shutting down...")
		cancel()
		_ = app.Shutdown()
	}()

	log.Printf("server starting on :%s", cfg.Port)
	if err := app.Listen(":" + cfg.Port); err != nil {
		log.Fatalf("server: %v", err)
	}
}
