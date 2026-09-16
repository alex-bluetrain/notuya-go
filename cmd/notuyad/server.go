package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/averstraeten/notuya-go/pkg/bulb"
	"github.com/averstraeten/notuya-go/pkg/device"
)

// server holds the daemon's configuration. It keeps no per-request state:
// every handler opens its own sessions and closes them, matching the CLI's
// non-persistent connection model.
type server struct {
	devices       []Device
	lastColorPath string
	streamOpts    bulb.StreamOptions
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /devices", s.handleListDevices)
	mux.HandleFunc("GET /color", s.handleGetColor)
	mux.HandleFunc("POST /on", s.handleOn)
	mux.HandleFunc("POST /off", s.handleOff)
	mux.HandleFunc("POST /color", s.handleColor)
	mux.HandleFunc("POST /brightness", s.handleBrightness)
	mux.HandleFunc("POST /stream", s.handleStream)
	return mux
}

func (s *server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	type dev struct {
		Name string `json:"name"`
		IP   string `json:"ip_address"`
		ID   string `json:"device_id"`
	}
	out := make([]dev, 0, len(s.devices))
	for _, d := range s.devices {
		out = append(out, dev{Name: d.name(), IP: d.IPAddress, ID: d.DeviceID})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) handleGetColor(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"color": readLastColor(s.lastColorPath)})
}

func (s *server) handleOn(w http.ResponseWriter, r *http.Request) {
	s.runAll(w, r, func(ctx context.Context, b *bulb.Bulb) error { return b.TurnOn(ctx) })
}

func (s *server) handleOff(w http.ResponseWriter, r *http.Request) {
	s.runAll(w, r, func(ctx context.Context, b *bulb.Bulb) error { return b.TurnOff(ctx) })
}

func (s *server) handleColor(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 64))
	if err != nil {
		http.Error(w, "reading body", http.StatusBadRequest)
		return
	}
	rr, gg, bb, err := parseColor(strings.TrimSpace(string(body)))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ok := s.forEachDevice(r.Context(), func(ctx context.Context, b *bulb.Bulb) error {
		if err := b.TurnOn(ctx); err != nil {
			return err
		}
		return b.SetColour(ctx, rr, gg, bb)
	})
	if ok {
		if err := writeLastColor(s.lastColorPath, fmt.Sprintf("%02x%02x%02x", rr, gg, bb)); err != nil {
			logf("warning: could not save last color: %v", err)
		}
	}
	writeResult(w, ok)
}

func (s *server) handleBrightness(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 16))
	if err != nil {
		http.Error(w, "reading body", http.StatusBadRequest)
		return
	}
	pct, err := strconv.ParseFloat(strings.TrimSpace(string(body)), 64)
	if err != nil || pct < 0 || pct > 100 {
		http.Error(w, "brightness: want 0-100", http.StatusBadRequest)
		return
	}
	ok := s.forEachDevice(r.Context(), func(ctx context.Context, b *bulb.Bulb) error {
		if err := b.TurnOn(ctx); err != nil {
			return err
		}
		mode, err := b.Raw().GetMode(ctx)
		if err != nil {
			return err
		}
		if mode == device.ModeColour {
			return b.SetColourBrightness(ctx, pct)
		}
		return b.SetWhiteBrightness(ctx, pct)
	})
	writeResult(w, ok)
}

// handleStream consumes a chunked request body of "RRGGBB [TT]" lines and
// streams them to every device for the life of the body. The connection
// stays open for the whole drag; when the client closes the body, every
// bulb is left on the final colour (music mode exited, colour persisted).
func (s *server) handleStream(w http.ResponseWriter, r *http.Request) {
	// r.Context() is cancelled when the client hangs up, which is what
	// ends the stream on a dropped connection.
	lastColor, ok := streamColours(r.Context(), s.devices, r.Body, s.streamOpts)

	// Persist only a clean run's final colour, so get-color matches what
	// is actually lit.
	if lastColor != "" && ok {
		if err := writeLastColor(s.lastColorPath, lastColor); err != nil {
			logf("warning: could not save last color: %v", err)
		}
	}
	writeResult(w, ok)
}

func (s *server) runAll(w http.ResponseWriter, r *http.Request, fn func(context.Context, *bulb.Bulb) error) {
	writeResult(w, s.forEachDevice(r.Context(), fn))
}

func writeResult(w http.ResponseWriter, ok bool) {
	if ok {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	writeJSON(w, http.StatusBadGateway, map[string]string{"status": "error"})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
