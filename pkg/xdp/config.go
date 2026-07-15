// Package xdp mirrors gateway/pkg/xdp so Core can read/write the same Redis
// payload that Edge consumes. Keep the struct field-for-field aligned with
// the gateway-side definition.
package xdp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
)

const RedisConfigKey = "dylaris:xdp:config"

type Config struct {
	Enabled  bool `json:"enabled"`
	HostMode bool `json:"host_mode"`

	Interface string `json:"interface,omitempty"`

	ProtectedPorts string `json:"protected_ports"`
	RateLimit      uint64 `json:"rate_limit"`
	RateWindowMs   uint64 `json:"rate_window_ms"`
	BanDurationMin uint64 `json:"ban_duration_min"`

	MCMalformedLimit       int `json:"mc_malformed_limit"`
	MCMalformedWindowMin   int `json:"mc_malformed_window_min"`
	MCInvalidHostLimit     int `json:"mc_invalid_host_limit"`
	MCInvalidHostWindowMin int `json:"mc_invalid_host_window_min"`
	MCBanDurationMin       int `json:"mc_ban_duration_min"`

	Whitelist string `json:"whitelist,omitempty"`
}

func Defaults() Config {
	return Config{
		Enabled:                false,
		HostMode:               false,
		ProtectedPorts:         "25565",
		RateLimit:              1000,
		RateWindowMs:           1000,
		BanDurationMin:         30,
		MCMalformedLimit:       20,
		MCMalformedWindowMin:   2,
		MCInvalidHostLimit:     100,
		MCInvalidHostWindowMin: 2,
		MCBanDurationMin:       5,
	}
}

func Load(ctx context.Context, rdb *redis.Client) (Config, bool, error) {
	raw, err := rdb.Get(ctx, RedisConfigKey).Result()
	if err == redis.Nil {
		return Defaults(), false, nil
	}
	if err != nil {
		return Defaults(), false, err
	}
	var c Config
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return Defaults(), false, fmt.Errorf("xdp config: parse: %w", err)
	}
	return c, true, nil
}

func Save(ctx context.Context, rdb *redis.Client, c Config) error {
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return rdb.Set(ctx, RedisConfigKey, data, 0).Err()
}
