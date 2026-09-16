package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/averstraeten/notuya-go/pkg/discovery"
)

const scanTestConfig = `{
  "follow_mode": true,
  "theme_color": "aabbcc",
  "devices": [
    {"device_id": "aaa", "ip_address": "192.168.1.6", "local_key": "keyA", "name": "luz 1"},
    {"device_id": "bbb", "ip_address": "192.168.1.5", "local_key": "keyB", "name": "luz 2"}
  ]
}`

// writeScanConfig writes the fixture to a temp file and returns its path.
func writeScanConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(scanTestConfig), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	return path
}

func readScanConfig(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading config: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("parsing config: %v", err)
	}
	return root
}

func devicesOf(t *testing.T, root map[string]any) []map[string]any {
	t.Helper()
	raw, ok := root["devices"].([]any)
	if !ok {
		t.Fatalf("devices is not an array: %T", root["devices"])
	}
	out := make([]map[string]any, len(raw))
	for i, d := range raw {
		out[i] = d.(map[string]any)
	}
	return out
}

// A device whose ID matches the scan gets its IP rewritten; an unmatched
// device, the local_key, the name, and unmodelled top-level keys are all
// preserved.
func TestUpdateConfigIPs_MatchByID(t *testing.T) {
	path := writeScanConfig(t)

	found := []discovery.Device{
		{ID: "aaa", IP: "192.168.1.4", Version: "3.5"},
	}
	if !updateConfigIPs(path, found) {
		t.Fatal("updateConfigIPs returned false")
	}

	root := readScanConfig(t, path)
	if root["follow_mode"] != true {
		t.Errorf("follow_mode lost: %v", root["follow_mode"])
	}
	if root["theme_color"] != "aabbcc" {
		t.Errorf("theme_color lost: %v", root["theme_color"])
	}

	devs := devicesOf(t, root)
	if devs[0]["ip_address"] != "192.168.1.4" {
		t.Errorf("luz 1 ip = %v, want 192.168.1.4", devs[0]["ip_address"])
	}
	if devs[0]["local_key"] != "keyA" {
		t.Errorf("luz 1 local_key changed: %v", devs[0]["local_key"])
	}
	if devs[0]["name"] != "luz 1" {
		t.Errorf("luz 1 name changed: %v", devs[0]["name"])
	}
	if devs[1]["ip_address"] != "192.168.1.5" {
		t.Errorf("luz 2 ip changed: %v, want unchanged 192.168.1.5", devs[1]["ip_address"])
	}
}

// A scan that finds nothing to change leaves the file byte-for-byte intact.
func TestUpdateConfigIPs_NoChange(t *testing.T) {
	path := writeScanConfig(t)
	before, _ := os.ReadFile(path)

	// Same IP as already configured → no rewrite.
	found := []discovery.Device{{ID: "bbb", IP: "192.168.1.5", Version: "3.5"}}
	if !updateConfigIPs(path, found) {
		t.Fatal("updateConfigIPs returned false")
	}

	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Errorf("file rewritten despite no IP change:\nbefore=%s\nafter=%s", before, after)
	}
}

// An unmatched scan result never touches a device.
func TestUpdateConfigIPs_UnknownDevice(t *testing.T) {
	path := writeScanConfig(t)

	found := []discovery.Device{{ID: "zzz", IP: "192.168.1.9", Version: "3.5"}}
	if !updateConfigIPs(path, found) {
		t.Fatal("updateConfigIPs returned false")
	}

	devs := devicesOf(t, readScanConfig(t, path))
	if devs[0]["ip_address"] != "192.168.1.6" || devs[1]["ip_address"] != "192.168.1.5" {
		t.Errorf("IPs changed for unmatched scan: %v, %v", devs[0]["ip_address"], devs[1]["ip_address"])
	}
}
