package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/coder/websocket"

	"github.com/alex-bluetrain/notuya-go/pkg/control"
	"github.com/alex-bluetrain/notuya-go/pkg/device"
)

// handleDeviceStream is the WebSocket live colour drag for one device. The
// client sends text frames "RRGGBB [TT]" during the drag; each frame is fed
// into the device's music-mode streamer (newest-wins). When the socket closes
// the bulb is left on the last streamed colour.
func (s *server) handleDeviceStream(w http.ResponseWriter, r *http.Request) {
	c := s.registry.Get(r.PathValue("id"))
	if c == nil {
		writeError(w, http.StatusNotFound, "unknown device")
		return
	}
	conn, err := acceptWS(w, r)
	if err != nil {
		return
	}

	seed := parseHexRGB(readLastColor(s.lastColorPath))
	c.BeginLiveDrag(seed)
	defer c.EndLiveDrag()

	last := runDrag(r.Context(), conn, func(rgb device.RGB, tt *int) {
		c.UpdateLiveDrag(rgb, tt)
	})
	if last != nil {
		_ = writeLastColor(s.lastColorPath, rgbToHex(*last))
	}
}

// handleRoomStream is the WebSocket live colour drag fanned out to a room.
func (s *server) handleRoomStream(w http.ResponseWriter, r *http.Request) {
	controls := s.roomControls(r.PathValue("name"))
	if controls == nil {
		writeError(w, http.StatusNotFound, "unknown room")
		return
	}
	conn, err := acceptWS(w, r)
	if err != nil {
		return
	}

	seed := parseHexRGB(readLastColor(s.lastColorPath))
	drag := control.BeginLiveDrag(controls, seed)
	defer drag.Close()

	last := runDrag(r.Context(), conn, func(rgb device.RGB, tt *int) {
		drag.Update(rgb, tt)
	})
	if last != nil {
		_ = writeLastColor(s.lastColorPath, rgbToHex(*last))
	}
}

// acceptWS upgrades the request, permitting any origin (trusted LAN, no auth).
func acceptWS(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	return websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // any origin; trusted LAN only
	})
}

// runDrag reads text frames until the socket closes, feeding each parsed colour
// into apply. It returns the last colour seen (or nil if none), so the caller
// can persist it as the new last-colour.
func runDrag(ctx context.Context, conn *websocket.Conn, apply func(device.RGB, *int)) *device.RGB {
	var last *device.RGB
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			// Normal closure or client hang-up ends the drag.
			var ce websocket.CloseError
			if errors.As(err, &ce) || errors.Is(err, context.Canceled) {
				conn.Close(websocket.StatusNormalClosure, "")
				return last
			}
			conn.Close(websocket.StatusProtocolError, "read error")
			return last
		}
		if typ != websocket.MessageText {
			continue
		}
		rgb, tt, ok := parseStreamFrame(string(data))
		if !ok {
			continue
		}
		apply(rgb, tt)
		cp := rgb
		last = &cp
	}
}

// parseStreamFrame parses a "RRGGBB [TT]" text frame: a 6-hex-digit colour and
// an optional transition (0-10).
func parseStreamFrame(line string) (device.RGB, *int, bool) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return device.RGB{}, nil, false
	}
	rgb, ok := parseHex6(fields[0])
	if !ok {
		return device.RGB{}, nil, false
	}
	if len(fields) >= 2 {
		if tt, err := strconv.Atoi(fields[1]); err == nil {
			if tt < 0 {
				tt = 0
			}
			if tt > device.MaxTransition {
				tt = device.MaxTransition
			}
			return rgb, &tt, true
		}
	}
	return rgb, nil, true
}

// parseHex6 parses a bare 6-hex-digit colour, tolerating a leading '#'.
func parseHex6(s string) (device.RGB, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 {
		return device.RGB{}, false
	}
	r, e1 := strconv.ParseUint(s[0:2], 16, 8)
	g, e2 := strconv.ParseUint(s[2:4], 16, 8)
	b, e3 := strconv.ParseUint(s[4:6], 16, 8)
	if e1 != nil || e2 != nil || e3 != nil {
		return device.RGB{}, false
	}
	return device.RGB{R: uint8(r), G: uint8(g), B: uint8(b)}, true
}

// parseHexRGB parses a hex colour for seeding, falling back to white.
func parseHexRGB(s string) device.RGB {
	if rgb, ok := parseHex6(s); ok {
		return rgb
	}
	return device.RGB{R: 255, G: 255, B: 255}
}

func rgbToHex(rgb device.RGB) string {
	const hex = "0123456789abcdef"
	buf := []byte{
		hex[rgb.R>>4], hex[rgb.R&0xf],
		hex[rgb.G>>4], hex[rgb.G&0xf],
		hex[rgb.B>>4], hex[rgb.B&0xf],
	}
	return string(buf)
}
