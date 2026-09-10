package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// defaultColor is what get-color reports when the cache says nothing —
// matches ctl.py's read_last_color.
const defaultColor = "ffffff"

// Device is one entry of the `devices` array in config.json — the only
// part of that file this project reads; the wallpaper/theme-sync keys
// used by the sibling Python project are ignored.
type Device struct {
	DeviceID  string `json:"device_id"`
	IPAddress string `json:"ip_address"`
	LocalKey  string `json:"local_key"`
	Name      string `json:"name"`
}

// Config is the subset of config.json this project cares about.
type Config struct {
	Devices []Device `json:"devices"`
}

// loadConfig reads and parses config.json at path.
func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: reading %s: %w", path, err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config: parsing %s: %w", path, err)
	}
	return &cfg, nil
}

// readLastColor returns the cached last-applied colour (hex, no '#'), or
// the default white if the cache is missing or empty.
func readLastColor(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return defaultColor
	}
	s := strings.TrimSpace(string(data))
	if s == "" {
		return defaultColor
	}
	return s
}

// writeLastColor persists the last-applied colour (hex, no '#').
func writeLastColor(path, hexColor string) error {
	return os.WriteFile(path, []byte(hexColor), 0o644)
}
