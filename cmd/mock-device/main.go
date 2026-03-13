package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type TunnelRequest struct {
	Type       string            `json:"type"`
	ID         string            `json:"id"`
	Method     string            `json:"method"`
	Path       string            `json:"path"`
	Headers    map[string]string `json:"headers,omitempty"`
	BodyBase64 string            `json:"body_base64,omitempty"`
}

type TunnelResponse struct {
	Type       string            `json:"type"`
	ID         string            `json:"id"`
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers,omitempty"`
	BodyBase64 string            `json:"body_base64,omitempty"`
}

func main() {
	serverWS := envString("SERVER_WS", "ws://127.0.0.1:8081/tunnel/device")
	deviceID := envString("DEVICE_ID", "RTK001")
	token := envString("TOKEN", "dev-token")
	targetBase := envString("TARGET_BASE", "http://127.0.0.1:8080")

	u, err := url.Parse(serverWS)
	if err != nil {
		log.Fatalf("bad SERVER_WS: %v", err)
	}
	q := u.Query()
	q.Set("device_id", deviceID)
	q.Set("token", token)
	u.RawQuery = q.Encode()

	log.Printf("connecting: %s", u.String())
	ws, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Fatalf("dial error: %v", err)
	}
	defer ws.Close()

	client := &http.Client{Timeout: 30 * time.Second}

	for {
		mt, data, err := ws.ReadMessage()
		if err != nil {
			log.Fatalf("read error: %v", err)
		}
		if mt != websocket.TextMessage {
			continue
		}

		var treq TunnelRequest
		if err := json.Unmarshal(data, &treq); err != nil {
			continue
		}
		if treq.Type != "request" || treq.ID == "" {
			continue
		}

		go func(req TunnelRequest) {
			resp := handleOne(client, targetBase, req)
			b, err := json.Marshal(resp)
			if err != nil {
				return
			}
			_ = ws.WriteMessage(websocket.TextMessage, b)
		}(treq)
	}
}

func handleOne(client *http.Client, targetBase string, treq TunnelRequest) TunnelResponse {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	bodyBytes, _ := base64.StdEncoding.DecodeString(treq.BodyBase64)

	targetURL := strings.TrimRight(targetBase, "/") + treq.Path
	httpReq, err := http.NewRequestWithContext(ctx, treq.Method, targetURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return TunnelResponse{Type: "response", ID: treq.ID, StatusCode: 502, BodyBase64: ""}
	}
	for k, v := range treq.Headers {
		if k == "" {
			continue
		}
		httpReq.Header.Set(k, v)
	}

	httpResp, err := client.Do(httpReq)
	if err != nil {
		return TunnelResponse{Type: "response", ID: treq.ID, StatusCode: 502, BodyBase64: ""}
	}
	defer httpResp.Body.Close()

	b, _ := io.ReadAll(httpResp.Body)
	h := map[string]string{}
	for k, vals := range httpResp.Header {
		if len(vals) > 0 {
			h[k] = vals[0]
		}
	}

	return TunnelResponse{
		Type:       "response",
		ID:         treq.ID,
		StatusCode: httpResp.StatusCode,
		Headers:    h,
		BodyBase64: base64.StdEncoding.EncodeToString(b),
	}
}

func envString(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}
