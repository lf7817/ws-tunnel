package tunnelclient

import (
	"net/url"
	"os"
	"time"
)

// Config 为隧道客户端配置，优先从环境变量读取。
type Config struct {
	// ServerWS 中心 WebSocket 地址，如 ws://127.0.0.1:8081/tunnel/device
	ServerWS string
	// DeviceID 设备唯一标识
	DeviceID string
	// Token 鉴权令牌，与中心配置一致
	Token string
	// TargetBase 本地 HTTP 服务基地址，如 http://127.0.0.1:8080
	TargetBase string
	// RequestTimeout 单次本地 HTTP 请求超时
	RequestTimeout time.Duration
	// ReconnectInitial 首次重连间隔
	ReconnectInitial time.Duration
	// ReconnectMax 重连间隔上限
	ReconnectMax time.Duration
}

// LoadFromEnv 从环境变量加载配置。
func LoadFromEnv() Config {
	c := Config{
		ServerWS:         envString("SERVER_WS", "ws://127.0.0.1:8081/tunnel/device"),
		DeviceID:         envString("DEVICE_ID", "RTK001"),
		Token:            envString("TOKEN", "dev-token"),
		TargetBase:       envString("TARGET_BASE", "http://127.0.0.1:8080"),
		RequestTimeout:   envDuration("REQUEST_TIMEOUT", 30*time.Second),
		ReconnectInitial: envDuration("RECONNECT_INITIAL", 1*time.Second),
		ReconnectMax:     envDuration("RECONNECT_MAX", 60*time.Second),
	}
	return c
}

// DialURL 返回带 device_id、token 查询参数的完整 WebSocket 连接 URL。
func (c Config) DialURL() (string, error) {
	u, err := url.Parse(c.ServerWS)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("device_id", c.DeviceID)
	q.Set("token", c.Token)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func envString(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
