package config

import (
	"os"
	"strconv"
)

const (
	// DefaultPingIntervalSec is the expected seconds between heartbeat pings.
	DefaultPingIntervalSec = 300
	// DefaultOfflineThresholdSec is seconds without ping before marking monitor offline.
	DefaultOfflineThresholdSec = 300
	// DefaultOutageFetchIntervalSec is seconds between outage data fetches from GitHub.
	DefaultOutageFetchIntervalSec = 900
)

type Config struct {
	Port                string
	DatabaseURL         string
	RedisURL            string
	BotToken            string
	BaseURL             string
	GraphServiceURL     string
	PingInterval        int // expected seconds between pings
	OfflineThreshold    int // seconds without ping before marking offline
	AdminLogin          string
	AdminPassword       string
	OutageFetchInterval int    // seconds between outage data fetches
	OutageServiceURL    string // URL of the outage data service
	RabbitMQURL         string // AMQP connection URL for RabbitMQ
}

func Load() *Config {
	return &Config{
		Port:             getEnv("PORT", "8080"),
		DatabaseURL:      getEnv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/nolights?sslmode=disable"),
		RedisURL:         getEnv("REDIS_URL", "redis://localhost:6379/0"),
		BotToken:         getEnv("BOT_TOKEN", ""),
		BaseURL:          getEnv("BASE_URL", "http://localhost:8080"),
		GraphServiceURL:  getEnv("GRAPH_SERVICE_URL", "http://localhost:8000"),
		PingInterval:     getEnvInt("PING_INTERVAL", DefaultPingIntervalSec),
		OfflineThreshold: getEnvInt("OFFLINE_THRESHOLD", DefaultOfflineThresholdSec),
		AdminLogin:          getEnv("ADMIN_LOGIN", ""),
		AdminPassword:       getEnv("ADMIN_PASSWORD", ""),
		OutageFetchInterval: getEnvInt("OUTAGE_FETCH_INTERVAL", DefaultOutageFetchIntervalSec),
		OutageServiceURL:    getEnv("OUTAGE_SERVICE_URL", "http://localhost:8090"),
		RabbitMQURL:         getEnv("RABBITMQ_URL", "amqp://nolights:changeme@localhost:5672/"),
	}
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if val := os.Getenv(key); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			return n
		}
	}
	return fallback
}
