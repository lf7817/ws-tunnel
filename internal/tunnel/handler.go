package tunnel

import (
	"log"
	"net/http"
	"strings"
	"time"
)

func DeviceWSHandler(hub *DeviceHub, allowAllOrigins bool, pingInterval, pongWait time.Duration) http.HandlerFunc {
	up := Upgrader(allowAllOrigins)

	return func(w http.ResponseWriter, r *http.Request) {
		deviceID := strings.TrimSpace(r.URL.Query().Get("device_id"))
		token := strings.TrimSpace(r.URL.Query().Get("token"))
		if deviceID == "" {
			http.Error(w, "missing device_id", http.StatusBadRequest)
			return
		}

		if expected, ok := hub.ExpectedToken(deviceID); ok {
			if token == "" || token != expected {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		} else {
			// If token map is configured, and device_id missing from map, treat as unauthorized.
			// This matches \"static_map\" intent: only listed devices allowed.
			// If you want allow-any-device, leave DEVICE_TOKENS empty.
			if len(hub.tokens) > 0 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}

		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}

		log.Printf("device connected: %s", deviceID)

		conn := NewDeviceConn(deviceID, ws, pingInterval, pongWait)
		replaced := hub.Register(deviceID, conn)
		if replaced != nil {
			log.Printf("device replaced: %s", deviceID)
			replaced.Close()
		}

		conn.StartReadLoop(func() {
			log.Printf("device disconnected: %s", deviceID)
			hub.RemoveIfMatch(deviceID, conn)
		})
	}
}
