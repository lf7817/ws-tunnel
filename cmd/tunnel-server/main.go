package main

import (
	"log"
	"net/http"
	"time"

	"ws-tunnel/internal/config"
	"ws-tunnel/internal/httpproxy"
	"ws-tunnel/internal/tunnel"
)

var version string // 由构建时 -ldflags "-X main.version=..." 注入

func main() {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}
	if version != "" {
		log.Printf("tunnel-server version=%s", version)
	}

	var tokenSource tunnel.TokenSource
	if cfg.DeviceTokensFile != "" {
		src, err := config.FileTokenSource(cfg.DeviceTokensFile)
		if err != nil {
			log.Fatalf("device tokens file %s: %v", cfg.DeviceTokensFile, err)
		}
		tokenSource = src
		log.Printf("device tokens from file: %s (reload on connect when mtime changes)", cfg.DeviceTokensFile)
	} else {
		tokenSource = config.StaticTokenSource(cfg.DeviceTokenByID)
	}
	hub := tunnel.NewDeviceHub(tokenSource)

	mux := http.NewServeMux()
	mux.HandleFunc(cfg.TunnelDevicePath, tunnel.DeviceWSHandler(hub, cfg.AllowAllWSOrigins, cfg.PingInterval, cfg.PongWait))
	mux.Handle(cfg.DevicePrefix, httpproxy.NewHandler(hub, cfg))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("listening on %s", cfg.ListenAddr)
	log.Printf("ws path: %s", cfg.TunnelDevicePath)
	log.Printf("device prefix: %s", cfg.DevicePrefix)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}
