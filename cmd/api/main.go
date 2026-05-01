package main

import (
	"bytes"
	"context"
	"html/template"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/joho/godotenv"

	"no-lights-monitor/cmd/api/handlers"
	"no-lights-monitor/internal/cache"
	"no-lights-monitor/internal/config"
	"no-lights-monitor/internal/database"
	"no-lights-monitor/internal/mq"
)

func main() {
	// Load .env if present.
	_ = godotenv.Load()

	cfg := config.Load()

	// Pre-render HTML pages that need config values injected (values are static after startup).
	type webVars struct{ BotUsername, ChatUsername string }
	webCfg := webVars{cfg.TelegramBotUsername, cfg.TelegramChatUsername}
	renderOnce := func(file string) []byte {
		var buf bytes.Buffer
		template.Must(template.ParseFiles(file)).Execute(&buf, webCfg)
		return buf.Bytes()
	}
	indexHTML := renderOnce("./web/index.html")
	notFoundHTML := renderOnce("./web/404.html")
	serveHTML := func(body []byte, status int) fiber.Handler {
		return func(c *fiber.Ctx) error {
			c.Set("Content-Type", "text/html; charset=utf-8")
			c.Set("Cache-Control", "no-cache, must-revalidate")
			return c.Status(status).Send(body)
		}
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

	// --- RabbitMQ ---
	mqPub, err := mq.NewPublisher(cfg.RabbitMQURL)
	if err != nil {
		log.Fatalf("rabbitmq: %v", err)
	}
	defer mqPub.Close()
	log.Println("rabbitmq connected")

	// --- Fiber HTTP Server ---
	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
		BodyLimit:             64 * 1024, // 64KB — settings JSON has no business being larger
	})

	app.Use(logger.New(logger.Config{
		Format: "${time} ${status} ${method} ${path} ${latency}\n",
	}))
	app.Use(cors.New())

	// API routes
	h := &handlers.Handlers{DB: db, Cache: redisCache, OutageServiceURL: cfg.OutageServiceURL, DtekServiceURL: cfg.DtekServiceURL, MQPublisher: mqPub}
	api := app.Group("/api")
	api.Get("/ping/:token", h.PingAPI)
	api.Get("/monitors", h.GetMonitors)

	// Proxy outage API from the outage service (for settings page)
	api.Get("/outage/*", h.ProxyOutage)

	// Proxy DTEK scraper (address autocomplete for settings page)
	api.Get("/dtek/*", h.ProxyDtek)

	// Settings API (accessed by settings_token)
	api.Get("/settings/:token", h.GetSettings)
	api.Put("/settings/:token", h.UpdateSettings)
	api.Post("/settings/:token/stop", h.StopMonitor)
	api.Post("/settings/:token/resume", h.ResumeMonitor)
	api.Delete("/settings/:token", h.DeleteMonitorWeb)

	// Admin routes (protected by HTTP Basic Auth)
	if cfg.AdminLogin != "" && cfg.AdminPassword != "" {
		admin := app.Group("/admin", handlers.BasicAuth(cfg.AdminLogin, cfg.AdminPassword))
		admin.Get("/", h.AdminPage)
		admin.Get("/api/settings", h.AdminGetSettings)
		admin.Put("/api/settings", h.AdminSetSettings)
		admin.Get("/api/users", h.AdminGetUsers)
		admin.Get("/api/monitors", h.AdminGetMonitors)
		admin.Get("/api/monitors/deleted", h.AdminGetDeletedMonitors)
		admin.Get("/api/monitors/:id/history", h.GetHistory)
		admin.Post("/api/broadcast", h.AdminBroadcast)
	}

	// Settings page (serve settings.html for any /settings/* path).
	app.Get("/settings/:token", func(c *fiber.Ctx) error {
		c.Set("Cache-Control", "no-cache, must-revalidate")
		return c.SendFile("./web/settings.html")
	})

	// Index page: pre-rendered with config values injected.
	app.Get("/", serveHTML(indexHTML, fiber.StatusOK))
	app.Get("/index.html", serveHTML(indexHTML, fiber.StatusOK))

	// HTML and JS files: bypass static handler so Cache-Control is guaranteed.
	noCache := func(c *fiber.Ctx) error {
		c.Set("Cache-Control", "no-cache, must-revalidate")
		return c.SendFile("./web" + c.Path())
	}
	app.Get("/*.html", noCache)
	app.Get("/js/*.js", noCache)

	// Everything else (CSS, images, fonts…) served normally with default caching.
	app.Static("/", "./web")

	// 404 handler: pre-rendered with config values injected.
	app.Use(serveHTML(notFoundHTML, fiber.StatusNotFound))

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
