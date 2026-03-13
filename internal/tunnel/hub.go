package tunnel

import "sync"

// TokenSource 提供设备 token 查询（由 config 包实现：静态 map 或配置文件 mtime 缓存）。
type TokenSource interface {
	ExpectedToken(deviceID string) (string, bool)
	HasConfiguredTokens() bool
}

type DeviceHub struct {
	mu     sync.RWMutex
	byID   map[string]*DeviceConn
	source TokenSource
}

func NewDeviceHub(source TokenSource) *DeviceHub {
	return &DeviceHub{
		byID:   map[string]*DeviceConn{},
		source: source,
	}
}

func (h *DeviceHub) ExpectedToken(deviceID string) (string, bool) {
	return h.source.ExpectedToken(deviceID)
}

// HasConfiguredTokens 是否配置了设备 token 白名单（非空则只允许列表内设备）
func (h *DeviceHub) HasConfiguredTokens() bool {
	return h.source.HasConfiguredTokens()
}

func (h *DeviceHub) Register(deviceID string, conn *DeviceConn) (replaced *DeviceConn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if old, ok := h.byID[deviceID]; ok {
		replaced = old
	}
	h.byID[deviceID] = conn
	return replaced
}

func (h *DeviceHub) Get(deviceID string) (*DeviceConn, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	c, ok := h.byID[deviceID]
	return c, ok
}

func (h *DeviceHub) RemoveIfMatch(deviceID string, conn *DeviceConn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if cur, ok := h.byID[deviceID]; ok && cur == conn {
		delete(h.byID, deviceID)
	}
}
