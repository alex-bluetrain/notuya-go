package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alex-bluetrain/notuya-go/pkg/bulb"
)

// shortTimeout keeps these tests fast: they only need the dial to fail, not
// to wait out the CLI's real 10s budget.
func shortTimeout(t *testing.T) {
	t.Helper()
	previous := commandTimeout
	commandTimeout = 200 * time.Millisecond
	t.Cleanup(func() { commandTimeout = previous })
}

// 192.0.2.0/24 is TEST-NET-1 (RFC 5737): reserved for documentation and
// guaranteed not to route, so these never touch a real device.
func unreachable(n int) []Device {
	devices := make([]Device, n)
	for i := range devices {
		devices[i] = Device{
			DeviceID:  "unreachable",
			IPAddress: "192.0.2.1",
			LocalKey:  "0123456789abcdef",
			Name:      "unreachable",
		}
	}
	return devices
}

// A failing device must be visible in the exit status, not just on stderr.
// forEachDevice used to swallow every failure, so `notuya on` exited 0 with
// FAIL printed for every bulb — the shape of failure a script cannot see.
func TestForEachDeviceReportsFailure(t *testing.T) {
	shortTimeout(t)

	called := false
	ok := forEachDevice(unreachable(1), func(ctx context.Context, b *bulb.Bulb) error {
		called = true
		return nil
	})
	if ok {
		t.Error("forEachDevice = true for an unreachable device, want false")
	}
	if called {
		t.Error("fn ran despite the session never opening")
	}
}

// One bad device must not hide the others' outcome, nor stop them running.
func TestForEachDeviceFailsIfAnyDeviceFails(t *testing.T) {
	shortTimeout(t)

	ok := forEachDevice(unreachable(3), func(ctx context.Context, b *bulb.Bulb) error {
		return errors.New("unused: the session never opens")
	})
	if ok {
		t.Error("forEachDevice = true when every device failed, want false")
	}
}

func TestForEachDeviceSucceedsWithNoDevices(t *testing.T) {
	if !forEachDevice(nil, func(ctx context.Context, b *bulb.Bulb) error {
		return nil
	}) {
		t.Error("forEachDevice = false for an empty device list, want true")
	}
}
