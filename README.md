# No-Lights Monitor

A community-powered power outage monitoring system. Devices ping a unique URL every 5 minutes — when pings stop, the system notifies a Telegram channel that the power is off. When pings resume, it notifies that power is back on. A web map shows all monitored locations with green (on) and red (off) markers.

## Architecture

```
[Device] --ping every 5min--> [Go Backend] --status change--> [Telegram Channel]
                                  |                                  |
                              [PostgreSQL]                      [Subscribers]
                              [Redis cache]
                              [Graph service]
                                  |
                              [Web Frontend] -- map via HTTP polling
```

**Tech stack:** Go (Fiber), PostgreSQL, Redis, Leaflet.js, Telegram Bot API, Python (graph service)

## Quick Start

### Prerequisites

- [Go 1.22+](https://go.dev/dl/)
- [Docker & Docker Compose](https://docs.docker.com/get-docker/)
- A Telegram bot token (get from [@BotFather](https://t.me/BotFather))

### 1. Clone and configure

```bash
git clone <your-repo-url>
cd no-lights-monitor
cp .env.example .env
# Edit .env and set your BOT_TOKEN
```

### 2. Start infrastructure

```bash
docker compose up -d
```

This starts PostgreSQL, Redis, graph service, and nginx. For local dev without SSL certs, run only infra: `docker compose up -d postgres redis`. Set `REDIS_PASSWORD` in `.env`.

### 3. Run the server

```bash
go run ./cmd/server
```

The server starts on `http://localhost:8080`:
- **Landing page:** http://localhost:8080
- **Live map:** http://localhost:8080/map.html
- **Ping endpoint:** `GET /api/ping/{token}`
- **Monitors API:** `GET /api/monitors`
- **Monitors history:** `GET /api/monitors/:id/history`

### 4. Register a monitor via Telegram

1. Find your bot on Telegram
2. Send `/create`
3. Follow the steps: name your location, share GPS coordinates, create a channel and add the bot as admin
4. You'll receive a unique ping URL
5. Configure any device to send a GET request to that URL every 5 minutes

## How Monitoring Works

1. Your device sends `GET /api/ping/{token}` every 5 minutes
2. The server records the heartbeat in Redis (sub-millisecond)
3. A background checker runs every 30 seconds
4. If no ping is received for 5 minutes — power is OFF — Telegram notification
5. When the next ping arrives — power is ON — Telegram notification
6. The web map polls the API every 30 seconds for status updates

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

## API Reference

| Endpoint | Method | Description |
|---|---|---|
| `/api/ping/:token` | GET | Heartbeat ping from monitoring device |
| `/api/monitors` | GET | List all monitors with status (for map) |
| `/api/monitors/:id/history` | GET | Status event history for a monitor |

## Production deployment

- Set `BASE_URL=https://yourdomain.com` in the server env (e.g. `/opt/no-lights-monitor/.env`)
- nginx proxies HTTPS to the app; config in `deploy/nginx.conf`
- GitHub Actions deploy uses `--env-file /opt/no-lights-monitor/.env`

## Development

```bash
# Build binary
go build -o bin/server ./cmd/server

# Run with hot-reload (install air: go install github.com/air-verse/air@latest)
air
```

## License

MIT
