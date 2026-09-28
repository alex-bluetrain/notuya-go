// Command notuyad is the HTTP/WebSocket backend for the notuya mobile app.
// It exposes the full per-device feature set of the notuya GUI — power,
// colour, brightness, colour temperature, live colour drag, rooms, scenes,
// discovery and config editing — over a resourceful JSON API keyed by
// device_id, with a WebSocket transport for the live colour drag.
//
// The daemon owns all Tuya protocol work: it keeps one persistent per-device
// session (see pkg/control) and the mobile client only ever speaks HTTP/JSON
// and WebSocket to it. It binds the LAN with no authentication (trusted LAN
// only) and advertises itself over mDNS as _notuyad._tcp so the app can find
// it without a typed URL.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/alex-bluetrain/notuya-go/pkg/bulb"
	"github.com/alex-bluetrain/notuya-go/pkg/control"
	"github.com/alex-bluetrain/notuya-go/pkg/device"
)

func logf(format string, args ...any) { log.Printf(format, args...) }

func main() {
	configPath := flag.String("config", "", "path to config.json (default: $NOTUYA_CONFIG, or the user config dir)")
	addr := flag.String("addr", "0.0.0.0:8765", "address to listen on (use 0.0.0.0:0 for an ephemeral port)")
	transition := flag.Int("transition", device.DefaultTransition, "stream: default per-update fade length, 0-10")
	interval := flag.Duration("interval", bulb.DefaultStreamInterval, "stream: minimum spacing between colour updates")
	mdns := flag.Bool("mdns", true, "advertise the daemon over mDNS as _notuyad._tcp")
	flag.Parse()

	if *transition < 0 || *transition > device.MaxTransition {
		fmt.Fprintf(os.Stderr, "transition: must be 0-%d, got %d\n", device.MaxTransition, *transition)
		os.Exit(1)
	}

	path := resolveConfigPath(*configPath)
	cfg, err := control.LoadConfig(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	streamOpts := bulb.StreamOptions{Interval: *interval, Transition: transition}
	srv := &server{
		configPath:    path,
		lastColorPath: filepath.Join(filepath.Dir(path), "last-color.txt"),
		streamOpts:    streamOpts,
		registry:      control.NewRegistry(cfg.Devices, streamOpts),
		rooms:         cfg.Rooms,
		scenes:        cfg.Scenes,
		devices:       cfg.Devices,
	}
	defer srv.registry.CloseAll()

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "listen:", err)
		os.Exit(1)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	// Announce the resolved address next to config, so a client that asked
	// for an ephemeral port can discover the real one.
	addrFile := filepath.Join(filepath.Dir(path), "notuyad.addr")
	if err := os.WriteFile(addrFile, []byte(ln.Addr().String()), 0o644); err != nil {
		logf("warning: could not write %s: %v", addrFile, err)
	}
	defer os.Remove(addrFile)

	if *mdns {
		shutdown, err := advertiseMDNS(port)
		if err != nil {
			logf("warning: mDNS advertisement failed: %v", err)
		} else {
			defer shutdown()
		}
	}

	httpSrv := &http.Server{Handler: srv.routes()}

	go func() {
		log.Printf("notuyad listening on http://%s (%d device(s))", ln.Addr(), len(cfg.Devices))
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, "serve:", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("notuyad shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
}
