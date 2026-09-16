package main

import (
	"testing"

	"github.com/averstraeten/notuya-go/pkg/bulb"
	"github.com/averstraeten/notuya-go/pkg/device"
)

// TestOfferKeepsNewest is the core guarantee of the stdin broadcast: a
// stalled device must never make the reader block, and when it catches up
// it should get the current colour, not a backlog of stale ones.
func TestOfferKeepsNewest(t *testing.T) {
	ch := make(chan bulb.StreamColour, 1)

	for i := range 100 {
		offer(ch, bulb.StreamColour{RGB: device.RGB{R: uint8(i)}})
	}

	if len(ch) != 1 {
		t.Fatalf("queued %d colours, want 1", len(ch))
	}
	if got := <-ch; got.RGB.R != 99 {
		t.Errorf("queued colour R = %d, want 99 (the newest)", got.RGB.R)
	}
}

// TestOfferDeliversToWaitingConsumer guards against offer dropping a colour
// that a ready consumer could have taken.
func TestOfferDeliversToWaitingConsumer(t *testing.T) {
	ch := make(chan bulb.StreamColour, 1)
	offer(ch, bulb.StreamColour{RGB: device.RGB{R: 7, G: 8, B: 9}})

	select {
	case got := <-ch:
		if got.RGB != (device.RGB{R: 7, G: 8, B: 9}) {
			t.Errorf("got %+v, want {7 8 9}", got.RGB)
		}
	default:
		t.Fatal("offer delivered nothing to an empty channel")
	}
}
