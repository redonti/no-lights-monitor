.PHONY: dev build run infra infra-down migrate

# Start infrastructure (PostgreSQL + Redis)
infra:
	docker compose up -d

# Stop infrastructure
infra-down:
	docker compose down

# Run in development mode
dev:
	go run ./cmd/server

# Build binary
build:
	go build -o bin/server ./cmd/server

# Run built binary
run: build
	./bin/server
