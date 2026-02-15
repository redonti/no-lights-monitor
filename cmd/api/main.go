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

	"no-lights-monitor/internal/cache"
	"no-lights-monitor/internal/config"
	"no-lights-monitor/internal/database"
	"no-lights-monitor/internal/handlers"
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

	// --- Fiber HTTP Server ---
	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
	})

	app.Use(logger.New(logger.Config{
		Format: "${time} ${status} ${method} ${path} ${latency}\n",
	}))
	app.Use(cors.New())

	// API routes
	h := &handlers.Handlers{DB: db, Cache: redisCache}
	api := app.Group("/api")
	api.Get("/ping/:token", h.PingAPI)
	api.Get("/monitors", h.GetMonitors)
	api.Get("/monitors/:id/history", h.GetHistory)

	// Admin routes (protected by HTTP Basic Auth)
	if cfg.AdminLogin != "" && cfg.AdminPassword != "" {
		admin := app.Group("/admin", handlers.BasicAuth(cfg.AdminLogin, cfg.AdminPassword))
		admin.Get("/", h.AdminPage)
		admin.Get("/api/users", h.AdminGetUsers)
		admin.Get("/api/monitors", h.AdminGetMonitors)
	}

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

	log.Printf("API service starting on :%s", cfg.Port)
	if err := app.Listen(":" + cfg.Port); err != nil {
		log.Fatalf("server: %v", err)
	}
}
