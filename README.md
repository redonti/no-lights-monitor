# No-Lights Monitor

A community-powered power outage monitoring system. Devices ping a unique URL every 5 minutes — when pings stop, the system notifies a Telegram channel that the power is off. When pings resume, it notifies that power is back on. A web map shows all monitored locations with green (on) and red (off) markers. With the newest updates, it also integrates with DTEK/region power schedules to show expected restoration times or upcoming scheduled blackouts.

## Architecture

The project has evolved into a multi-service architecture fully orchestrated by Docker Compose. Devices ping the Go API which proxies state to Redis. A background Go Worker monitors Redis drops to notify Telegram channels, utilizing a Go Outage Service for external blackout schedule estimates and a Python Graph Service for historical charts.

### Services Breakdown:
1. **api** (`cmd/api`): Handles public HTTP requests (the heartbeat `/api/ping/:token` endpoint and UI paths). Let your ESP32 or RaspberryPi hit this.
2. **worker** (`cmd/worker`): Runs the Telegram bot logic, heartbeat checker (reads from Redis to see who dropped offline), calls the graph generator, and sends notifications.
3. **outage** (`cmd/outage`): Fetches and processes external blackout schedules to enhance Telegram notifications with contextual "when will light be back" or "when will it turn off" estimations.
4. **graph-service**: Python service that visually renders heartbeat/outage statistical history as charts for the Telegram bot.

**Tech stack:** Go (Fiber, Telebot), PostgreSQL, Redis, Leaflet.js, Python.

## Quick Start

### Prerequisites

- [Go 1.22+](https://go.dev/dl/)
- [Docker & Docker Compose](https://docs.docker.com/get-docker/)
- A Telegram bot token (get from [@BotFather](https://t.me/BotFather))

### 1. Clone and configure

```bash
git clone https://github.com/redonti/no-lights-monitor.git
cd no-lights-monitor
cp .env.example .env
# Edit .env and set your BOT_TOKEN, DB credentials, etc.
```

### 2. Start Infrastructure and Services

```bash
# This builds and runs all Go and Python microservices along with Postgres & Redis
docker compose up -d --build
```
For local dev, you can just run `docker compose up -d postgres redis` and manually run the services.

### 3. Register a monitor via Telegram

1. Find your bot on Telegram
2. Send `/create`
3. Follow the steps: pick monitor type, name your location, share GPS coordinates, linking channels.
4. You'll receive a unique ping URL
5. Configure any device to send a GET request to that URL every 5 minutes

## How Monitoring Works

1. Your device sends `GET /api/ping/{token}` every 5 minutes to the **API service**.
2. The server records the heartbeat in **Redis** directly.
3. The **Worker service** background checker runs every 30 seconds.
4. If no ping is received for 5 minutes — power is OFF — Worker sends Telegram notification.
5. Notification is enhanced using data from **Outage service**.
6. When the next ping arrives — power is ON — Telegram notification is updated.
7. The web map polls the API every 30 seconds for status updates.

## Monitoring Devices

Any device that can make HTTP GET requests works:

- **Old Android phone** plugged into an outlet (use Tasker or a simple app)
- **ESP32/Arduino** with WiFi (send GET request in a loop)
- **Raspberry Pi** with a cron job (`curl` every 5 minutes)
- **Any computer** with a scheduled task

Example with curl:
```bash
# Run every 5 minutes via cron
*/5 * * * * curl -s https://your-server.com/api/ping/YOUR-TOKEN-HERE
```

## Production Deployment

If you are deploying this project on your own server with a custom domain, you will need to replace the default `lights-monitor.com` references throughout the codebase with your actual domain:

1. **Nginx Configuration (`deploy/nginx.conf`)**: 
   - Update `server_name` to your new domain.
   - Update the Let's Encrypt SSL certificate paths.
2. **Frontend Files (`web/index.html`, `web/map.html`, `web/robots.txt`)**:
   - Update any hardcoded canonical URLs and metadata tags pointing to the old domain.
3. **Environment (`.env`)**:
   - Ensure `BASE_URL=https://your-domain.com` is correctly set.

## Development

Currently the repository requires running multiple binaries manually if not using Docker Compose.

```bash
# Run API
go run ./cmd/api

# Run Worker
go run ./cmd/worker

# Run Outage Service
go run ./cmd/outage
```

## License

MIT
