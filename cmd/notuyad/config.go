package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// defaultColor is what the last-colour cache reports when nothing is stored.
const defaultColor = "ffffff"

// Device is one entry of the `devices` array in config.json — the only part
// of that file the daemon reads, matching cmd/notuya.
type Device struct {
	DeviceID  string `json:"device_id"`
	IPAddress string `json:"ip_address"`
	LocalKey  string `json:"local_key"`
	Name      string `json:"name"`
}

// Config is the subset of config.json the daemon cares about.
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

// resolveConfigPath applies the precedence: --config flag > NOTUYA_CONFIG env
// > the user's config directory — identical to cmd/notuya.
func resolveConfigPath(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if env := os.Getenv("NOTUYA_CONFIG"); env != "" {
		return env
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "notuya-go", "config.json")
}

// readLastColor returns the cached last-applied colour (hex, no '#'), or the
// default white if the cache is missing or empty.
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

// name returns the device's display name, falling back to its id.
func (d Device) name() string {
	if d.Name != "" {
		return d.Name
	}
	return d.DeviceID
}
