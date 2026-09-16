// Command notuyad is an HTTP daemon for controlling Tuya local-protocol
// v3.5 bulbs. It exposes the same operations as the notuya CLI over a small
// cross-platform HTTP API, plus a chunked-body /stream endpoint whose
// request body carries a live colour stream (RRGGBB [TT] lines) for the
// duration of a colour-picker drag — the equivalent of piping colours into
// `notuya music`, without spawning a subprocess.
//
// It binds loopback by default and keeps no persistent bulb sessions
// between requests: one-shot endpoints open/command/close per request, and
// /stream owns a session only for the life of its body, mirroring the CLI.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/averstraeten/notuya-go/pkg/bulb"
	"github.com/averstraeten/notuya-go/pkg/device"
	"github.com/averstraeten/notuya-go/pkg/protocol35"
)

// commandTimeout bounds one device's whole open→command→close cycle. A var
// so tests can shorten it; nothing at runtime reassigns it.
var commandTimeout = 10 * time.Second

func logf(format string, args ...any) { log.Printf(format, args...) }

func main() {
	configPath := flag.String("config", "", "path to config.json (default: $NOTUYA_CONFIG, or the user config dir)")
	addr := flag.String("addr", "127.0.0.1:8765", "address to listen on (use 127.0.0.1:0 for an ephemeral port)")
	transition := flag.Int("transition", device.DefaultTransition, "stream: default per-update fade length, 0-10")
	interval := flag.Duration("interval", bulb.DefaultStreamInterval, "stream: minimum spacing between colour updates")
	flag.Parse()

	if *transition < 0 || *transition > device.MaxTransition {
		fmt.Fprintf(os.Stderr, "transition: must be 0-%d, got %d\n", device.MaxTransition, *transition)
		os.Exit(1)
	}

	path := resolveConfigPath(*configPath)
	cfg, err := loadConfig(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(cfg.Devices) == 0 {
		fmt.Fprintln(os.Stderr, "no devices configured in", path)
		os.Exit(1)
	}

	srv := &server{
		devices:       cfg.Devices,
		lastColorPath: filepath.Join(filepath.Dir(path), "last-color.txt"),
		streamOpts:    bulb.StreamOptions{Interval: *interval, Transition: transition},
	}

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "listen:", err)
		os.Exit(1)
	}

	// Announce the resolved address next to config, so a client that asked
	// for an ephemeral port (127.0.0.1:0) can discover the real one.
	addrFile := filepath.Join(filepath.Dir(path), "notuyad.addr")
	if err := os.WriteFile(addrFile, []byte(ln.Addr().String()), 0o644); err != nil {
		logf("warning: could not write %s: %v", addrFile, err)
	}
	defer os.Remove(addrFile)

	log.Printf("notuyad listening on http://%s (%d device(s))", ln.Addr(), len(cfg.Devices))
	if err := http.Serve(ln, srv.routes()); err != nil {
		fmt.Fprintln(os.Stderr, "serve:", err)
		os.Exit(1)
	}
}

// forEachDevice fans out fn to every device concurrently, isolating
// failures per device — mirrors the CLI's forEachDevice, including the
// Status() connectivity warm-up before the real command. It returns whether
// every device succeeded.
func (s *server) forEachDevice(ctx context.Context, fn func(context.Context, *bulb.Bulb) error) bool {
	var wg sync.WaitGroup
	var mu sync.Mutex
	failed := 0

	for _, d := range s.devices {
		wg.Add(1)
		go func(d Device) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, commandTimeout)
			defer cancel()

			sess := protocol35.NewSession(d.IPAddress, []byte(d.LocalKey))
			if err := sess.Open(cctx); err != nil {
				mu.Lock()
				failed++
				mu.Unlock()
				logf("FAIL %s: %v", d.name(), err)
				return
			}
			defer sess.Close()

			b := bulb.NewBulb(sess, d.name())
			if err := b.Status(cctx); err != nil {
				mu.Lock()
				failed++
				mu.Unlock()
				logf("FAIL %s: %v", d.name(), err)
				return
			}
			if err := fn(cctx, b); err != nil {
				mu.Lock()
				failed++
				mu.Unlock()
				logf("FAIL %s: %v", d.name(), err)
			}
		}(d)
	}
	wg.Wait()
	return failed == 0
}
