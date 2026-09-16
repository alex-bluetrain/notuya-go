package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/averstraeten/notuya-go/pkg/bulb"
	"github.com/averstraeten/notuya-go/pkg/device"
	"github.com/averstraeten/notuya-go/pkg/protocol35"
)

// streamColours reads "RRGGBB [TT]" lines from r and streams them to every
// device for the whole life of r, then leaves each bulb on the last colour.
//
// This is the daemon counterpart to cmd/notuya's runMusic: the only change
// is the colour source. runMusic reads os.Stdin; here r is the chunked body
// of an HTTP request, which stays open for the whole drag and closes when
// the client stops sending — exactly the lifecycle a subprocess's stdin had.
//
// It returns the last colour sent (hex, no '#') and whether every device
// streamed successfully, so the caller can persist the cache and set the
// HTTP status.
func streamColours(ctx context.Context, devices []Device, r io.Reader, opts bulb.StreamOptions) (lastColor string, ok bool) {
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
		// Buffer of one, producer drops stale colours: the newest colour
		// is the only one worth delivering.
		colours := make(chan bulb.StreamColour, 1)
		targets = append(targets, target{name: d.name(), colours: colours})

		wg.Add(1)
		go func(d Device, colours <-chan bulb.StreamColour) {
			defer wg.Done()
			if err := streamDevice(ctx, d, colours, opts); err != nil {
				logf("FAIL stream %s: %v", d.name(), err)
				mu.Lock()
				failed = true
				mu.Unlock()
				// Drain so a dead device cannot leave the broadcast
				// loop blocked on a full channel.
				for range colours {
				}
			}
		}(d, colours)
	}

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// "RRGGBB" or "RRGGBB TT": the optional second token is a per-line
		// transition (0-10) so a picker's transition slider takes effect
		// live, without reopening the stream.
		colorTok, transTok, _ := strings.Cut(line, " ")
		rr, gg, bb, err := parseColor(colorTok)
		if err != nil {
			logf("%v", err)
			continue
		}
		var transition *int
		if transTok = strings.TrimSpace(transTok); transTok != "" {
			t, err := strconv.Atoi(transTok)
			if err != nil || t < 0 || t > device.MaxTransition {
				logf("stream: invalid transition %q (want 0-%d)", transTok, device.MaxTransition)
				continue
			}
			transition = &t
		}
		lastColor = fmt.Sprintf("%02x%02x%02x", rr, gg, bb)
		for _, t := range targets {
			offer(t.colours, bulb.StreamColour{RGB: device.RGB{R: rr, G: gg, B: bb}, Transition: transition})
		}
		if ctx.Err() != nil {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		logf("stream: reading body: %v", err)
	}

	for _, t := range targets {
		close(t.colours)
	}
	wg.Wait()

	return lastColor, !failed
}

// offer delivers c to ch, discarding an undelivered older colour if one is
// still queued. It never blocks, so the body reader keeps up even while a
// bulb is mid-send.
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
// run, then leaves the bulb on the last colour it received (a normal
// colour-mode write, which exits music mode and persists the colour).
func streamDevice(ctx context.Context, d Device, colours <-chan bulb.StreamColour, opts bulb.StreamOptions) error {
	sess := protocol35.NewSession(d.IPAddress, []byte(d.LocalKey))

	openCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	if err := sess.Open(openCtx); err != nil {
		return err
	}
	defer sess.Close()

	b := bulb.NewBulb(sess, d.name())

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
	// else consumes tapped, and its goroutine would leak blocked on send.
	for range tapped {
	}

	if streamErr != nil {
		return streamErr
	}
	if !haveOne {
		return nil
	}

	// A fresh context because ctx may be cancelled once the body closed.
	finalCtx, cancelFinal := context.WithTimeout(context.Background(), commandTimeout)
	defer cancelFinal()
	if err := b.SetColour(finalCtx, last.RGB.R, last.RGB.G, last.RGB.B); err != nil {
		return fmt.Errorf("leaving music mode: %w", err)
	}
	return nil
}
