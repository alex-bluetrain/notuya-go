package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/averstraeten/notuya-go/pkg/bulb"
	"github.com/averstraeten/notuya-go/pkg/control"
	"github.com/averstraeten/notuya-go/pkg/device"
)

// server holds the daemon's shared state. The device registry owns the
// persistent per-device sessions; rooms/scenes/devices mirror config.json and
// are guarded by cfgMu for the config-editing routes.
type server struct {
	configPath    string
	lastColorPath string
	streamOpts    bulb.StreamOptions
	registry      *control.Registry

	cfgMu   sync.Mutex
	devices []control.Device
	rooms   []control.Room
	scenes  []control.Scene
}

// routes wires the resourceful API. Go 1.22+ method+wildcard patterns keep the
// mux flat; CORS is applied to every response so the Expo web build can call
// the daemon cross-origin.
func (s *server) routes() http.Handler {
	mux := http.NewServeMux()

	// Devices / control.
	mux.HandleFunc("GET /devices", s.handleListDevices)
	mux.HandleFunc("GET /devices/{id}", s.handleDeviceStatus)
	mux.HandleFunc("POST /devices/{id}/power", s.handleDevicePower)
	mux.HandleFunc("POST /devices/{id}/color", s.handleDeviceColor)
	mux.HandleFunc("POST /devices/{id}/brightness", s.handleDeviceBrightness)
	mux.HandleFunc("POST /devices/{id}/temperature", s.handleDeviceTemperature)
	mux.HandleFunc("GET /devices/{id}/stream", s.handleDeviceStream) // WebSocket

	// Rooms.
	mux.HandleFunc("GET /rooms", s.handleListRooms)
	mux.HandleFunc("POST /rooms/{name}/power", s.handleRoomPower)
	mux.HandleFunc("GET /rooms/{name}/stream", s.handleRoomStream) // WebSocket

	// Scenes.
	mux.HandleFunc("GET /scenes", s.handleListScenes)
	mux.HandleFunc("POST /scenes/{name}/apply", s.handleSceneApply)
	mux.HandleFunc("POST /scenes/{name}/capture", s.handleSceneCapture)
	mux.HandleFunc("DELETE /scenes/{name}", s.handleSceneDelete)

	// Discovery / config.
	mux.HandleFunc("POST /discover", s.handleDiscover)
	mux.HandleFunc("GET /config/devices", s.handleGetConfigDevices)
	mux.HandleFunc("PUT /config/devices", s.handlePutConfigDevices)

	return withCORS(mux)
}

// withCORS answers preflight requests and adds permissive CORS headers. No
// auth — trusted LAN only.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// writeResult reports 200 on full success, 502 when a device command failed.
func writeResult(w http.ResponseWriter, ok bool) {
	if ok {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	writeJSON(w, http.StatusBadGateway, map[string]bool{"ok": false})
}

func decodeBody(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

// --- device handlers ---

// deviceInfo is the list entry for GET /devices.
type deviceInfo struct {
	DeviceID  string `json:"device_id"`
	Name      string `json:"name"`
	IPAddress string `json:"ip_address"`
	Room      string `json:"room,omitempty"`
}

func (s *server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	s.cfgMu.Lock()
	rooms := s.rooms
	s.cfgMu.Unlock()

	// Map device_id -> first room that names it.
	roomOf := map[string]string{}
	for _, room := range rooms {
		for _, id := range room.Devices {
			if _, seen := roomOf[id]; !seen {
				roomOf[id] = room.Name
			}
		}
	}

	devices := s.registry.Devices()
	out := make([]deviceInfo, 0, len(devices))
	for _, d := range devices {
		out = append(out, deviceInfo{
			DeviceID:  d.DeviceID,
			Name:      d.DisplayName(),
			IPAddress: d.IPAddress,
			Room:      roomOf[d.DeviceID],
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) handleDeviceStatus(w http.ResponseWriter, r *http.Request) {
	c := s.registry.Get(r.PathValue("id"))
	if c == nil {
		writeError(w, http.StatusNotFound, "unknown device")
		return
	}
	st, err := c.Refresh(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *server) handleDevicePower(w http.ResponseWriter, r *http.Request) {
	c := s.registry.Get(r.PathValue("id"))
	if c == nil {
		writeError(w, http.StatusNotFound, "unknown device")
		return
	}
	var body struct {
		On bool `json:"on"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeResult(w, c.SetPower(r.Context(), body.On) == nil)
}

// colorBody accepts either an RGB triple or an HSV selection (hue/sat 0-1,
// bright 0-100).
type colorBody struct {
	R *uint8 `json:"r"`
	G *uint8 `json:"g"`
	B *uint8 `json:"b"`

	Hue    *float64 `json:"hue"`
	Sat    *float64 `json:"sat"`
	Bright *float64 `json:"bright"`
}

func (s *server) handleDeviceColor(w http.ResponseWriter, r *http.Request) {
	c := s.registry.Get(r.PathValue("id"))
	if c == nil {
		writeError(w, http.StatusNotFound, "unknown device")
		return
	}
	var body colorBody
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rgb, err := body.rgb()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeResult(w, c.SetColour(r.Context(), rgb) == nil)
}

// rgb resolves the colour body to a device.RGB, converting HSV if the RGB
// triple is absent.
func (b colorBody) rgb() (device.RGB, error) {
	if b.R != nil && b.G != nil && b.B != nil {
		return device.RGB{R: *b.R, G: *b.G, B: *b.B}, nil
	}
	if b.Hue != nil && b.Sat != nil {
		v := 1.0
		if b.Bright != nil {
			v = *b.Bright / 100.0
		}
		r, g, bl := control.HSVToRGBInt(*b.Hue, *b.Sat, v)
		return device.RGB{R: r, G: g, B: bl}, nil
	}
	return device.RGB{}, fmt.Errorf("color: provide r,g,b or hue,sat[,bright]")
}

func (s *server) handleDeviceBrightness(w http.ResponseWriter, r *http.Request) {
	c := s.registry.Get(r.PathValue("id"))
	if c == nil {
		writeError(w, http.StatusNotFound, "unknown device")
		return
	}
	var body struct {
		Pct float64 `json:"pct"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeResult(w, c.SetBrightness(r.Context(), body.Pct) == nil)
}

func (s *server) handleDeviceTemperature(w http.ResponseWriter, r *http.Request) {
	c := s.registry.Get(r.PathValue("id"))
	if c == nil {
		writeError(w, http.StatusNotFound, "unknown device")
		return
	}
	var body struct {
		Pct float64 `json:"pct"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeResult(w, c.SetColourTempPercent(r.Context(), body.Pct) == nil)
}

// --- room handlers ---

// roomSummary is the resolved room shape for GET /rooms.
type roomSummary struct {
	Name    string       `json:"name"`
	Devices []deviceInfo `json:"devices"`
}

func (s *server) handleListRooms(w http.ResponseWriter, r *http.Request) {
	cfg := s.snapshotConfig()
	groups := control.GroupByRoom(cfg)
	out := make([]roomSummary, 0, len(groups))
	for _, g := range groups {
		devs := make([]deviceInfo, 0, len(g.Devices))
		for _, d := range g.Devices {
			devs = append(devs, deviceInfo{
				DeviceID:  d.DeviceID,
				Name:      d.DisplayName(),
				IPAddress: d.IPAddress,
				Room:      g.Name,
			})
		}
		out = append(out, roomSummary{Name: g.Name, Devices: devs})
	}
	writeJSON(w, http.StatusOK, out)
}

// roomControls resolves a room name to its member controls, tolerating the
// synthetic "No room" group.
func (s *server) roomControls(name string) []*control.Control {
	cfg := s.snapshotConfig()
	for _, g := range control.GroupByRoom(cfg) {
		if g.Name == name {
			ids := make([]string, 0, len(g.Devices))
			for _, d := range g.Devices {
				ids = append(ids, d.DeviceID)
			}
			return s.registry.ControlsFor(ids)
		}
	}
	return nil
}

func (s *server) handleRoomPower(w http.ResponseWriter, r *http.Request) {
	controls := s.roomControls(r.PathValue("name"))
	if controls == nil {
		writeError(w, http.StatusNotFound, "unknown room")
		return
	}
	var body struct {
		On bool `json:"on"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeResult(w, control.SetPowerMany(r.Context(), controls, body.On))
}

// --- scene handlers ---

func (s *server) handleListScenes(w http.ResponseWriter, r *http.Request) {
	s.cfgMu.Lock()
	scenes := append([]control.Scene(nil), s.scenes...)
	s.cfgMu.Unlock()
	if scenes == nil {
		scenes = []control.Scene{}
	}
	writeJSON(w, http.StatusOK, scenes)
}

func (s *server) handleSceneApply(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	s.cfgMu.Lock()
	var scene *control.Scene
	for i := range s.scenes {
		if s.scenes[i].Name == name {
			cp := s.scenes[i]
			scene = &cp
			break
		}
	}
	s.cfgMu.Unlock()
	if scene == nil {
		writeError(w, http.StatusNotFound, "unknown scene")
		return
	}
	writeResult(w, s.registry.ApplyScene(r.Context(), *scene))
}

// handleSceneCapture snapshots current live status into a scene and persists it
// (creating or overwriting by name).
func (s *server) handleSceneCapture(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	scene := s.registry.CaptureScene(r.Context(), name)

	s.cfgMu.Lock()
	replaced := false
	for i := range s.scenes {
		if s.scenes[i].Name == name {
			s.scenes[i] = scene
			replaced = true
			break
		}
	}
	if !replaced {
		s.scenes = append(s.scenes, scene)
	}
	err := s.saveConfigLocked()
	s.cfgMu.Unlock()

	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, scene)
}

func (s *server) handleSceneDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	s.cfgMu.Lock()
	next := s.scenes[:0]
	found := false
	for _, sc := range s.scenes {
		if sc.Name == name {
			found = true
			continue
		}
		next = append(next, sc)
	}
	s.scenes = append([]control.Scene(nil), next...)
	var err error
	if found {
		err = s.saveConfigLocked()
	}
	s.cfgMu.Unlock()

	if !found {
		writeError(w, http.StatusNotFound, "unknown scene")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- discovery / config handlers ---

type discoverBody struct {
	TimeoutMS int `json:"timeout_ms"`
}

func (s *server) handleDiscover(w http.ResponseWriter, r *http.Request) {
	var body discoverBody
	if r.ContentLength != 0 {
		if err := decodeBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	timeout := defaultDiscoverTimeout
	if body.TimeoutMS > 0 {
		timeout = msToDuration(body.TimeoutMS)
	}
	found, err := scanDevices(r.Context(), timeout)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, found)
}

func (s *server) handleGetConfigDevices(w http.ResponseWriter, r *http.Request) {
	s.cfgMu.Lock()
	devices := append([]control.Device(nil), s.devices...)
	s.cfgMu.Unlock()
	if devices == nil {
		devices = []control.Device{}
	}
	writeJSON(w, http.StatusOK, devices)
}

func (s *server) handlePutConfigDevices(w http.ResponseWriter, r *http.Request) {
	var devices []control.Device
	if err := decodeBody(r, &devices); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.cfgMu.Lock()
	s.devices = devices
	s.registry.Replace(devices)
	err := s.saveConfigLocked()
	s.cfgMu.Unlock()

	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, devices)
}

// --- config persistence ---

// snapshotConfig returns a copy of the current config for read-only resolution.
func (s *server) snapshotConfig() *control.Config {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	return &control.Config{
		Devices: append([]control.Device(nil), s.devices...),
		Rooms:   append([]control.Room(nil), s.rooms...),
		Scenes:  append([]control.Scene(nil), s.scenes...),
	}
}

// saveConfigLocked persists devices/rooms/scenes. The caller must hold cfgMu.
func (s *server) saveConfigLocked() error {
	return control.SaveConfig(s.configPath, s.devices, s.rooms, s.scenes)
}
