package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/averstraeten/notuya-go/internal/bulb"
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
func runMusic(devices []Device, lastColorPath string, opts bulb.StreamOptions) bool {
	// SIGINT is the normal way a drag ends (the caller usually pipes a
	// long-running picker into us), so treat it as a clean stop rather
	// than letting it kill the process mid-stream and leave the bulb
	// stuck in music mode.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	type target struct {
		name    string
		colours chan bulb.StreamColour
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
		colours := make(chan bulb.StreamColour, 1)
		targets = append(targets, target{name: name, colours: colours})

		wg.Add(1)
		go func(d Device, name string, colours <-chan bulb.StreamColour, opts bulb.StreamOptions) {
			defer wg.Done()
			// Drain on failure so a dead device cannot leave the
			// broadcast loop blocked on a full channel.
			if err := streamDevice(ctx, d, name, colours, opts); err != nil {
				fmt.Fprintf(os.Stderr, "FAIL: %s -> %v\n", name, err)
				mu.Lock()
				failed = true
				mu.Unlock()
				for range colours {
				}
			}
		}(d, name, colours, opts)
	}

	scanner := bufio.NewScanner(os.Stdin)
	lastColor := ""
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		// A stdin line is "RRGGBB" or "RRGGBB TT": the optional second
		// token is a per-line transition (0-10) so a picker's transition
		// slider takes effect live, without relaunching the process.
		colorTok, transTok, _ := strings.Cut(strings.TrimSpace(line), " ")
		r, g, b, err := parseColor(colorTok)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			continue
		}
		var transition *int
		if transTok = strings.TrimSpace(transTok); transTok != "" {
			t, err := strconv.Atoi(transTok)
			if err != nil || t < 0 || t > device.MaxTransition {
				fmt.Fprintf(os.Stderr, "music: invalid transition %q (want 0-%d)\n", transTok, device.MaxTransition)
				continue
			}
			transition = &t
		}
		lastColor = fmt.Sprintf("%02x%02x%02x", r, g, b)
		for _, t := range targets {
			offer(t.colours, bulb.StreamColour{RGB: device.RGB{R: r, G: g, B: b}, Transition: transition})
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
		if err := writeLastColor(lastColorPath, lastColor); err != nil {
			fmt.Fprintln(os.Stderr, "warning: could not save last color:", err)
		}
	}
	return !failed
}

// offer delivers c to ch, discarding an undelivered older colour if one is
// still queued. It never blocks: stdin must keep being read at full speed
// even while a bulb is mid-send.
func offer(ch chan bulb.StreamColour, c bulb.StreamColour) {
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
func streamDevice(ctx context.Context, d Device, name string, colours <-chan bulb.StreamColour, opts bulb.StreamOptions) error {
	sess := protocol35.NewSession(d.IPAddress, []byte(d.LocalKey))

	openCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	if err := sess.Open(openCtx); err != nil {
		return err
	}
	defer sess.Close()

	b := bulb.NewBulb(sess, name)

	// Tap the stream to remember the last colour, so the bulb can be left
	// holding it. Reading last/haveOne is safe only after tapped is
	// drained to completion, which is what the loop below guarantees.
	var (
		last    bulb.StreamColour
		haveOne bool
	)
	tapped := make(chan bulb.StreamColour)
	go func() {
		defer close(tapped)
		for c := range colours {
			last, haveOne = c, true
			tapped <- c
		}
	}()

	streamErr := b.StreamColours(ctx, tapped, opts)

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
	if err := b.SetColour(finalCtx, last.RGB.R, last.RGB.G, last.RGB.B); err != nil {
		return fmt.Errorf("leaving music mode: %w", err)
	}
	return nil
}
