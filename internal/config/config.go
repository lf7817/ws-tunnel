package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ListenAddr          string
	DevicePrefix        string
	TunnelDevicePath    string
	RequestTimeout      time.Duration
	LongRequestTimeout  time.Duration // 用于升级上传等耗时操作
	MaxBodyBytes        int64
	PingInterval        time.Duration
	PongWait            time.Duration
	DeviceTokenByID     map[string]string
	AllowAllWSOrigins   bool
}

func LoadFromEnv() (Config, error) {
	cfg := Config{
		ListenAddr:        envString("LISTEN_ADDR", ":8081"),
		DevicePrefix:      envString("DEVICE_PREFIX", "/device/"),
		TunnelDevicePath:  envString("TUNNEL_DEVICE_PATH", "/tunnel/device"),
		RequestTimeout:     envDuration("REQUEST_TIMEOUT", 30*time.Second),
		LongRequestTimeout: envDuration("LONG_REQUEST_TIMEOUT", 5*time.Minute), // 升级上传等
		MaxBodyBytes:       envInt64("MAX_BODY_BYTES", 20<<20),                  // 20 MiB（含升级包上传等）
		PingInterval:      envDuration("WS_PING_INTERVAL", 25*time.Second),
		PongWait:          envDuration("WS_PONG_WAIT", 60*time.Second),
		DeviceTokenByID:   parseDeviceTokens(os.Getenv("DEVICE_TOKENS")),
		AllowAllWSOrigins: envBool("WS_ALLOW_ALL_ORIGINS", true),
	}

	if !strings.HasPrefix(cfg.DevicePrefix, "/") {
		return Config{}, errors.New("DEVICE_PREFIX must start with '/'")
	}
	if !strings.HasSuffix(cfg.DevicePrefix, "/") {
		cfg.DevicePrefix += "/"
	}
	if cfg.TunnelDevicePath == "" || !strings.HasPrefix(cfg.TunnelDevicePath, "/") {
		return Config{}, errors.New("TUNNEL_DEVICE_PATH must start with '/'")
	}
	if cfg.RequestTimeout <= 0 {
		return Config{}, errors.New("REQUEST_TIMEOUT must be > 0")
	}
	if cfg.LongRequestTimeout <= 0 {
		return Config{}, errors.New("LONG_REQUEST_TIMEOUT must be > 0")
	}
	if cfg.MaxBodyBytes <= 0 {
		return Config{}, errors.New("MAX_BODY_BYTES must be > 0")
	}
	if cfg.PongWait <= 0 || cfg.PingInterval <= 0 {
		return Config{}, errors.New("WS_PONG_WAIT and WS_PING_INTERVAL must be > 0")
	}
	return cfg, nil
}

func parseDeviceTokens(raw string) map[string]string {
	out := map[string]string{}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return out
	}
	// Format: "RTK001=token1,RTK002=token2"
	parts := strings.Split(raw, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		kv := strings.SplitN(p, "=", 2)
		if len(kv) != 2 {
			continue
		}
		id := strings.TrimSpace(kv[0])
		tok := strings.TrimSpace(kv[1])
		if id == "" || tok == "" {
			continue
		}
		out[id] = tok
	}
	return out
}

func envString(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	if v, ok := os.LookupEnv(key); ok {
		v = strings.TrimSpace(strings.ToLower(v))
		return v == "1" || v == "true" || v == "yes" || v == "y" || v == "on"
	}
	return def
}

func envInt64(key string, def int64) int64 {
	if v, ok := os.LookupEnv(key); ok {
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err == nil {
			return n
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok {
		d, err := time.ParseDuration(strings.TrimSpace(v))
		if err == nil {
			return d
		}
	}
	return def
}
