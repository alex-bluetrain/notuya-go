// Package control provides a per-device, session-owning control layer plus the
// config model (devices, rooms, scenes) shared by the notuya daemon and GUI.
//
// A Control owns one persistent protocol35 session for a single device and
// serializes every command behind a mutex. It borrows a short-lived music-mode
// streamer during a live colour drag. This mirrors what the GUI proved out and
// is the model the daemon uses to back the mobile app.
package control

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Device is one entry of the `devices` array in config.json.
type Device struct {
	DeviceID  string `json:"device_id"`
	IPAddress string `json:"ip_address"`
	LocalKey  string `json:"local_key"`
	Name      string `json:"name"`
}

// DisplayName returns the device's label, falling back to its id.
func (d Device) DisplayName() string {
	if d.Name != "" {
		return d.Name
	}
	return d.DeviceID
}

// Room is one entry of the top-level `rooms` array in config.json. Rooms are a
// first-class entity keyed by device_id; a device may belong to several rooms.
type Room struct {
	Name    string   `json:"name"`
	Devices []string `json:"devices"` // device_id references
}

// SceneState is one light's captured state within a scene. When Off (On is
// false) only DeviceID + On are meaningful. Hue and Sat are 0-1 units; Bright
// and Temp are 0-100 percents.
type SceneState struct {
	DeviceID string  `json:"device_id"`
	On       bool    `json:"on"`
	Mode     string  `json:"mode,omitempty"`   // device.ModeColour / ModeWhite
	Hue      float64 `json:"hue,omitempty"`    // 0-1 (colour mode)
	Sat      float64 `json:"sat,omitempty"`    // 0-1 (colour mode)
	Bright   float64 `json:"bright,omitempty"` // 0-100
	Temp     float64 `json:"temp,omitempty"`   // 0-100 (white mode)
}

// Scene is a named, software-only snapshot: applying it fans out discrete
// commands to the lights it lists.
type Scene struct {
	Name   string       `json:"name"`
	States []SceneState `json:"states"`
}

// Config is the subset of config.json this package cares about.
type Config struct {
	Devices []Device `json:"devices"`
	Rooms   []Room   `json:"rooms"`
	Scenes  []Scene  `json:"scenes"`
}

// RoomGroup is a resolved room: its name and the devices it contains, in the
// order they appear in the config's devices array.
type RoomGroup struct {
	Name    string
	Devices []Device
}

// UnassignedRoomName is the synthetic group holding devices not referenced by
// any room. It is computed at load time and never written back to the config.
const UnassignedRoomName = "No room"

// GroupByRoom resolves the config's rooms into ordered RoomGroups. Stale ids
// (no matching device) are skipped. Every device not referenced by any room is
// appended in a trailing synthetic "No room" group.
func GroupByRoom(cfg *Config) []RoomGroup {
	byID := make(map[string]Device, len(cfg.Devices))
	for _, d := range cfg.Devices {
		byID[d.DeviceID] = d
	}

	assigned := make(map[string]bool, len(cfg.Devices))
	groups := make([]RoomGroup, 0, len(cfg.Rooms)+1)
	for _, room := range cfg.Rooms {
		g := RoomGroup{Name: room.Name}
		for _, id := range room.Devices {
			dev, ok := byID[id]
			if !ok {
				continue // stale reference, tolerated
			}
			g.Devices = append(g.Devices, dev)
			assigned[id] = true
		}
		groups = append(groups, g)
	}

	var unassigned RoomGroup
	unassigned.Name = UnassignedRoomName
	for _, d := range cfg.Devices {
		if !assigned[d.DeviceID] {
			unassigned.Devices = append(unassigned.Devices, d)
		}
	}
	if len(unassigned.Devices) > 0 {
		groups = append(groups, unassigned)
	}
	return groups
}

// LoadConfig reads and parses config.json at path.
func LoadConfig(path string) (*Config, error) {
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

// SaveConfig writes devices, rooms and scenes back into config.json at path,
// preserving every other top-level key by round-tripping the file through a
// map of raw messages. The write is atomic (temp file then rename).
func SaveConfig(path string, devices []Device, rooms []Room, scenes []Scene) error {
	root := map[string]json.RawMessage{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("config: parsing %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("config: reading %s: %w", path, err)
	}

	if devices == nil {
		devices = []Device{}
	}
	devicesJSON, err := json.Marshal(devices)
	if err != nil {
		return fmt.Errorf("config: encoding devices: %w", err)
	}
	root["devices"] = devicesJSON

	if rooms == nil {
		rooms = []Room{}
	}
	roomsJSON, err := json.Marshal(rooms)
	if err != nil {
		return fmt.Errorf("config: encoding rooms: %w", err)
	}
	root["rooms"] = roomsJSON

	if scenes == nil {
		scenes = []Scene{}
	}
	scenesJSON, err := json.Marshal(scenes)
	if err != nil {
		return fmt.Errorf("config: encoding scenes: %w", err)
	}
	root["scenes"] = scenesJSON

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("config: encoding %s: %w", path, err)
	}

	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("config: creating %s: %w", dir, err)
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return fmt.Errorf("config: writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("config: replacing %s: %w", path, err)
	}
	return nil
}
