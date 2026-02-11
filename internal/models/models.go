package models

import "time"

type User struct {
	ID         int64     `json:"id"`
	TelegramID int64     `json:"telegram_id"`
	Username   string    `json:"username"`
	FirstName  string    `json:"first_name"`
	CreatedAt  time.Time `json:"created_at"`
}

type Monitor struct {
	ID                 int64      `json:"id"`
	UserID             int64      `json:"user_id"`
	Token              string     `json:"token"`
	Name               string     `json:"name"`
	Address            string     `json:"address"`
	Latitude           float64    `json:"latitude"`
	Longitude          float64    `json:"longitude"`
	ChannelID          int64      `json:"channel_id,omitempty"`
	ChannelName        string     `json:"channel_name,omitempty"`
	IsOnline           bool       `json:"is_online"`
	IsActive           bool       `json:"is_active"` // whether monitoring is enabled
	LastHeartbeatAt    *time.Time `json:"last_heartbeat_at,omitempty"`
	LastStatusChangeAt time.Time  `json:"last_status_change_at"`
	GraphMessageID     int        `json:"graph_message_id"`
	GraphWeekStart     *time.Time `json:"graph_week_start,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
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
	ID        int64     `json:"id"`
	MonitorID int64     `json:"monitor_id"`
	IsOnline  bool      `json:"is_online"`
	Timestamp time.Time `json:"timestamp"`
}
