package httpproxy

import "testing"

func TestParseDeviceRoute(t *testing.T) {
	deviceID, devicePath, ok := ParseDeviceRoute("/device/RTK001/api/gnss", "x=1&y=2", "/device/")
	if !ok {
		t.Fatalf("expected ok")
	}
	if deviceID != "RTK001" {
		t.Fatalf("deviceID = %q", deviceID)
	}
	if devicePath != "/api/gnss?x=1&y=2" {
		t.Fatalf("devicePath = %q", devicePath)
	}

	deviceID, devicePath, ok = ParseDeviceRoute("/device/RTK001/", "", "/device/")
	if !ok || deviceID != "RTK001" || devicePath != "/" {
		t.Fatalf("got ok=%v id=%q path=%q", ok, deviceID, devicePath)
	}
}
