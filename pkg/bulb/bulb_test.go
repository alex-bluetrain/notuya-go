package bulb

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/alex-bluetrain/notuya-go/pkg/dp"
	"github.com/alex-bluetrain/notuya-go/pkg/session"
)

// call is one recorded session request.
type call struct {
	kind string // "query", "control", "refresh", "heartbeat"
	body []byte
	wait bool
}

// mockSession is a session.Session that records every request.
type mockSession struct {
	mu        sync.Mutex
	calls     []call
	queryResp string
	pushes    chan session.Push
	done      chan struct{}
	err       error
}

func newMock() *mockSession {
	return &mockSession{
		queryResp: `{"dps":{"20":true,"21":"white","22":500}}`,
		pushes:    make(chan session.Push, 8),
		done:      make(chan struct{}),
	}
}

func (m *mockSession) record(c call) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, c)
}

func (m *mockSession) Query(context.Context) ([]byte, error) {
	m.record(call{kind: "query", wait: true})
	return []byte(m.queryResp), nil
}

func (m *mockSession) Control(_ context.Context, body []byte, wait bool) error {
	m.record(call{kind: "control", body: append([]byte(nil), body...), wait: wait})
	return nil
}

func (m *mockSession) Refresh(_ context.Context, ids []int) error {
	b, _ := json.Marshal(ids)
	m.record(call{kind: "refresh", body: b})
	return nil
}

func (m *mockSession) Heartbeat(_ context.Context, wait bool) error {
	m.record(call{kind: "heartbeat", wait: wait})
	return nil
}

func (m *mockSession) Pushes() <-chan session.Push { return m.pushes }
func (m *mockSession) Done() <-chan struct{}       { return m.done }
func (m *mockSession) Err() error                  { return m.err }
func (m *mockSession) Close() error                { return nil }

func (m *mockSession) recorded() []call {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.calls)
}

// controls returns the data.dps of every Control body, in order.
func (m *mockSession) controls(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, c := range m.recorded() {
		if c.kind != "control" {
			continue
		}
		var env struct {
			Data struct {
				DPS map[string]any `json:"dps"`
			} `json:"data"`
		}
		if err := json.Unmarshal(c.body, &env); err != nil {
			t.Fatalf("control body %q: %v", c.body, err)
		}
		out = append(out, env.Data.DPS)
	}
	return out
}

func lastControl(t *testing.T, m *mockSession) map[string]any {
	t.Helper()
	cs := m.controls(t)
	if len(cs) == 0 {
		t.Fatal("no control was sent")
	}
	return cs[len(cs)-1]
}

func TestVerbsWriteTheDocumentedDPs(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		do   func(*Bulb) error
		want map[string]any
	}{
		{"TurnOn", func(b *Bulb) error { return b.TurnOn(ctx) }, map[string]any{"20": true}},
		{"TurnOff", func(b *Bulb) error { return b.TurnOff(ctx) }, map[string]any{"20": false}},
		{"SetColour", func(b *Bulb) error { return b.SetColour(ctx, dp.RGB{R: 255}) },
			map[string]any{"21": "colour", "24": "000003e803e8"}},
		{"SetWhiteBrightness 0 clamps to 1%", func(b *Bulb) error { return b.SetWhiteBrightness(ctx, 0) },
			map[string]any{"21": "white", "22": float64(10)}},
		{"SetColourTempPercent", func(b *Bulb) error { return b.SetColourTempPercent(ctx, 50) },
			map[string]any{"21": "white", "23": float64(500)}},
		{"SetTimer", func(b *Bulb) error { return b.SetTimer(ctx, 120) }, map[string]any{"26": float64(120)}},
		{"SetDoNotDisturb", func(b *Bulb) error { return b.SetDoNotDisturb(ctx, true) }, map[string]any{"34": true}},
		{"Set", func(b *Bulb) error { return b.Set(ctx, dp.Values{dp.ColourTemp: 7}) }, map[string]any{"23": float64(7)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMock()
			if err := tc.do(New(m, "t")); err != nil {
				t.Fatal(err)
			}
			got := lastControl(t, m)
			if len(got) != len(tc.want) {
				t.Fatalf("dps = %v, want %v", got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("DP %s = %v, want %v", k, got[k], v)
				}
			}
			if !m.recorded()[0].wait {
				t.Error("domain verbs must wait for the acknowledgement")
			}
		})
	}
}

func TestOutOfRangeNeverReachesTheWire(t *testing.T) {
	m := newMock()
	b := New(m, "t")
	if err := b.SetTimer(context.Background(), dp.TimerMax+1); err == nil {
		t.Fatal("SetTimer accepted an out-of-range value")
	}
	if len(m.recorded()) != 0 {
		t.Fatalf("invalid write reached the session: %v", m.recorded())
	}
}

func TestStatusSelectsLegacySchema(t *testing.T) {
	m := newMock()
	m.queryResp = `{"dps":{"1":true,"2":"white","3":200}}`
	b := New(m, "t")
	st, err := b.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !st.Schema.Legacy || st.Brightness != 200 {
		t.Fatalf("state = %+v", st)
	}
	if err := b.TurnOn(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := lastControl(t, m); got["1"] != true {
		t.Errorf("legacy bulb power write = %v, want DP 1", got)
	}
}

func TestCapabilities(t *testing.T) {
	ids, err := New(newMock(), "t").Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []dp.ID{dp.Switch, dp.Mode, dp.Brightness}; !slices.Equal(ids, want) {
		t.Errorf("capabilities = %v, want %v", ids, want)
	}
}

func TestRefreshSendsDPNumbers(t *testing.T) {
	m := newMock()
	if err := New(m, "t").Refresh(context.Background(), dp.Brightness, dp.Colour); err != nil {
		t.Fatal(err)
	}
	if got := string(m.recorded()[0].body); got != "[22,24]" {
		t.Errorf("refresh ids = %s", got)
	}
}

func TestWatchDecodesPushesAndSkipsJunk(t *testing.T) {
	m := newMock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	states := New(m, "t").Watch(ctx)

	m.pushes <- session.Push{Body: []byte(`not json`)}
	m.pushes <- session.Push{Body: []byte(`{"data":{"dps":{"22":300}}}`)}
	select {
	case st := <-states:
		if st.Brightness != 300 || !st.Has(dp.Brightness) || st.Has(dp.Switch) {
			t.Errorf("state = %+v", st)
		}
	case <-time.After(time.Second):
		t.Fatal("no state delivered")
	}

	close(m.pushes)
	select {
	case _, ok := <-states:
		if ok {
			t.Fatal("unexpected state")
		}
	case <-time.After(time.Second):
		t.Fatal("Watch did not end with the session")
	}
}

func TestStreamEndsWhenSessionDies(t *testing.T) {
	m := newMock()
	m.err = errors.New("link down")
	colours := make(chan StreamColour)
	go func() { close(m.done) }()
	err := New(m, "t").StreamColours(context.Background(), colours, StreamOptions{})
	if err == nil || !errors.Is(err, m.err) {
		t.Fatalf("err = %v, want the session's error", err)
	}
}
