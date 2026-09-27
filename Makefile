.PHONY: dev web build run infra infra-down

# Start infrastructure (PostgreSQL + Redis)
infra:
	docker compose up -d

# Stop infrastructure
infra-down:
	docker compose down

# Run API service in development mode
dev:
	go run ./cmd/api

# Serve ./web locally, proxying /api/* to production (UPSTREAM=... to override)
web:
	go run ./cmd/webdev -upstream $(or $(UPSTREAM),https://lights-monitor.com)

# Build API binary
build:
	go build -o bin/api ./cmd/api

# Run built binary
run: build
	./bin/api
