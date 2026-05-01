package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"

	"no-lights-monitor/cmd/bot/bot"
	"no-lights-monitor/cmd/bot/channeldesc"
	"no-lights-monitor/internal/config"
	"no-lights-monitor/internal/database"
	"no-lights-monitor/internal/health"
	"no-lights-monitor/internal/mq"
	"no-lights-monitor/internal/outage"
	"no-lights-monitor/internal/ping"
)

func main() {
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

	// --- RabbitMQ ---
	mqPublisher, err := mq.NewPublisher(cfg.RabbitMQURL)
	if err != nil {
		log.Fatalf("rabbitmq publisher: %v", err)
	}
	defer mqPublisher.Close()

	mqConsumer, err := mq.NewConsumer(cfg.RabbitMQURL)
	if err != nil {
		log.Fatalf("rabbitmq consumer: %v", err)
	}
	defer mqConsumer.Close()
	log.Println("rabbitmq connected")

	// --- Health server ---
	health.ServeAsync(func() error {
		return db.Pool.Ping(ctx)
	})

	// --- Telegram Bot ---
	tgBot, err := bot.New(cfg.BotToken, db, ping.PingHost, cfg.BaseURL, cfg.TelegramChatUsername)
	if err != nil {
		log.Fatalf("bot: %v", err)
	}

	// --- Outage Client ---
	outageClient := outage.NewClient(cfg.OutageServiceURL)
	tgBot.SetOutageClient(outageClient)

	// --- Graph Requester (publishes to MQ for worker to generate) ---
	graphRequester := mq.NewGraphRequester(mqPublisher)
	tgBot.SetGraphUpdater(graphRequester)

	// --- Start bot polling ---
	go tgBot.Start()
	defer tgBot.Stop()
	log.Println("telegram bot started")

	// --- Start RabbitMQ listener ---
	listener := newListener(tgBot.TeleBot(), db, outageClient, mqConsumer)
	go listener.start(ctx)
	log.Println("rabbitmq listener started")

	// --- Channel description checker (daily at 14:00 Kyiv) ---
	descChecker := channeldesc.NewChecker(tgBot.TeleBot(), db, cfg.BaseURL)
	go descChecker.Start(ctx)
	log.Println("channel description checker started")

	// --- Graceful shutdown ---
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down bot service...")
	cancel()
}
