package tunnel

import "sync"

type DeviceHub struct {
	mu      sync.RWMutex
	byID    map[string]*DeviceConn
	tokens  map[string]string
	onClose func(deviceID string)
}

func NewDeviceHub(deviceTokenByID map[string]string) *DeviceHub {
	cp := map[string]string{}
	for k, v := range deviceTokenByID {
		cp[k] = v
	}
	return &DeviceHub{
		byID:   map[string]*DeviceConn{},
		tokens: cp,
	}
}

func (h *DeviceHub) ExpectedToken(deviceID string) (string, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	t, ok := h.tokens[deviceID]
	return t, ok
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
