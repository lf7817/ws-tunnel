package tunnelclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"ws-tunnel/internal/httpx"
	"ws-tunnel/internal/tunnel"

	"github.com/gorilla/websocket"
)

const streamChunkSize = 32 * 1024

// Client 隧道客户端：连中心、收请求、转本地 HTTP、回写响应（含流式）。
type Client struct {
	cfg        Config
	httpClient *http.Client
}

// New 根据配置创建客户端。
func New(cfg Config) *Client {
	return &Client{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: cfg.RequestTimeout,
		},
	}
}

// Run 连接中心并维持隧道，断线后按退避重连；ctx 取消时退出。
func (c *Client) Run(ctx context.Context) {
	backoff := c.cfg.ReconnectInitial
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		ws, err := c.dial(ctx)
		if err != nil {
			log.Printf("[tunnel-client] dial: %v", err)
			backoff = c.nextBackoff(backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
			continue
		}
		backoff = c.cfg.ReconnectInitial

		connCtx, cancel := context.WithCancel(ctx)
		writeChan := make(chan []byte, 64)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			c.writeLoop(connCtx, ws, writeChan)
		}()
		go func() {
			defer wg.Done()
			c.readLoop(connCtx, ws, writeChan)
		}()
		wg.Wait()
		cancel()
		_ = ws.Close()
	}
}

func (c *Client) nextBackoff(current time.Duration) time.Duration {
	next := current * 2
	if next > c.cfg.ReconnectMax {
		next = c.cfg.ReconnectMax
	}
	return next
}

func (c *Client) dial(ctx context.Context) (*websocket.Conn, error) {
	u, err := c.cfg.DialURL()
	if err != nil {
		return nil, err
	}
	dialer := websocket.Dialer{}
	ws, _, err := dialer.DialContext(ctx, u, nil)
	if err != nil {
		return nil, err
	}
	log.Printf("[tunnel-client] connected device_id=%s", c.cfg.DeviceID)
	return ws, nil
}

// readLoop 单协程读 WebSocket，收到 request 后起 goroutine 处理，通过 writeChan 串行写回。
func (c *Client) readLoop(ctx context.Context, ws *websocket.Conn, writeChan chan<- []byte) {
	defer close(writeChan)
	for {
		mt, data, err := ws.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				log.Printf("[tunnel-client] read: %v", err)
			}
			return
		}
		if mt != websocket.TextMessage {
			continue
		}
		var treq tunnel.TunnelRequest
		if err := json.Unmarshal(data, &treq); err != nil {
			continue
		}
		if treq.Type != "request" || treq.ID == "" {
			continue
		}
		go c.handleRequest(ctx, writeChan, treq)
	}
}

// writeLoop 单协程从 channel 取数据写 WebSocket，保证写串行化。
func (c *Client) writeLoop(ctx context.Context, ws *websocket.Conn, writeChan <-chan []byte) {
	for {
		select {
		case <-ctx.Done():
			return
		case data, ok := <-writeChan:
			if !ok {
				return
			}
			if err := ws.WriteMessage(websocket.TextMessage, data); err != nil {
				log.Printf("[tunnel-client] write: %v", err)
				return
			}
		}
	}
}

func (c *Client) send(ctx context.Context, writeChan chan<- []byte, resp tunnel.TunnelResponse) {
	b, err := json.Marshal(resp)
	if err != nil {
		return
	}
	select {
	case writeChan <- b:
	case <-ctx.Done():
	}
}

func (c *Client) handleRequest(ctx context.Context, writeChan chan<- []byte, treq tunnel.TunnelRequest) {
	if c.isStreamRequest(treq) {
		c.handleStream(ctx, writeChan, treq)
	} else {
		c.handleOneShot(ctx, writeChan, treq)
	}
}

func (c *Client) isStreamRequest(treq tunnel.TunnelRequest) bool {
	accept := treq.Headers["Accept"]
	if accept == "" {
		accept = treq.Headers["accept"]
	}
	return strings.Contains(strings.ToLower(accept), "text/event-stream")
}

func (c *Client) handleOneShot(ctx context.Context, writeChan chan<- []byte, treq tunnel.TunnelRequest) {
	reqCtx, cancel := context.WithTimeout(ctx, c.cfg.RequestTimeout)
	defer cancel()

	bodyBytes, _ := base64.StdEncoding.DecodeString(treq.BodyBase64)
	targetURL := strings.TrimRight(c.cfg.TargetBase, "/") + treq.Path
	httpReq, err := http.NewRequestWithContext(reqCtx, treq.Method, targetURL, bytes.NewReader(bodyBytes))
	if err != nil {
		c.send(ctx, writeChan, tunnel.TunnelResponse{Type: "response", ID: treq.ID, StatusCode: 502})
		return
	}
	for k, v := range treq.Headers {
		if k == "" || httpx.IsHopByHopHeader(k) {
			continue
		}
		if strings.ToLower(k) == "content-length" {
			continue
		}
		httpReq.Header.Set(k, v)
	}

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		c.send(ctx, writeChan, tunnel.TunnelResponse{Type: "response", ID: treq.ID, StatusCode: 502})
		return
	}
	defer httpResp.Body.Close()

	body, _ := io.ReadAll(httpResp.Body)
	headers := httpx.FirstValueHeaders(httpResp.Header)
	c.send(ctx, writeChan, tunnel.TunnelResponse{
		Type:       "response",
		ID:         treq.ID,
		StatusCode: httpResp.StatusCode,
		Headers:    headers,
		BodyBase64: base64.StdEncoding.EncodeToString(body),
	})
}

func (c *Client) handleStream(ctx context.Context, writeChan chan<- []byte, treq tunnel.TunnelRequest) {
	reqCtx, cancel := context.WithTimeout(ctx, c.cfg.RequestTimeout)
	defer cancel()

	bodyBytes, _ := base64.StdEncoding.DecodeString(treq.BodyBase64)
	targetURL := strings.TrimRight(c.cfg.TargetBase, "/") + treq.Path
	httpReq, err := http.NewRequestWithContext(reqCtx, treq.Method, targetURL, bytes.NewReader(bodyBytes))
	if err != nil {
		c.send(ctx, writeChan, tunnel.TunnelResponse{Type: "response", ID: treq.ID, StatusCode: 502})
		return
	}
	for k, v := range treq.Headers {
		if k == "" || httpx.IsHopByHopHeader(k) {
			continue
		}
		if strings.ToLower(k) == "content-length" {
			continue
		}
		httpReq.Header.Set(k, v)
	}

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		c.send(ctx, writeChan, tunnel.TunnelResponse{Type: "response", ID: treq.ID, StatusCode: 502})
		return
	}
	defer httpResp.Body.Close()

	headers := httpx.FirstValueHeaders(httpResp.Header)
	c.send(ctx, writeChan, tunnel.TunnelResponse{
		Type:       "response_start",
		ID:         treq.ID,
		StatusCode: httpResp.StatusCode,
		Headers:    headers,
	})
	buf := make([]byte, streamChunkSize)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		n, err := httpResp.Body.Read(buf)
		if n > 0 {
			c.send(ctx, writeChan, tunnel.TunnelResponse{
				Type:       "response_chunk",
				ID:         treq.ID,
				BodyBase64: base64.StdEncoding.EncodeToString(buf[:n]),
			})
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
	}
	c.send(ctx, writeChan, tunnel.TunnelResponse{Type: "response_end", ID: treq.ID})
}
