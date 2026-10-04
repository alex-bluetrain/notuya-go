package bulb

import (
	"context"
	"testing"
	"time"

	"github.com/alex-bluetrain/notuya-go/pkg/dp"
)

// adjustHex is the DP 28 value expected for a colour sent with mode.
func adjustHex(t *testing.T, mode dp.ChangeMode, c dp.RGB) string {
	t.Helper()
	h, err := dp.Adjust{Mode: mode, Colour: dp.HSVFromRGB(c)}.Hex()
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func stream(t *testing.T, m *mockSession, cs []StreamColour, opts StreamOptions) {
	t.Helper()
	colours := make(chan StreamColour, len(cs))
	for _, c := range cs {
		colours <- c
	}
	close(colours)
	if opts.Interval <= 0 {
		opts.Interval = time.Millisecond
	}
	if err := New(m, "t").StreamColours(context.Background(), colours, opts); err != nil {
		t.Fatalf("StreamColours: %v", err)
	}
}

// TestStreamColoursHonoursJump guards the reason ChangeMode is a pointer:
// its zero value (ChangeJump) is a meaningful setting, so it must reach the
// wire instead of being read as "unset" and replaced by the default (fade).
func TestStreamColoursHonoursJump(t *testing.T) {
	m := newMock()
	stream(t, m, []StreamColour{{RGB: dp.RGB{R: 255}}}, StreamOptions{ChangeMode: dp.ChangeJump.Ptr()})
	if got, want := lastControl(t, m)["28"], adjustHex(t, dp.ChangeJump, dp.RGB{R: 255}); got != want {
		t.Errorf("DP 28 = %v, want %q (jump must survive)", got, want)
	}
}

// TestStreamColoursPayloadShape checks the on-the-wire shape of a stream:
// a blocking warm-up query first, then DP 28 writes that do not wait and
// never touch the mode DP.
func TestStreamColoursPayloadShape(t *testing.T) {
	m := newMock()
	stream(t, m, []StreamColour{{RGB: dp.RGB{R: 255}}}, StreamOptions{ChangeMode: dp.ChangeFade.Ptr()})

	calls := m.recorded()
	if len(calls) < 2 || calls[0].kind != "query" {
		t.Fatalf("calls = %+v, want a warm-up query then colours", calls)
	}
	for _, c := range calls[1:] {
		if c.kind == "control" && c.wait {
			t.Error("colour send waits; the bulb stops acking mid-stream")
		}
	}
	for _, dps := range m.controls(t) {
		if _, ok := dps["21"]; ok {
			t.Errorf("stream writes DP 21 (%v); a stream writes DP 28 only", dps)
		}
	}
	got := lastControl(t, m)
	if want := adjustHex(t, dp.ChangeFade, dp.RGB{R: 255}); got["28"] != want || len(got) != 1 {
		t.Errorf("dps = %v, want only DP 28 = %q", got, want)
	}
}

// TestStreamColoursCoalesces verifies the newest-wins throttle: colours
// arriving faster than the interval must not queue up behind each other.
func TestStreamColoursCoalesces(t *testing.T) {
	m := newMock()
	colours := make(chan StreamColour)
	done := make(chan error, 1)
	go func() {
		done <- New(m, "t").StreamColours(context.Background(), colours, StreamOptions{
			Interval:   50 * time.Millisecond,
			ChangeMode: dp.ChangeFade.Ptr(),
		})
	}()
	for i := range 10 {
		colours <- StreamColour{RGB: dp.RGB{R: uint8(i)}}
	}
	close(colours)
	if err := <-done; err != nil {
		t.Fatalf("StreamColours: %v", err)
	}

	cs := m.controls(t)
	if len(cs) == 0 || len(cs) >= 10 {
		t.Fatalf("sent %d colours for a 10-colour burst inside one interval; expected coalescing", len(cs))
	}
	if got, want := cs[len(cs)-1]["28"], adjustHex(t, dp.ChangeFade, dp.RGB{R: 9}); got != want {
		t.Errorf("last colour = %v, want %q (the newest of the burst)", got, want)
	}
}

// TestStreamColoursPerColourChangeMode checks that a StreamColour carrying
// its own change mode overrides the run default, while one that leaves it
// nil falls back to StreamOptions.ChangeMode.
func TestStreamColoursPerColourChangeMode(t *testing.T) {
	opts := StreamOptions{ChangeMode: dp.ChangeFade.Ptr()}

	m := newMock()
	stream(t, m, []StreamColour{{RGB: dp.RGB{R: 255}, ChangeMode: dp.ChangeJump.Ptr()}}, opts)
	if got, want := lastControl(t, m)["28"], adjustHex(t, dp.ChangeJump, dp.RGB{R: 255}); got != want {
		t.Errorf("per-colour change mode: DP 28 = %v, want %q", got, want)
	}

	m = newMock()
	stream(t, m, []StreamColour{{RGB: dp.RGB{G: 255}}}, opts)
	if got, want := lastControl(t, m)["28"], adjustHex(t, dp.ChangeFade, dp.RGB{G: 255}); got != want {
		t.Errorf("nil change mode: DP 28 = %v, want %q", got, want)
	}
}
