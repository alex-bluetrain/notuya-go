package v35

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alex-bluetrain/notuya-go/pkg/bulb"
	"github.com/alex-bluetrain/notuya-go/pkg/dp"
)

// TestBulbEndToEnd runs bulb's streaming loop and verbs over a real
// session against the fake device: a real handshake, real 6699 frames, and
// the reader goroutine absorbing pushes while colours stream. It lives here
// because the fake device is this package's test fixture; bulb does not
// import session/v35, so there is no cycle.
func TestBulbEndToEnd(t *testing.T) {
	d := newFakeDevice(t)
	d.silentControl = true // a streaming bulb stops acknowledging
	s := open(t, d, Options{})
	b := bulb.New(s, "e2e")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	watch := b.Watch(ctx)

	colours := make(chan bulb.StreamColour)
	done := make(chan error, 1)
	go func() {
		done <- b.StreamColours(ctx, colours, bulb.StreamOptions{
			Interval:   5 * time.Millisecond,
			ChangeMode: dp.ChangeFade.Ptr(),
		})
	}()
	for i, c := range []dp.RGB{{R: 255}, {G: 255}, {B: 255}} {
		colours <- bulb.StreamColour{RGB: c}
		if i == 1 {
			// The bulb reports something mid-stream; it must reach
			// Watch without disturbing the stream.
			d.sendPush(`{"dps":{"21":"music"}}`)
		}
		time.Sleep(20 * time.Millisecond)
	}
	close(colours)
	if err := <-done; err != nil {
		t.Fatalf("StreamColours: %v", err)
	}

	select {
	case st := <-watch:
		if st.Mode != dp.ModeMusic {
			t.Errorf("watched state = %+v, want music mode", st)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("push during stream never reached Watch")
	}

	frames := d.frames()
	if frames[0].Cmd != cmdQuery {
		t.Errorf("first frame code %d, want the warm-up query", frames[0].Cmd)
	}
	var last string
	for _, f := range frames {
		if f.Cmd != cmdControl {
			continue
		}
		var env struct {
			Data struct {
				DPS map[string]string `json:"dps"`
			} `json:"data"`
		}
		if err := json.Unmarshal(jsonObject(f.Payload), &env); err != nil {
			t.Fatal(err)
		}
		last = env.Data.DPS["28"]
	}
	if want := "1" + "00f003e803e8" + "00000000"; last != want {
		t.Errorf("last streamed DP 28 = %q, want %q (blue)", last, want)
	}

	// The session is still good for ordinary commands after the stream.
	st, err := b.Status(ctx)
	if err != nil || !st.On || st.Brightness != 500 {
		t.Fatalf("status after stream = %+v, %v", st, err)
	}
}
