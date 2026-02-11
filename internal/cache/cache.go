package cache

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const heartbeatPrefix = "hb:"

type Cache struct {
	Client *redis.Client
}

func New(redisURL string) (*Cache, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	client := redis.NewClient(opts)
	if err := client.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return &Cache{Client: client}, nil
}

func (c *Cache) Close() error {
	return c.Client.Close()
}

// SetHeartbeat records the last heartbeat time for a monitor.
func (c *Cache) SetHeartbeat(ctx context.Context, monitorID int64, t time.Time) error {
	key := fmt.Sprintf("%s%d", heartbeatPrefix, monitorID)
	return c.Client.Set(ctx, key, t.Unix(), 0).Err()
}

// GetHeartbeat returns the last heartbeat time for a monitor.
func (c *Cache) GetHeartbeat(ctx context.Context, monitorID int64) (time.Time, error) {
	key := fmt.Sprintf("%s%d", heartbeatPrefix, monitorID)
	val, err := c.Client.Get(ctx, key).Result()
	if err != nil {
		return time.Time{}, err
	}
	unix, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(unix, 0), nil
}

// GetAllHeartbeats returns heartbeat timestamps for all monitors.
func (c *Cache) GetAllHeartbeats(ctx context.Context) (map[int64]time.Time, error) {
	pattern := heartbeatPrefix + "*"
	result := make(map[int64]time.Time)

	iter := c.Client.Scan(ctx, 0, pattern, 0).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()
		idStr := key[len(heartbeatPrefix):]
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			continue
		}
		val, err := c.Client.Get(ctx, key).Result()
		if err != nil {
			continue
		}
		unix, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			continue
		}
		result[id] = time.Unix(unix, 0)
	}
	return result, iter.Err()
}
