package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/alex-bluetrain/notuya-go/pkg/transport/udp"
)

// solicitInterval is how often the discovery request is repeated during a
// scan.
const solicitInterval = 1500 * time.Millisecond

// Device is one discovered Tuya device announcement.
type Device struct {
	ID      string `json:"gwId"`
	IP      string `json:"ip"`
	Version string `json:"version"`
}

// Listen reports device announcements until ctx is cancelled, then closes
// the channel. It is passive: it only hears devices announcing themselves,
// which they do periodically on UDP/6667 (encrypted, code 0x13/0x23) and,
// on older firmware, UDP/6666 (plaintext). A device announcing on several
// ports, or repeatedly, is reported each time; dedupe by ID if needed.
//
// It fails only if neither port can be bound.
func Listen(ctx context.Context) (<-chan Device, error) {
	return listen(ctx, udp.AnnouncePort, udp.AnnouncePortPlain)
}

func listen(ctx context.Context, ports ...int) (<-chan Device, error) {
	var ls []*udp.Listener
	var firstErr error
	for _, p := range ports {
		l, err := udp.Listen(p)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		ls = append(ls, l)
	}
	if len(ls) == 0 {
		return nil, fmt.Errorf("discovery: %w", firstErr)
	}

	out := make(chan Device)
	var wg sync.WaitGroup
	for _, l := range ls {
		wg.Add(1)
		go func(l *udp.Listener) {
			defer wg.Done()
			for {
				p, err := l.Read()
				if err != nil {
					return // closed
				}
				d, ok := parseAnnouncement(p.Payload)
				if !ok {
					continue
				}
				select {
				case out <- d:
				case <-ctx.Done():
					return
				}
			}
		}(l)
	}
	go func() {
		<-ctx.Done()
		for _, l := range ls {
			l.Close()
		}
	}()
	go func() { wg.Wait(); close(out) }()
	return out, nil
}

// Scan collects devices for the given duration and returns every distinct
// one (deduped by ID). Alongside passive listening it repeatedly
// broadcasts the solicited-discovery request on UDP/7000, which prompts
// v3.5 devices to reply sooner than their next periodic announcement.
// Solicitation is best-effort: if the platform refuses the broadcast or
// port 7000 is taken, passive listening alone still finds devices.
func Scan(ctx context.Context, timeout time.Duration) ([]Device, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ports := []int{udp.AnnouncePort, udp.AnnouncePortPlain}
	// The reply port is only worth listening on if we can solicit.
	if l, err := udp.Listen(udp.SolicitPort); err == nil {
		l.Close()
		ports = append(ports, udp.SolicitPort)
		go solicitRepeatedly(ctx)
	}

	seen, err := listen(ctx, ports...)
	if err != nil {
		return nil, err
	}
	found := make(map[string]Device)
	for d := range seen {
		found[d.ID] = d
	}
	devices := make([]Device, 0, len(found))
	for _, d := range found {
		devices = append(devices, d)
	}
	return devices, nil
}

// solicitRepeatedly re-sends the discovery request until ctx ends. A
// single request is unreliable in practice — observed on the development
// network, the first attempts routinely go unanswered while later ones get
// every device — and UDP offers no delivery guarantee anyway.
func solicitRepeatedly(ctx context.Context) {
	ticker := time.NewTicker(solicitInterval)
	defer ticker.Stop()
	for {
		udp.Solicit(solicitBody)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// solicitBody is the request ({"from":"app","ip":…}) advertising the
// interface it is sent from, which is where the device replies.
func solicitBody(src net.IP) []byte {
	b, _ := json.Marshal(map[string]string{"from": "app", "ip": src.String()})
	return b
}

// parseAnnouncement turns a discovery payload into a Device, reporting
// false for anything that is not a device announcement.
func parseAnnouncement(payload []byte) (Device, bool) {
	// Solicited replies (UDP/7000) carry a 4-byte retcode ahead of the JSON
	// like any other device-originated frame; the periodic announcements
	// do not.
	if !looksLikeJSON(payload) && len(payload) > 4 && looksLikeJSON(payload[4:]) {
		payload = payload[4:]
	}

	// Our own solicitation comes back to us: it goes to a broadcast
	// address on the very port the replies arrive on. It carries no gwId,
	// so it is filtered out here rather than reported as a device.
	var d Device
	if err := json.Unmarshal(payload, &d); err != nil || d.ID == "" {
		return Device{}, false
	}
	return d, true
}

// looksLikeJSON reports whether b starts an object, so a payload that had
// no retcode is not mistaken for one whose first four bytes got eaten.
func looksLikeJSON(b []byte) bool {
	for _, c := range b {
		switch c {
		case ' ', '\t', '\r', '\n':
			continue
		case '{':
			return true
		default:
			return false
		}
	}
	return false
}
