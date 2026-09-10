package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadValid(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "config", "valid.json")
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if len(cfg.Devices) != 2 {
		t.Fatalf("got %d devices, want 2", len(cfg.Devices))
	}

	d := cfg.Devices[0]
	if d.DeviceID != "ebfake1111111111111111" {
		t.Errorf("device_id = %q, want %q", d.DeviceID, "ebfake1111111111111111")
	}
	if d.IPAddress != "192.168.1.6" {
		t.Errorf("ip_address = %q, want %q", d.IPAddress, "192.168.1.6")
	}
	if d.LocalKey != "fake-local-key16" {
		t.Errorf("local_key = %q, want %q", d.LocalKey, "fake-local-key16")
	}
	if d.Name != "Luz escritorio" {
		t.Errorf("name = %q, want %q", d.Name, "Luz escritorio")
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := loadConfig("/nonexistent/path/config.json")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoadMalformedJSON(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "bad.json")
	if err := os.WriteFile(path, []byte(`{not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := loadConfig(path)
	if err == nil {
		t.Error("expected error for malformed JSON")
	}
}

func TestLoadIgnoresExtraKeys(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "extra.json")
	data := []byte(`{
		"wallpaper": "/path/to/img.png",
		"theme": "dark",
		"devices": [{"device_id":"abc","ip_address":"1.2.3.4","local_key":"key1234567890123","name":"test"}]
	}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if len(cfg.Devices) != 1 {
		t.Fatalf("got %d devices, want 1", len(cfg.Devices))
	}
	if cfg.Devices[0].DeviceID != "abc" {
		t.Errorf("device_id = %q, want %q", cfg.Devices[0].DeviceID, "abc")
	}
}

func TestLastColorRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "last-color.txt")

	if err := writeLastColor(path, "ff8800"); err != nil {
		t.Fatalf("WriteLastColor: %v", err)
	}
	got := readLastColor(path)
	if got != "ff8800" {
		t.Errorf("ReadLastColor = %q, want %q", got, "ff8800")
	}
}

func TestLastColorDefaultOnMissing(t *testing.T) {
	got := readLastColor("/nonexistent/last-color.txt")
	if got != "ffffff" {
		t.Errorf("readLastColor(missing) = %q, want %q", got, "ffffff")
	}
}
