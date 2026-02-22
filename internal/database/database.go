package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"no-lights-monitor/internal/models"
)

type DB struct {
	Pool *pgxpool.Pool
}

func New(ctx context.Context, databaseURL string) (*DB, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &DB{Pool: pool}, nil
}

func (db *DB) Close() {
	db.Pool.Close()
}

// scanMonitor is a helper that scans a row into a Monitor struct.
// It works with both pgx.Row and pgx.Rows since both have a Scan method.
func scanMonitor(scanner interface {
	Scan(dest ...interface{}) error
}, m *models.Monitor) error {
	return scanner.Scan(
		&m.ID, &m.UserID, &m.Token, &m.Name, &m.Address,
		&m.Latitude, &m.Longitude, &m.ChannelID, &m.ChannelName,
		&m.MonitorType, &m.PingTarget,
		&m.IsOnline, &m.IsActive, &m.IsPublic, &m.LastHeartbeatAt, &m.LastStatusChangeAt,
		&m.GraphMessageID, &m.GraphWeekStart, &m.CreatedAt,
	)
}

// Migrate creates the schema if it doesn't exist.
func (db *DB) Migrate(ctx context.Context) error {
	sql := `
	CREATE TABLE IF NOT EXISTS users (
		id            BIGSERIAL PRIMARY KEY,
		telegram_id   BIGINT UNIQUE NOT NULL,
		username      TEXT NOT NULL DEFAULT '',
		first_name    TEXT NOT NULL DEFAULT '',
		created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS monitors (
		id                   BIGSERIAL PRIMARY KEY,
		user_id              BIGINT NOT NULL REFERENCES users(id),
		token                UUID UNIQUE NOT NULL DEFAULT gen_random_uuid(),
		name                 TEXT NOT NULL,
		address              TEXT NOT NULL,
		latitude             DOUBLE PRECISION NOT NULL,
		longitude            DOUBLE PRECISION NOT NULL,
		channel_id           BIGINT,
		channel_name         TEXT NOT NULL DEFAULT '',
		is_online            BOOLEAN NOT NULL DEFAULT FALSE,
		last_heartbeat_at    TIMESTAMPTZ,
		last_status_change_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		graph_message_id     INT NOT NULL DEFAULT 0,
		graph_week_start     TIMESTAMPTZ,
		created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	ALTER TABLE monitors ADD COLUMN IF NOT EXISTS graph_message_id INT NOT NULL DEFAULT 0;
	ALTER TABLE monitors ADD COLUMN IF NOT EXISTS graph_week_start TIMESTAMPTZ;
	ALTER TABLE monitors ADD COLUMN IF NOT EXISTS is_active BOOLEAN NOT NULL DEFAULT TRUE;
	ALTER TABLE monitors ADD COLUMN IF NOT EXISTS monitor_type TEXT NOT NULL DEFAULT 'heartbeat';
	ALTER TABLE monitors ADD COLUMN IF NOT EXISTS ping_target TEXT NOT NULL DEFAULT '';
	ALTER TABLE monitors ADD COLUMN IF NOT EXISTS is_public BOOLEAN NOT NULL DEFAULT TRUE;

	CREATE INDEX IF NOT EXISTS idx_monitors_token   ON monitors(token);
	CREATE INDEX IF NOT EXISTS idx_monitors_user_id ON monitors(user_id);

	CREATE TABLE IF NOT EXISTS status_events (
		id          BIGSERIAL PRIMARY KEY,
		monitor_id  BIGINT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
		is_online   BOOLEAN NOT NULL,
		timestamp   TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE INDEX IF NOT EXISTS idx_status_events_monitor_time
		ON status_events (monitor_id, timestamp DESC);
	`
	_, err := db.Pool.Exec(ctx, sql)
	return err
}

// UpsertUser creates or updates a user and returns their record.
func (db *DB) UpsertUser(ctx context.Context, telegramID int64, username, firstName string) (*models.User, error) {
	var u models.User
	err := db.Pool.QueryRow(ctx, `
		INSERT INTO users (telegram_id, username, first_name)
		VALUES ($1, $2, $3)
		ON CONFLICT (telegram_id) DO UPDATE SET username = $2, first_name = $3
		RETURNING id, telegram_id, username, first_name, created_at
	`, telegramID, username, firstName).Scan(&u.ID, &u.TelegramID, &u.Username, &u.FirstName, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// GetAllUsers returns every user in the database.
func (db *DB) GetAllUsers(ctx context.Context) ([]*models.User, error) {
	rows, err := db.Pool.Query(ctx, `
		SELECT id, telegram_id, username, first_name, created_at
		FROM users ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []*models.User
	for rows.Next() {
		var u models.User
		if err := rows.Scan(&u.ID, &u.TelegramID, &u.Username, &u.FirstName, &u.CreatedAt); err != nil {
			return nil, err
		}
		users = append(users, &u)
	}
	return users, nil
}

// CreateMonitor inserts a new monitor and returns it (with generated token).
func (db *DB) CreateMonitor(ctx context.Context, userID int64, name, address string, lat, lng float64, channelID int64, channelName, monitorType, pingTarget string) (*models.Monitor, error) {
	var m models.Monitor
	row := db.Pool.QueryRow(ctx, `
		INSERT INTO monitors (user_id, name, address, latitude, longitude, channel_id, channel_name, monitor_type, ping_target)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, user_id, token, name, address, latitude, longitude,
		          channel_id, channel_name, monitor_type, ping_target,
		          is_online, is_active, is_public, last_heartbeat_at,
		          last_status_change_at, graph_message_id, graph_week_start, created_at
	`, userID, name, address, lat, lng, channelID, channelName, monitorType, pingTarget)
	err := scanMonitor(row, &m)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// GetMonitorByToken returns a monitor by its unique token.
func (db *DB) GetMonitorByToken(ctx context.Context, token string) (*models.Monitor, error) {
	var m models.Monitor
	row := db.Pool.QueryRow(ctx, `
		SELECT id, user_id, token, name, address, latitude, longitude,
		       channel_id, channel_name, monitor_type, ping_target,
		       is_online, is_active, is_public, last_heartbeat_at,
		       last_status_change_at, graph_message_id, graph_week_start, created_at
		FROM monitors WHERE token = $1
	`, token)
	err := scanMonitor(row, &m)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// GetMonitorsByTelegramID returns all monitors for the user with the given Telegram ID.
func (db *DB) GetMonitorsByTelegramID(ctx context.Context, telegramID int64) ([]*models.Monitor, error) {
	rows, err := db.Pool.Query(ctx, `
		SELECT m.id, m.user_id, m.token, m.name, m.address, m.latitude, m.longitude,
		       m.channel_id, m.channel_name, m.monitor_type, m.ping_target,
		       m.is_online, m.is_active, m.is_public, m.last_heartbeat_at,
		       m.last_status_change_at, m.graph_message_id, m.graph_week_start, m.created_at
		FROM monitors m
		JOIN users u ON u.id = m.user_id
		WHERE u.telegram_id = $1
		ORDER BY m.created_at DESC
	`, telegramID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var monitors []*models.Monitor
	for rows.Next() {
		var m models.Monitor
		if err := scanMonitor(rows, &m); err != nil {
			return nil, err
		}
		monitors = append(monitors, &m)
	}
	return monitors, nil
}

// GetPublicMonitors returns monitors that are visible on the public map.
func (db *DB) GetPublicMonitors(ctx context.Context) ([]*models.Monitor, error) {
	rows, err := db.Pool.Query(ctx, `
		SELECT id, user_id, token, name, address, latitude, longitude,
		       channel_id, channel_name, monitor_type, ping_target,
		       is_online, is_active, is_public, last_heartbeat_at,
		       last_status_change_at, graph_message_id, graph_week_start, created_at
		FROM monitors WHERE is_public = TRUE AND is_active = TRUE ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var monitors []*models.Monitor
	for rows.Next() {
		var m models.Monitor
		if err := scanMonitor(rows, &m); err != nil {
			return nil, err
		}
		monitors = append(monitors, &m)
	}
	return monitors, nil
}

// GetAllMonitors returns every monitor in the database.
func (db *DB) GetAllMonitors(ctx context.Context) ([]*models.Monitor, error) {
	rows, err := db.Pool.Query(ctx, `
		SELECT id, user_id, token, name, address, latitude, longitude,
		       channel_id, channel_name, monitor_type, ping_target,
		       is_online, is_active, is_public, last_heartbeat_at,
		       last_status_change_at, graph_message_id, graph_week_start, created_at
		FROM monitors ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var monitors []*models.Monitor
	for rows.Next() {
		var m models.Monitor
		if err := scanMonitor(rows, &m); err != nil {
			return nil, err
		}
		monitors = append(monitors, &m)
	}
	return monitors, nil
}

// UpdateMonitorStatus sets online/offline, updates the status change timestamp,
// and logs a status event for historical graphs.
func (db *DB) UpdateMonitorStatus(ctx context.Context, id int64, isOnline bool) error {
	_, err := db.Pool.Exec(ctx, `
		UPDATE monitors
		SET is_online = $2, last_status_change_at = NOW()
		WHERE id = $1
	`, id, isOnline)
	if err != nil {
		return err
	}

	// Log the status change event.
	_, err = db.Pool.Exec(ctx, `
		INSERT INTO status_events (monitor_id, is_online) VALUES ($1, $2)
	`, id, isOnline)
	return err
}

// GetLastEventBefore returns the most recent status event strictly before the given time.
// Returns nil, nil if no such event exists.
func (db *DB) GetLastEventBefore(ctx context.Context, monitorID int64, before time.Time) (*models.StatusEvent, error) {
	var e models.StatusEvent
	err := db.Pool.QueryRow(ctx, `
		SELECT id, monitor_id, is_online, timestamp
		FROM status_events
		WHERE monitor_id = $1 AND timestamp < $2
		ORDER BY timestamp DESC
		LIMIT 1
	`, monitorID, before).Scan(&e.ID, &e.MonitorID, &e.IsOnline, &e.Timestamp)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, err
	}
	return &e, nil
}

// GetStatusHistory returns status events for a monitor within a time range.
func (db *DB) GetStatusHistory(ctx context.Context, monitorID int64, from, to time.Time) ([]*models.StatusEvent, error) {
	rows, err := db.Pool.Query(ctx, `
		SELECT id, monitor_id, is_online, timestamp
		FROM status_events
		WHERE monitor_id = $1 AND timestamp >= $2 AND timestamp <= $3
		ORDER BY timestamp ASC
	`, monitorID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*models.StatusEvent
	for rows.Next() {
		var e models.StatusEvent
		if err := rows.Scan(&e.ID, &e.MonitorID, &e.IsOnline, &e.Timestamp); err != nil {
			return nil, err
		}
		events = append(events, &e)
	}
	return events, nil
}

// UpdateGraphMessage stores the Telegram message ID and week start for the current graph.
func (db *DB) UpdateGraphMessage(ctx context.Context, monitorID int64, messageID int, weekStart time.Time) error {
	_, err := db.Pool.Exec(ctx, `
		UPDATE monitors SET graph_message_id = $2, graph_week_start = $3 WHERE id = $1
	`, monitorID, messageID, weekStart)
	return err
}

// GetMonitorsWithChannels returns all monitors that have a Telegram channel linked.
func (db *DB) GetMonitorsWithChannels(ctx context.Context) ([]*models.Monitor, error) {
	rows, err := db.Pool.Query(ctx, `
		SELECT id, user_id, token, name, address, latitude, longitude,
		       channel_id, channel_name, monitor_type, ping_target,
		       is_online, is_active, is_public, last_heartbeat_at,
		       last_status_change_at, graph_message_id, graph_week_start, created_at
		FROM monitors
		WHERE channel_id IS NOT NULL AND channel_id != 0 AND is_active = TRUE
		ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var monitors []*models.Monitor
	for rows.Next() {
		var m models.Monitor
		if err := scanMonitor(rows, &m); err != nil {
			return nil, err
		}
		monitors = append(monitors, &m)
	}
	return monitors, nil
}

// UpdateMonitorHeartbeat sets the last heartbeat timestamp.
func (db *DB) UpdateMonitorHeartbeat(ctx context.Context, id int64, at time.Time) error {
	_, err := db.Pool.Exec(ctx, `
		UPDATE monitors SET last_heartbeat_at = $2 WHERE id = $1
	`, id, at)
	return err
}

// SetMonitorActive enables or disables monitoring for a monitor.
func (db *DB) SetMonitorActive(ctx context.Context, id int64, isActive bool) error {
	_, err := db.Pool.Exec(ctx, `
		UPDATE monitors SET is_active = $2 WHERE id = $1
	`, id, isActive)
	return err
}

// SetMonitorPublic shows or hides a monitor on the public map.
func (db *DB) SetMonitorPublic(ctx context.Context, id int64, isPublic bool) error {
	_, err := db.Pool.Exec(ctx, `
		UPDATE monitors SET is_public = $2 WHERE id = $1
	`, id, isPublic)
	return err
}

// UpdateMonitorName updates the display name of a monitor.
func (db *DB) UpdateMonitorName(ctx context.Context, id int64, name string) error {
	_, err := db.Pool.Exec(ctx, `
		UPDATE monitors SET name = $2 WHERE id = $1
	`, id, name)
	return err
}

// UpdateMonitorChannelName updates the stored Telegram channel username for a monitor.
func (db *DB) UpdateMonitorChannelName(ctx context.Context, id int64, channelName string) error {
	_, err := db.Pool.Exec(ctx, `
		UPDATE monitors SET channel_name = $2 WHERE id = $1
	`, id, channelName)
	return err
}

// UpdateMonitorAddress updates the address and coordinates of a monitor.
func (db *DB) UpdateMonitorAddress(ctx context.Context, id int64, address string, lat, lng float64) error {
	_, err := db.Pool.Exec(ctx, `
		UPDATE monitors SET address = $2, latitude = $3, longitude = $4 WHERE id = $1
	`, id, address, lat, lng)
	return err
}

// DeleteMonitor removes a monitor from the database.
// CASCADE will automatically delete associated status_events.
func (db *DB) DeleteMonitor(ctx context.Context, id int64) error {
	_, err := db.Pool.Exec(ctx, `
		DELETE FROM monitors WHERE id = $1
	`, id)
	return err
}

// GetOwnerTelegramIDByMonitorID returns the Telegram ID of the monitor's owner.
func (db *DB) GetOwnerTelegramIDByMonitorID(ctx context.Context, monitorID int64) (int64, error) {
	var telegramID int64
	err := db.Pool.QueryRow(ctx, `
		SELECT u.telegram_id FROM users u
		JOIN monitors m ON m.user_id = u.id
		WHERE m.id = $1
	`, monitorID).Scan(&telegramID)
	return telegramID, err
}

// FormatDuration returns a human-readable Ukrainian duration string.
func FormatDuration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60

	if days > 0 {
		return fmt.Sprintf("%d д %d год %d хв", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%d год %d хв", hours, minutes)
	}
	return fmt.Sprintf("%d хв", minutes)
}
