package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/averstraeten/notuya-go/internal/config"
	"github.com/averstraeten/notuya-go/internal/device"
	"github.com/averstraeten/notuya-go/internal/protocol35"
)

// runMusic streams colours read from stdin (one RRGGBB per line) to every
// configured device until stdin closes or the process is interrupted.
//
// This is the live-drag path, the counterpart to `notuya color`: the latter
// is a one-shot open/command/close and is subject to the bulb's own ~half
// second fade, which makes a colour picker feel laggy. Here each device
// keeps a session open for the whole run and colours go out on DP 28 with a
// short transition.
//
// Unlike forEachDevice, the streams share one stdin, so colours are
// broadcast to per-device channels: a slow or dead device must not stall
// the reader or the other bulbs.
// It reports whether every device streamed successfully, so a failing bulb
// is visible to a caller that only checks the exit status.
func runMusic(devices []config.Device, lastColorPath string) bool {
	// SIGINT is the normal way a drag ends (the caller usually pipes a
	// long-running picker into us), so treat it as a clean stop rather
	// than letting it kill the process mid-stream and leave the bulb
	// stuck in music mode.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	type target struct {
		name    string
		colours chan device.RGB
	}

	targets := make([]target, 0, len(devices))
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		failed bool
	)

	for _, d := range devices {
		name := d.Name
		if name == "" {
			name = d.DeviceID
		}
		// Buffer of one, with the producer dropping stale colours: the
		// newest colour is the only one worth delivering.
		colours := make(chan device.RGB, 1)
		targets = append(targets, target{name: name, colours: colours})

		wg.Add(1)
		go func(d config.Device, name string, colours <-chan device.RGB) {
			defer wg.Done()
			// Drain on failure so a dead device cannot leave the
			// broadcast loop blocked on a full channel.
			if err := streamDevice(ctx, d, name, colours); err != nil {
				fmt.Fprintf(os.Stderr, "FAIL: %s -> %v\n", name, err)
				mu.Lock()
				failed = true
				mu.Unlock()
				for range colours {
				}
			}
		}(d, name, colours)
	}

	scanner := bufio.NewScanner(os.Stdin)
	lastColor := ""
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		r, g, b, err := parseColor(line)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			continue
		}
		lastColor = fmt.Sprintf("%02x%02x%02x", r, g, b)
		for _, t := range targets {
			offer(t.colours, device.RGB{R: r, G: g, B: b})
		}
		if ctx.Err() != nil {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "reading stdin:", err)
	}

	for _, t := range targets {
		close(t.colours)
	}
	wg.Wait()

	// Only persist once the streams have settled the bulbs on this colour,
	// so `notuya get-color` agrees with what is actually lit. A failed
	// stream means the bulbs never reached it, so recording it would make
	// the cache lie.
	if lastColor != "" && !failed {
		if err := config.WriteLastColor(lastColorPath, lastColor); err != nil {
			fmt.Fprintln(os.Stderr, "warning: could not save last color:", err)
		}
	}
	return !failed
}

// offer delivers c to ch, discarding an undelivered older colour if one is
// still queued. It never blocks: stdin must keep being read at full speed
// even while a bulb is mid-send.
func offer(ch chan device.RGB, c device.RGB) {
	for {
		select {
		case ch <- c:
			return
		default:
		}
		select {
		case <-ch:
		default:
		}
	}
}

// streamDevice opens one session and streams colours to it for the whole
// run, then leaves the bulb on the last colour it received.
func streamDevice(ctx context.Context, d config.Device, name string, colours <-chan device.RGB) error {
	sess := protocol35.NewSession(d.IPAddress, []byte(d.LocalKey))

	openCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	if err := sess.Open(openCtx); err != nil {
		return err
	}
	defer sess.Close()

	bulb := device.NewBulb(sess, name)

	// Tap the stream to remember the last colour, so the bulb can be left
	// holding it. Reading last/haveOne is safe only after tapped is
	// drained to completion, which is what the loop below guarantees.
	var (
		last    device.RGB
		haveOne bool
	)
	tapped := make(chan device.RGB)
	go func() {
		defer close(tapped)
		for c := range colours {
			last, haveOne = c, true
			tapped <- c
		}
	}()

	streamErr := bulb.StreamColours(ctx, tapped, device.StreamOptions{})

	// Drain whatever is still queued: on an early stream error nothing
	// else would consume tapped, and its goroutine would leak blocked on
	// a send.
	for range tapped {
	}

	if streamErr != nil {
		return streamErr
	}

	if !haveOne {
		return nil
	}
	// Re-issue the final colour as a normal colour-mode write: it takes
	// the bulb out of music mode and makes the colour stick after we hang
	// up. A fresh context because ctx is already cancelled on SIGINT.
	finalCtx, cancelFinal := context.WithTimeout(context.Background(), commandTimeout)
	defer cancelFinal()
	if err := bulb.SetColour(finalCtx, last.R, last.G, last.B); err != nil {
		return fmt.Errorf("leaving music mode: %w", err)
	}
	return nil
}
