package models

import "time"

type User struct {
	ID         int64     `json:"id" db:"id"`
	TelegramID int64     `json:"telegram_id" db:"telegram_id"`
	Username   string    `json:"username" db:"username"`
	FirstName  string    `json:"first_name" db:"first_name"`
	CreatedAt  time.Time `json:"created_at" db:"created_at"`
}

type Monitor struct {
	ID                 int64      `json:"id" db:"id"`
	UserID             int64      `json:"user_id" db:"user_id"`
	Token              string     `json:"token" db:"token"`
	Name               string     `json:"name" db:"name"`
	Address            string     `json:"address" db:"address"`
	Latitude           float64    `json:"latitude" db:"latitude"`
	Longitude          float64    `json:"longitude" db:"longitude"`
	ChannelID          int64      `json:"channel_id,omitempty" db:"channel_id"`
	ChannelName        string     `json:"channel_name,omitempty" db:"channel_name"`
	MonitorType        string     `json:"monitor_type" db:"monitor_type"`   // "heartbeat" or "ping"
	PingTarget         string     `json:"ping_target" db:"ping_target"`     // IP/hostname for ping monitors
	IsOnline           bool       `json:"is_online" db:"is_online"`
	IsActive           bool       `json:"is_active" db:"is_active"`         // whether monitoring is enabled
	IsPublic           bool       `json:"is_public" db:"is_public"`         // whether shown on public map
	NotifyAddress      bool       `json:"notify_address" db:"notify_address"` // whether to show address in notifications
	OutageRegion       string     `json:"outage_region" db:"outage_region"`   // outage-data-ua region ID (e.g. "kyiv")
	OutageGroup        string     `json:"outage_group" db:"outage_group"`     // outage-data-ua group ID (e.g. "GPV1.1")
	NotifyOutage       bool       `json:"notify_outage" db:"notify_outage"`   // whether to show outage schedule in notifications
	OutagePhotoEnabled bool       `json:"outage_photo_enabled" db:"outage_photo_enabled"` // whether to post outage schedule photo to channel
	GraphEnabled       bool       `json:"graph_enabled" db:"graph_enabled"` // whether to post uptime graph to channel
	LastHeartbeatAt    *time.Time `json:"last_heartbeat_at,omitempty" db:"last_heartbeat_at"`
	LastStatusChangeAt time.Time  `json:"last_status_change_at" db:"last_status_change_at"`
	GraphMessageID       int        `json:"graph_message_id" db:"graph_message_id"`
	GraphWeekStart       *time.Time `json:"graph_week_start,omitempty" db:"graph_week_start"`
	OutagePhotoMessageID int        `json:"outage_photo_message_id" db:"outage_photo_message_id"`
	OutagePhotoUpdatedAt *time.Time `json:"outage_photo_updated_at,omitempty" db:"outage_photo_updated_at"`
	OutagePhotoETag      string     `json:"outage_photo_etag" db:"outage_photo_etag"`
	SettingsToken        string     `json:"settings_token" db:"settings_token"`
	DtekEnabled          bool       `json:"dtek_enabled" db:"dtek_enabled"`
	DtekRegion           string     `json:"dtek_region" db:"dtek_region"`
	DtekCity             string     `json:"dtek_city" db:"dtek_city"`
	DtekStreet           string     `json:"dtek_street" db:"dtek_street"`
	DtekHouse            string     `json:"dtek_house" db:"dtek_house"`
	DtekOutageNotifiedAt *time.Time `json:"dtek_outage_notified_at,omitempty" db:"dtek_outage_notified_at"`
	CreatedAt            time.Time  `json:"created_at" db:"created_at"`
}

// MonitorPublic is the public API representation shown on the map.
type MonitorPublic struct {
	ID             int64   `json:"id"`
	Name           string  `json:"name"`
	Address        string  `json:"address"`
	Latitude       float64 `json:"lat"`
	Longitude      float64 `json:"lng"`
	IsOnline       bool    `json:"is_online"`
	StatusDuration string  `json:"status_duration"`
	ChannelName    string  `json:"channel_name,omitempty"`
}

// StatusEvent is a historical record of a power status change (ON→OFF or OFF→ON).
type StatusEvent struct {
	ID        int64     `json:"id" db:"id"`
	MonitorID int64     `json:"monitor_id" db:"monitor_id"`
	IsOnline  bool      `json:"is_online" db:"is_online"`
	Timestamp time.Time `json:"timestamp" db:"timestamp"`
}
