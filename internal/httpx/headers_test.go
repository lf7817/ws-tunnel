package httpx

import "net/http"
import "testing"

func TestFirstValueHeadersFiltersHopByHop(t *testing.T) {
	h := http.Header{}
	h.Add("Connection", "keep-alive")
	h.Add("Upgrade", "websocket")
	h.Add("X-Test", "a")
	h.Add("X-Test", "b")
	h.Add("Content-Length", "999")

	m := FirstValueHeaders(h)
	if _, ok := m["Connection"]; ok {
		t.Fatalf("Connection should be filtered")
	}
	if _, ok := m["Upgrade"]; ok {
		t.Fatalf("Upgrade should be filtered")
	}
	if v := m["X-Test"]; v != "a" {
		t.Fatalf("X-Test = %q", v)
	}
	if _, ok := m["Content-Length"]; ok {
		t.Fatalf("Content-Length should be filtered")
	}
}
