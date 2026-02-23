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

	"no-lights-monitor/internal/config"
	"no-lights-monitor/internal/outage"
)

func main() {
	_ = godotenv.Load()

	cfg := config.Load()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// --- Outage data fetcher ---
	fetcher := outage.NewFetcher(cfg.OutageFetchInterval)
	go fetcher.Start(ctx)
	log.Printf("outage fetcher started (interval: %ds)", cfg.OutageFetchInterval)

	// --- Fiber HTTP Server ---
	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
	})

	app.Use(logger.New(logger.Config{
		Format: "${time} ${status} ${method} ${path} ${latency}\n",
	}))
	app.Use(cors.New())

	// Outage API routes
	api := app.Group("/api")
	h := &outage.Handlers{Fetcher: fetcher}
	h.RegisterRoutes(api)

	// --- Graceful shutdown ---
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit
		log.Println("shutting down...")
		cancel()
		_ = app.Shutdown()
	}()

	port := getEnv("OUTAGE_PORT", "8090")
	log.Printf("outage service starting on :%s", port)
	if err := app.Listen(":" + port); err != nil {
		log.Fatalf("server: %v", err)
	}
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}
