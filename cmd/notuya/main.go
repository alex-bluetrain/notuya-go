// Command notuya is the CLI for controlling Tuya local-protocol v3.5
// bulbs — the Go equivalent of the sibling Python project's ctl.py.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/averstraeten/notuya-go/internal/bulb"
	"github.com/averstraeten/notuya-go/internal/device"
	"github.com/averstraeten/notuya-go/internal/discovery"
	"github.com/averstraeten/notuya-go/internal/protocol35"
)

const usage = `Usage:
  notuya on
  notuya off
  notuya color RRGGBB
  notuya brightness 0-100
  notuya music            (streams RRGGBB colors read from stdin, one per line)
                          [--transition 0-10] [--interval 40ms]
  notuya get-color
  notuya list
  notuya scan             [--update: rewrite config.json IPs, matched by device_id]
`

// scanTimeout is generous because devices ignore the first couple of
// solicitations after a period of quiet — measured at roughly three
// seconds before any of them answer.
const scanTimeout = 15 * time.Second

// commandTimeout bounds one device's whole open→command→close cycle. It is
// a variable so tests can shorten it; nothing at runtime reassigns it.
var commandTimeout = 10 * time.Second

func main() {
	configPath := flag.String("config", "", "path to config.json (default: $NOTUYA_CONFIG, or the user config dir)")
	transition := flag.Int("transition", device.DefaultTransition, "music: per-update fade length, 0-10 (higher is smoother but smears)")
	interval := flag.Duration("interval", bulb.DefaultStreamInterval, "music: minimum spacing between colour updates")
	flag.Parse()
	args := flag.Args()

	if len(args) < 1 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}

	path := resolveConfigPath(*configPath)
	lastColorPath := filepath.Join(filepath.Dir(path), "last-color.txt")

	cmd := args[0]

	if cmd == "get-color" {
		fmt.Println(readLastColor(lastColorPath))
		return
	}

	if cmd == "scan" {
		// Parse flags that follow the subcommand (Go's flag package stops
		// at the first positional), so `notuya scan --update` works.
		scanFlags := flag.NewFlagSet("scan", flag.ExitOnError)
		scanUpdate := scanFlags.Bool("update", false, "rewrite config.json ip_address for every device matched by device_id")
		_ = scanFlags.Parse(args[1:])

		ctx, cancel := context.WithTimeout(context.Background(), scanTimeout)
		defer cancel()
		devices, err := discovery.Scan(ctx, scanTimeout)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if *scanUpdate {
			if !updateConfigIPs(path, devices) {
				os.Exit(1)
			}
			return
		}
		for _, d := range devices {
			fmt.Printf("%s\t%s\t%s\n", d.ID, d.IP, d.Version)
		}
		return
	}

	cfg, err := loadConfig(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if cmd == "list" {
		for _, d := range cfg.Devices {
			name := d.Name
			if name == "" {
				name = d.DeviceID
			}
			fmt.Printf("%s\t%s\n", name, d.IPAddress)
		}
		return
	}

	if len(cfg.Devices) == 0 {
		fmt.Fprintln(os.Stderr, "no devices configured in", path)
		os.Exit(1)
	}

	// Every device-touching command reports per-device outcomes and exits
	// non-zero if any of them failed, so a caller can tell a partial
	// failure from a clean run.
	ok := true

	switch cmd {
	case "on":
		ok = forEachDevice(cfg.Devices, func(ctx context.Context, b *bulb.Bulb) error {
			return b.TurnOn(ctx)
		})
	case "off":
		ok = forEachDevice(cfg.Devices, func(ctx context.Context, b *bulb.Bulb) error {
			return b.TurnOff(ctx)
		})
	case "color":
		if len(args) != 2 {
			fmt.Fprint(os.Stderr, usage)
			os.Exit(1)
		}
		r, g, b, err := parseColor(args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		ok = forEachDevice(cfg.Devices, func(ctx context.Context, bl *bulb.Bulb) error {
			if err := bl.TurnOn(ctx); err != nil {
				return err
			}
			return bl.SetColour(ctx, r, g, b)
		})
		if err := writeLastColor(lastColorPath, fmt.Sprintf("%02x%02x%02x", r, g, b)); err != nil {
			fmt.Fprintln(os.Stderr, "warning: could not save last color:", err)
		}
	case "brightness":
		if len(args) != 2 {
			fmt.Fprint(os.Stderr, usage)
			os.Exit(1)
		}
		pct, err := strconv.Atoi(args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "brightness: invalid value:", args[1])
			os.Exit(1)
		}
		ok = forEachDevice(cfg.Devices, func(ctx context.Context, b *bulb.Bulb) error {
			if err := b.TurnOn(ctx); err != nil {
				return err
			}
			return b.SetBrightnessPercent(ctx, float64(pct))
		})
	case "music":
		if *transition < 0 || *transition > device.MaxTransition {
			fmt.Fprintf(os.Stderr, "transition: must be 0-%d, got %d\n", device.MaxTransition, *transition)
			os.Exit(1)
		}
		opts := bulb.StreamOptions{Interval: *interval, Transition: transition}
		ok = runMusic(cfg.Devices, lastColorPath, opts)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}

	if !ok {
		os.Exit(1)
	}
}

// resolveConfigPath applies the precedence: --config flag > NOTUYA_CONFIG
// env > the user's config directory.
func resolveConfigPath(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if env := os.Getenv("NOTUYA_CONFIG"); env != "" {
		return env
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "notuya-go", "config.json")
}

var colorRGBAPattern = regexp.MustCompile(`(?i)^rgba?\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)`)

// parseColor accepts #RRGGBB, RRGGBB, rgb(r,g,b) or rgba(r,g,b,a) — the
// same formats ctl.py's parse_color accepts (zenity's colour-picker output
// formats).
func parseColor(raw string) (r, g, b uint8, err error) {
	raw = strings.TrimSpace(raw)
	if m := colorRGBAPattern.FindStringSubmatch(raw); m != nil {
		rv, _ := strconv.Atoi(m[1])
		gv, _ := strconv.Atoi(m[2])
		bv, _ := strconv.Atoi(m[3])
		return uint8(rv), uint8(gv), uint8(bv), nil
	}
	hexColor := strings.TrimPrefix(raw, "#")
	if len(hexColor) < 6 {
		return 0, 0, 0, fmt.Errorf("color: invalid format: %q", raw)
	}
	hexColor = hexColor[:6]
	rv, err1 := strconv.ParseUint(hexColor[0:2], 16, 8)
	gv, err2 := strconv.ParseUint(hexColor[2:4], 16, 8)
	bv, err3 := strconv.ParseUint(hexColor[4:6], 16, 8)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, 0, 0, fmt.Errorf("color: invalid format: %q", raw)
	}
	return uint8(rv), uint8(gv), uint8(bv), nil
}

// forEachDevice fans out fn to every device concurrently, isolating
// failures per device (one device failing does not stop the others) —
// mirrors ctl.py's ThreadPoolExecutor-based for_each_device, including
// its "status() as a connectivity warm-up before the real command" step.
// forEachDevice reports whether every device succeeded.
func forEachDevice(devices []Device, fn func(ctx context.Context, b *bulb.Bulb) error) bool {
	var wg sync.WaitGroup
	var mu sync.Mutex
	failed := 0

	report := func(name string, err error) {
		mu.Lock()
		defer mu.Unlock()
		if err == nil {
			fmt.Printf("OK: %s\n", name)
			return
		}
		failed++
		fmt.Fprintf(os.Stderr, "FAIL: %s -> %v\n", name, err)
	}

	for _, d := range devices {
		wg.Add(1)
		go func(d Device) {
			defer wg.Done()
			name := d.Name
			if name == "" {
				name = d.DeviceID
			}

			ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
			defer cancel()

			sess := protocol35.NewSession(d.IPAddress, []byte(d.LocalKey))
			if err := sess.Open(ctx); err != nil {
				report(name, err)
				return
			}
			defer sess.Close()

			b := bulb.NewBulb(sess, name)
			if err := b.Status(ctx); err != nil {
				report(name, err)
				return
			}
			report(name, fn(ctx, b))
		}(d)
	}
	wg.Wait()

	return failed == 0
}
