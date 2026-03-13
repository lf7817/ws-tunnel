package tunnel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var (
	ErrDeviceDisconnected = errors.New("device disconnected")
	ErrResponseClosed     = errors.New("response channel closed")
	ErrStreamClosed       = errors.New("stream closed")
)

// StreamSession 表示设备端的一次流式响应（SSE/长连接）。
// 设备会依次发送 response_start → 若干 response_chunk → 可选的 response_end。
type StreamSession struct {
	id     string
	startCh chan *TunnelResponse // 发送一次 response_start 后关闭
	chunkCh chan []byte          // 每块 body；response_end 或连接断开时关闭
	endCh   chan struct{}        // response_end 或连接断开时关闭

	closeStartOnce sync.Once
	closeChunkOnce sync.Once
}

// WaitStart 阻塞直到收到 response_start 或 ctx 取消/超时。返回状态码与响应头。
func (s *StreamSession) WaitStart(ctx context.Context) (statusCode int, headers map[string]string, err error) {
	select {
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	case resp, ok := <-s.startCh:
		if !ok {
			return 0, nil, ErrStreamClosed
		}
		if resp != nil {
			return resp.StatusCode, resp.Headers, nil
		}
		return 0, nil, ErrStreamClosed
	}
}

// Chunks 返回 body 块通道，调用方应 range 直到通道关闭。
func (s *StreamSession) Chunks() <-chan []byte { return s.chunkCh }

// End 返回在 response_end 或连接断开时被关闭的 channel。
func (s *StreamSession) End() <-chan struct{} { return s.endCh }

func (s *StreamSession) closeStart() {
	s.closeStartOnce.Do(func() { close(s.startCh) })
}

func (s *StreamSession) closeChunkAndEnd() {
	s.closeChunkOnce.Do(func() {
		close(s.chunkCh)
		close(s.endCh)
	})
}

type DeviceConn struct {
	deviceID string
	ws       *websocket.Conn

	writeMu sync.Mutex

	pendingMu sync.Mutex
	pending   map[string]chan TunnelResponse

	streamMu     sync.Mutex
	streamSessions map[string]*StreamSession

	closeOnce sync.Once
	closeCh   chan struct{}

	pingInterval time.Duration
	pongWait     time.Duration
}

func NewDeviceConn(deviceID string, ws *websocket.Conn, pingInterval, pongWait time.Duration) *DeviceConn {
	return &DeviceConn{
		deviceID:       deviceID,
		ws:             ws,
		pending:        map[string]chan TunnelResponse{},
		streamSessions: map[string]*StreamSession{},
		closeCh:        make(chan struct{}),
		pingInterval:   pingInterval,
		pongWait:       pongWait,
	}
}

func (c *DeviceConn) DeviceID() string { return c.deviceID }

func (c *DeviceConn) StartReadLoop(onDisconnect func()) {
	c.ws.SetReadLimit(16 << 20) // frame-level safety; body limits handled at HTTP layer
	_ = c.ws.SetReadDeadline(time.Now().Add(c.pongWait))
	c.ws.SetPongHandler(func(string) error {
		return c.ws.SetReadDeadline(time.Now().Add(c.pongWait))
	})

	go c.pingLoop()

	go func() {
		defer func() {
			c.closeAllStreamSessions()
			c.Close()
			if onDisconnect != nil {
				onDisconnect()
			}
		}()

		for {
			msgType, data, err := c.ws.ReadMessage()
			if err != nil {
				return
			}
			if msgType != websocket.TextMessage {
				continue
			}

			var resp TunnelResponse
			if err := json.Unmarshal(data, &resp); err != nil {
				continue
			}
			if resp.ID == "" {
				continue
			}

			switch resp.Type {
			case "response":
				c.pendingMu.Lock()
				ch, ok := c.pending[resp.ID]
				if ok {
					delete(c.pending, resp.ID)
				}
				c.pendingMu.Unlock()
				if ok {
					select {
					case ch <- resp:
					default:
					}
					close(ch)
				}
			case "response_start":
				c.streamMu.Lock()
				session, ok := c.streamSessions[resp.ID]
				c.streamMu.Unlock()
				if ok {
					select {
					case session.startCh <- &resp:
					default:
					}
					session.closeStart()
				}
			case "response_chunk":
				var chunk []byte
				if resp.BodyBase64 != "" {
					chunk, _ = base64.StdEncoding.DecodeString(resp.BodyBase64)
				}
				c.streamMu.Lock()
				session, ok := c.streamSessions[resp.ID]
				c.streamMu.Unlock()
				if ok && chunk != nil {
					select {
					case session.chunkCh <- chunk:
					default:
					}
				}
			case "response_end":
				c.streamMu.Lock()
				session, ok := c.streamSessions[resp.ID]
				if ok {
					delete(c.streamSessions, resp.ID)
				}
				c.streamMu.Unlock()
				if ok {
					session.closeChunkAndEnd()
				}
			default:
				// 忽略未知 type
			}
		}
	}()
}

func (c *DeviceConn) closeAllStreamSessions() {
	c.streamMu.Lock()
	sessions := make([]*StreamSession, 0, len(c.streamSessions))
	for _, s := range c.streamSessions {
		sessions = append(sessions, s)
	}
	c.streamSessions = map[string]*StreamSession{}
	c.streamMu.Unlock()
	for _, s := range sessions {
		s.closeStart()
		s.closeChunkAndEnd()
	}
}

func (c *DeviceConn) pingLoop() {
	t := time.NewTicker(c.pingInterval)
	defer t.Stop()
	for {
		select {
		case <-c.closeCh:
			return
		case <-t.C:
			c.writeMu.Lock()
			err := c.ws.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(5*time.Second))
			c.writeMu.Unlock()
			if err != nil {
				c.Close()
				return
			}
		}
	}
}

func (c *DeviceConn) SendRequest(ctx context.Context, req TunnelRequest) (TunnelResponse, error) {
	if req.Type == "" {
		req.Type = "request"
	}
	if req.Type != "request" || req.ID == "" || req.Method == "" || req.Path == "" {
		return TunnelResponse{}, errors.New("invalid tunnel request")
	}

	select {
	case <-c.closeCh:
		return TunnelResponse{}, ErrDeviceDisconnected
	default:
	}

	respCh := make(chan TunnelResponse, 1)

	c.pendingMu.Lock()
	c.pending[req.ID] = respCh
	c.pendingMu.Unlock()

	defer func() {
		c.pendingMu.Lock()
		if ch, ok := c.pending[req.ID]; ok && ch == respCh {
			delete(c.pending, req.ID)
			close(ch)
		}
		c.pendingMu.Unlock()
	}()

	payload, err := json.Marshal(req)
	if err != nil {
		return TunnelResponse{}, err
	}

	c.writeMu.Lock()
	err = c.ws.WriteMessage(websocket.TextMessage, payload)
	c.writeMu.Unlock()
	if err != nil {
		return TunnelResponse{}, err
	}

	select {
	case <-ctx.Done():
		return TunnelResponse{}, ctx.Err()
	case <-c.closeCh:
		return TunnelResponse{}, ErrDeviceDisconnected
	case resp, ok := <-respCh:
		if !ok {
			return TunnelResponse{}, ErrResponseClosed
		}
		return resp, nil
	}
}

// SendRequestStream 发送请求并返回流式会话，用于 SSE/长连接响应。
// 调用方应先 WaitStart，再 range Chunks() 写 body，直到 Chunks() 关闭。
func (c *DeviceConn) SendRequestStream(ctx context.Context, req TunnelRequest) (*StreamSession, error) {
	if req.Type == "" {
		req.Type = "request"
	}
	if req.Type != "request" || req.ID == "" || req.Method == "" || req.Path == "" {
		return nil, errors.New("invalid tunnel request")
	}
	select {
	case <-c.closeCh:
		return nil, ErrDeviceDisconnected
	default:
	}

	session := &StreamSession{
		id:      req.ID,
		startCh: make(chan *TunnelResponse, 1),
		chunkCh: make(chan []byte, 16),
		endCh:   make(chan struct{}),
	}

	c.streamMu.Lock()
	c.streamSessions[req.ID] = session
	c.streamMu.Unlock()

	sent := false
	defer func() {
		if !sent {
			c.streamMu.Lock()
			if c.streamSessions[req.ID] == session {
				delete(c.streamSessions, req.ID)
				session.closeStart()
				session.closeChunkAndEnd()
			}
			c.streamMu.Unlock()
		}
	}()

	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	c.writeMu.Lock()
	err = c.ws.WriteMessage(websocket.TextMessage, payload)
	c.writeMu.Unlock()
	if err != nil {
		return nil, err
	}
	sent = true
	return session, nil
}

func (c *DeviceConn) Close() {
	c.closeOnce.Do(func() {
		close(c.closeCh)

		c.pendingMu.Lock()
		for id, ch := range c.pending {
			delete(c.pending, id)
			close(ch)
		}
		c.pendingMu.Unlock()

		_ = c.ws.Close()
	})
}

func Upgrader(allowAllOrigins bool) websocket.Upgrader {
	up := websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin: func(r *http.Request) bool {
			if allowAllOrigins {
				return true
			}
			// Minimal safe default: same-origin only (empty Origin is allowed for some clients).
			origin := r.Header.Get("Origin")
			return origin == "" || origin == "null"
		},
	}
	return up
}
