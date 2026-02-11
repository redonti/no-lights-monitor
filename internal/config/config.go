package config

import (
	"os"
	"strconv"
)

type Config struct {
	Port             string
	DatabaseURL      string
	RedisURL         string
	BotToken         string
	BaseURL          string
	GraphServiceURL  string
	PingInterval     int // expected seconds between pings
	OfflineThreshold int // seconds without ping before marking offline
}

func Load() *Config {
	return &Config{
		Port:             getEnv("PORT", "8080"),
		DatabaseURL:      getEnv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/nolights?sslmode=disable"),
		RedisURL:         getEnv("REDIS_URL", "redis://localhost:6379/0"),
		BotToken:         getEnv("BOT_TOKEN", ""),
		BaseURL:          getEnv("BASE_URL", "http://localhost:8080"),
		GraphServiceURL:  getEnv("GRAPH_SERVICE_URL", "http://localhost:8000"),
		PingInterval:     getEnvInt("PING_INTERVAL", 300),
		OfflineThreshold: getEnvInt("OFFLINE_THRESHOLD", 600),
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
