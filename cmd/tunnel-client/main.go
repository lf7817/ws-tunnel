// tunnel-client 是通用隧道客户端：连接隧道中心，将中心下发的 HTTP 请求转发到本地服务，
// 并将响应回传。与业务解耦，仅需配置 TARGET_BASE 指向本地 HTTP 服务即可。
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"ws-tunnel/internal/tunnelclient"
)

var version string // 由构建时 -ldflags "-X main.version=..." 注入

func main() {
	cfg := tunnelclient.LoadFromEnv()
	client := tunnelclient.New(cfg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if version != "" {
		log.Printf("[tunnel-client] version=%s", version)
	}
	log.Printf("[tunnel-client] starting device_id=%s target=%s", cfg.DeviceID, cfg.TargetBase)
	client.Run(ctx)
	log.Print("[tunnel-client] stopped")
}
