package device

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/averstraeten/notuya-go/internal/protocol"
)

// mockSession captures the arguments passed to Command so tests can inspect
// the cmd and plaintext that Device.SetDPs / Device.Status produce without
// needing real hardware.
type mockSession struct {
	// Captured from the last Command call.
	lastCmd     uint32
	lastPayload []byte
	lastWait    bool

	// Response to return from Command (set by the test before calling).
	resp []byte
	err  error
}

func (m *mockSession) Open(context.Context) error { return nil }
func (m *mockSession) Close() error               { return nil }

func (m *mockSession) Command(_ context.Context, cmd uint32, payload []byte, wait bool) ([]byte, error) {
	m.lastCmd = cmd
	m.lastPayload = append([]byte(nil), payload...) // defensive copy
	m.lastWait = wait
	return m.resp, m.err
}

// TestSetDPsPayloadShape verifies that Device.SetDPs sends cmd=CONTROL_NEW
// with the 15-byte version prefix followed by the expected JSON envelope.
func TestSetDPsPayloadShape(t *testing.T) {
	mock := &mockSession{}
	d := NewDevice(mock, "test")

	dps := map[string]any{"20": true, "21": "colour"}
	if err := d.SetDPs(context.Background(), dps, true); err != nil {
		t.Fatalf("SetDPs: %v", err)
	}

	if mock.lastCmd != protocol.ControlNew {
		t.Errorf("cmd = 0x%02x, want 0x%02x (CONTROL_NEW)", mock.lastCmd, protocol.ControlNew)
	}
	if !mock.lastWait {
		t.Error("wait = false, want true")
	}

	// First 15 bytes must be the version header: "3.5" + 12 zero bytes.
	versionPrefix := versionHeader35()
	if len(mock.lastPayload) < 15 {
		t.Fatalf("payload too short (%d bytes) for version header", len(mock.lastPayload))
	}
	if !bytes.Equal(mock.lastPayload[:15], versionPrefix) {
		t.Errorf("version header = %x, want %x", mock.lastPayload[:15], versionPrefix)
	}

	// The rest must be valid JSON with the expected structure.
	jsonPart := mock.lastPayload[15:]
	var body struct {
		Protocol int `json:"protocol"`
		T        int `json:"t"`
		Data     struct {
			DPS map[string]json.RawMessage `json:"dps"`
		} `json:"data"`
	}
	if err := json.Unmarshal(jsonPart, &body); err != nil {
		t.Fatalf("JSON part is not valid: %v\nraw: %s", err, jsonPart)
	}
	if body.Protocol != 5 {
		t.Errorf("protocol = %d, want 5", body.Protocol)
	}
	if body.T == 0 {
		t.Error("t = 0, expected a unix timestamp")
	}
	if len(body.Data.DPS) != 2 {
		t.Fatalf("dps has %d keys, want 2", len(body.Data.DPS))
	}
	if string(body.Data.DPS["20"]) != "true" {
		t.Errorf("dps[\"20\"] = %s, want true", body.Data.DPS["20"])
	}
	if string(body.Data.DPS["21"]) != `"colour"` {
		t.Errorf("dps[\"21\"] = %s, want %q", body.Data.DPS["21"], "colour")
	}
}

// TestSetValueSingleDP ensures SetValue produces a single-DP control call.
func TestSetValueSingleDP(t *testing.T) {
	mock := &mockSession{}
	d := NewDevice(mock, "test")

	if err := d.SetValue(context.Background(), "20", false, true); err != nil {
		t.Fatalf("SetValue: %v", err)
	}

	jsonPart := mock.lastPayload[15:]
	var body struct {
		Data struct {
			DPS map[string]json.RawMessage `json:"dps"`
		} `json:"data"`
	}
	if err := json.Unmarshal(jsonPart, &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(body.Data.DPS) != 1 {
		t.Errorf("dps has %d keys, want 1", len(body.Data.DPS))
	}
	if string(body.Data.DPS["20"]) != "false" {
		t.Errorf("dps[\"20\"] = %s, want false", body.Data.DPS["20"])
	}
}

// TestSetDPsFireAndForget verifies wait=false is forwarded to the session.
func TestSetDPsFireAndForget(t *testing.T) {
	mock := &mockSession{}
	d := NewDevice(mock, "test")

	if err := d.SetValue(context.Background(), "20", true, false); err != nil {
		t.Fatalf("SetValue: %v", err)
	}
	if mock.lastWait {
		t.Error("wait = true, want false")
	}
}

// TestStatusPayloadShape verifies that Device.Status sends cmd=DP_QUERY_NEW
// with a bare "{}" payload and NO version prefix.
func TestStatusPayloadShape(t *testing.T) {
	mock := &mockSession{resp: []byte(`{"dps":{"20":true}}`)}
	d := NewDevice(mock, "test")

	if _, err := d.Status(context.Background()); err != nil {
		t.Fatalf("Status: %v", err)
	}

	if mock.lastCmd != protocol.DPQueryNew {
		t.Errorf("cmd = 0x%02x, want 0x%02x (DP_QUERY_NEW)", mock.lastCmd, protocol.DPQueryNew)
	}
	if !mock.lastWait {
		t.Error("wait = false, want true")
	}

	// DP_QUERY_NEW must NOT have the version prefix — just "{}".
	if string(mock.lastPayload) != "{}" {
		t.Errorf("payload = %q, want %q", mock.lastPayload, "{}")
	}
}

// TestStatusParsesResponse verifies that Device.Status correctly parses a
// device's dps response into a map.
func TestStatusParsesResponse(t *testing.T) {
	mock := &mockSession{
		resp: []byte(`{"dps":{"20":true,"21":"colour","24":"016903e803e8"}}`),
	}
	d := NewDevice(mock, "test")

	dps, err := d.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(dps) != 3 {
		t.Fatalf("got %d dps, want 3", len(dps))
	}
	if string(dps["20"]) != "true" {
		t.Errorf("dps[\"20\"] = %s, want true", dps["20"])
	}
	if string(dps["21"]) != `"colour"` {
		t.Errorf("dps[\"21\"] = %s, want %q", dps["21"], "colour")
	}
	if string(dps["24"]) != `"016903e803e8"` {
		t.Errorf("dps[\"24\"] = %s, want %q", dps["24"], "016903e803e8")
	}
}

// TestStatusEmptyResponse verifies that an empty response yields an empty
// map (not nil, not an error).
func TestStatusEmptyResponse(t *testing.T) {
	mock := &mockSession{resp: nil}
	d := NewDevice(mock, "test")

	dps, err := d.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if dps == nil {
		t.Error("dps is nil, want empty map")
	}
	if len(dps) != 0 {
		t.Errorf("got %d dps, want 0", len(dps))
	}
}
