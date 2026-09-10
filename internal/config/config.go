package config

import (
	"encoding/json"
	"fmt"
	"os"
)

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

// Load reads and parses config.json at path.
func Load(path string) (*Config, error) {
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
