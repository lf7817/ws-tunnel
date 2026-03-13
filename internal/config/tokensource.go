package config

import (
	"log"
	"os"
	"sync"
	"time"
)

// tokenSource 与 tunnel.TokenSource 方法一致，供本包实现返回。
type tokenSource interface {
	ExpectedToken(deviceID string) (string, bool)
	HasConfiguredTokens() bool
}

// StaticTokenSource 使用固定 map，不做热更新。返回值可作为 tunnel.TokenSource 传入 Hub。
func StaticTokenSource(m map[string]string) tokenSource {
	cp := make(map[string]string)
	for k, v := range m {
		cp[k] = v
	}
	return &staticTokenSource{tokens: cp}
}

type staticTokenSource struct {
	tokens map[string]string
}

func (s *staticTokenSource) ExpectedToken(deviceID string) (string, bool) {
	t, ok := s.tokens[deviceID]
	return t, ok
}

func (s *staticTokenSource) HasConfiguredTokens() bool {
	return len(s.tokens) > 0
}

// FileTokenSource 从配置文件按需读取 device_id=token，用 mtime 缓存，文件变更后下次连接自动生效。返回值可作为 tunnel.TokenSource 传入 Hub。
func FileTokenSource(path string) (tokenSource, error) {
	f := &fileTokenSource{path: path}
	if _, err := f.refresh(); err != nil {
		return nil, err
	}
	return f, nil
}

type fileTokenSource struct {
	path  string
	mu    sync.RWMutex
	cache map[string]string
	mtime time.Time
}

func (f *fileTokenSource) refresh() (map[string]string, error) {
	info, err := os.Stat(f.path)
	if err != nil {
		return nil, err
	}
	newMtime := info.ModTime()
	f.mu.Lock()
	if !f.mtime.IsZero() && newMtime.Equal(f.mtime) {
		m := f.cache
		f.mu.Unlock()
		return m, nil
	}
	f.mu.Unlock()

	tokens, err := LoadDeviceTokensFromFile(f.path)
	if err != nil {
		f.mu.RLock()
		cache := f.cache
		f.mu.RUnlock()
		if cache != nil {
			log.Printf("token file refresh failed, using cached: %v", err)
			return cache, nil
		}
		return nil, err
	}
	f.mu.Lock()
	f.cache = tokens
	f.mtime = newMtime
	f.mu.Unlock()
	return tokens, nil
}

func (f *fileTokenSource) ExpectedToken(deviceID string) (string, bool) {
	m, _ := f.refresh()
	if m == nil {
		return "", false
	}
	t, ok := m[deviceID]
	return t, ok
}

func (f *fileTokenSource) HasConfiguredTokens() bool {
	m, _ := f.refresh()
	if m == nil {
		return false
	}
	return len(m) > 0
}
