package httpproxy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"ws-tunnel/internal/config"
	"ws-tunnel/internal/httpx"
	"ws-tunnel/internal/id"
	"ws-tunnel/internal/tunnel"
)

// 等待设备返回 response_start 的最大时间；流式 body 开始后不再用短超时，避免断掉 SSE。
const streamStartTimeout = 10 * time.Second

type Handler struct {
	hub *tunnel.DeviceHub
	cfg config.Config
}

func NewHandler(hub *tunnel.DeviceHub, cfg config.Config) *Handler {
	return &Handler{hub: hub, cfg: cfg}
}

// isStreamRequest 判断是否应按流式处理（SSE/长连接）。设备端对这类请求会发 response_start + response_chunk + response_end。
func isStreamRequest(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/event-stream")
}

// needsLongTimeout 判断是否使用长超时（如升级包上传，设备端写盘+校验耗时长）。
func (h *Handler) needsLongTimeout(devicePath string) bool {
	return strings.Contains(devicePath, "upgrade/upload")
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	deviceID, devicePath, ok := ParseDeviceRoute(r.URL.Path, r.URL.RawQuery, h.cfg.DevicePrefix)
	if !ok {
		http.NotFound(w, r)
		return
	}

	conn, ok := h.hub.Get(deviceID)
	if !ok || conn == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "device offline")
		return
	}

	body, err := readBodyLimited(w, r, h.cfg.MaxBodyBytes)
	if err != nil {
		return
	}

	req := tunnel.TunnelRequest{
		Type:       "request",
		ID:         id.New(),
		Method:     r.Method,
		Path:       devicePath,
		Headers:    httpx.FirstValueHeaders(r.Header),
		BodyBase64: base64.StdEncoding.EncodeToString(body),
	}

	if isStreamRequest(r) {
		h.serveStream(w, r, conn, req, deviceID)
		return
	}

	h.serveOneShot(w, r, conn, req, deviceID)
}

func (h *Handler) serveOneShot(w http.ResponseWriter, r *http.Request, conn *tunnel.DeviceConn, req tunnel.TunnelRequest, deviceID string) {
	timeout := h.cfg.RequestTimeout
	if h.needsLongTimeout(req.Path) {
		timeout = h.cfg.LongRequestTimeout
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	resp, err := conn.SendRequest(ctx, req)
	if err != nil {
		if errorsIsTimeout(err) {
			writeJSONError(w, http.StatusGatewayTimeout, "gateway timeout")
			return
		}
		log.Printf("device request error device_id=%s err=%v", deviceID, err)
		writeJSONError(w, http.StatusServiceUnavailable, "device unavailable")
		return
	}

	if resp.Type != "response" || resp.ID != req.ID || resp.StatusCode == 0 {
		writeJSONError(w, http.StatusBadGateway, "bad gateway")
		return
	}

	raw, err := base64.StdEncoding.DecodeString(resp.BodyBase64)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "bad gateway")
		return
	}
	if int64(len(raw)) > h.cfg.MaxBodyBytes {
		writeJSONError(w, http.StatusBadGateway, "response too large")
		return
	}

	httpx.WriteResponseHeaders(w.Header(), resp.Headers)
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(raw)
}

func (h *Handler) serveStream(w http.ResponseWriter, r *http.Request, conn *tunnel.DeviceConn, req tunnel.TunnelRequest, deviceID string) {
	flusher, canFlush := w.(http.Flusher)
	if !canFlush {
		writeJSONError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	session, err := conn.SendRequestStream(r.Context(), req)
	if err != nil {
		if errors.Is(err, tunnel.ErrDeviceDisconnected) {
			writeJSONError(w, http.StatusServiceUnavailable, "device unavailable")
			return
		}
		log.Printf("device stream error device_id=%s err=%v", deviceID, err)
		writeJSONError(w, http.StatusServiceUnavailable, "device unavailable")
		return
	}

	ctxStart, cancelStart := context.WithTimeout(r.Context(), streamStartTimeout)
	statusCode, headers, err := session.WaitStart(ctxStart)
	cancelStart()
	if err != nil {
		if errorsIsTimeout(err) {
			writeJSONError(w, http.StatusGatewayTimeout, "gateway timeout")
			return
		}
		writeJSONError(w, http.StatusBadGateway, "bad gateway")
		return
	}

	httpx.WriteResponseHeaders(w.Header(), headers)
	w.WriteHeader(statusCode)
	flusher.Flush()

	for chunk := range session.Chunks() {
		if _, err := w.Write(chunk); err != nil {
			return
		}
		flusher.Flush()
	}
}

func ParseDeviceRoute(path, rawQuery, prefix string) (deviceID, devicePath string, ok bool) {
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	rest = strings.TrimPrefix(rest, "/")
	if rest == "" {
		return "", "", false
	}
	parts := strings.SplitN(rest, "/", 2)
	deviceID = parts[0]
	if deviceID == "" {
		return "", "", false
	}
	if len(parts) == 1 || parts[1] == "" {
		devicePath = "/"
	} else {
		devicePath = "/" + parts[1]
	}
	if rawQuery != "" {
		devicePath += "?" + rawQuery
	}
	return deviceID, devicePath, true
}

func readBodyLimited(w http.ResponseWriter, r *http.Request, max int64) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	defer r.Body.Close()

	reader := http.MaxBytesReader(w, r.Body, max)
	b, err := io.ReadAll(reader)
	if err != nil {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "payload too large")
		return nil, err
	}
	return b, nil
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func errorsIsTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	// fallback for wrapped/opaque errors
	return strings.Contains(strings.ToLower(err.Error()), "deadline exceeded")
}
