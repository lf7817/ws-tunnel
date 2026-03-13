package main

import (
	"log"
	"net/http"
	"time"

	"tunnel-server/internal/config"
	"tunnel-server/internal/httpproxy"
	"tunnel-server/internal/tunnel"
)

func main() {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	hub := tunnel.NewDeviceHub(cfg.DeviceTokenByID)

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
