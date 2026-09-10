package discovery

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/averstraeten/notuya-go/internal/protocol"
	"github.com/averstraeten/notuya-go/internal/protocol35"
)

const (
	unsolicitedPort = 6667 // devices broadcast their presence here periodically
	solicitedPort   = 7000 // "app" solicitation + device replies

	ivLen = 12

	// solicitInterval is how often the discovery request is repeated
	// during a scan.
	solicitInterval = 1500 * time.Millisecond
)

// discoveryKey is Tuya's well-known, hardcoded key used to encrypt/decrypt
// LAN discovery traffic — never a device's own local_key.
func discoveryKey() []byte {
	sum := md5.Sum([]byte("yGAdlopoPVldABfn"))
	return sum[:]
}

// Device is one discovered Tuya device announcement.
type Device struct {
	ID      string `json:"gwId"`
	IP      string `json:"ip"`
	Version string `json:"version"`
}

// Scan listens for Tuya LAN discovery broadcasts for the given duration
// and returns every distinct device seen (deduped by ID). It also makes a
// best-effort attempt at the "solicited" discovery pattern (broadcasting
// a request on UDP/7000) to prompt a faster reply; that attempt is
// non-fatal if the platform/network refuses a broadcast send (e.g.
// missing SO_BROADCAST permission) — passive listening on UDP/6667 alone
// is enough to find devices, since they announce themselves periodically
// on their own.
func Scan(ctx context.Context, timeout time.Duration) ([]Device, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: unsolicitedPort})
	if err != nil {
		return nil, fmt.Errorf("discovery: listening on :%d: %w", unsolicitedPort, err)
	}
	defer conn.Close()

	replyConn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: solicitedPort})

	deadline := time.Now().Add(timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}

	if err == nil {
		defer replyConn.Close()
		go solicitRepeatedly(ctx, deadline)
	}
	_ = conn.SetReadDeadline(deadline)
	if replyConn != nil {
		_ = replyConn.SetReadDeadline(deadline)
	}

	key := discoveryKey()

	// Both ports are drained concurrently, so announcements travel over a
	// channel rather than into a shared map.
	seen := make(chan Device)
	drain := func(c *net.UDPConn) {
		if c == nil {
			return
		}
		buf := make([]byte, 4096)
		for {
			n, _, err := c.ReadFromUDP(buf)
			if err != nil {
				return // timeout or closed
			}
			_, payload, err := protocol35.DecodeFrame(key, buf[:n])
			if err != nil {
				continue
			}
			if d, ok := parseAnnouncement(payload); ok {
				seen <- d
			}
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); drain(conn) }()
	go func() { defer wg.Done(); drain(replyConn) }()
	go func() { wg.Wait(); close(seen) }()

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

// solicitRepeatedly re-sends the discovery request until the scan window
// closes. A single request is unreliable in practice — observed on the
// development network, the first attempts routinely go unanswered while
// later ones get every device — and UDP offers no delivery guarantee
// anyway, so one lost request would otherwise mean an empty scan.
func solicitRepeatedly(ctx context.Context, deadline time.Time) {
	ticker := time.NewTicker(solicitInterval)
	defer ticker.Stop()

	for {
		trySolicit()
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if !now.Before(deadline) {
				return
			}
		}
	}
}

// trySolicit sends the solicited-discovery request ({"from":"app","ip":…})
// as a broadcast on UDP/7000, prompting v3.5 devices to reply sooner than
// waiting for their next periodic unsolicited announcement. Best-effort:
// errors (including a platform refusing the broadcast send outright) are
// silently ignored — the caller falls back to passive listening.
func trySolicit() {
	targets := broadcastTargets()

	// One request per interface, bound to that interface and advertising
	// its own address. The request tells the device where to reply, so
	// sending from an unbound socket would announce one interface's
	// address while the kernel routed the packet out of another.
	for _, src := range localIPv4s() {
		body, err := json.Marshal(map[string]string{"from": "app", "ip": src.String()})
		if err != nil {
			continue
		}
		iv := make([]byte, ivLen)
		if _, err := rand.Read(iv); err != nil {
			continue
		}
		frame, err := protocol35.EncodeFrame(discoveryKey(), iv, 1, protocol.ReqDevInfo, body)
		if err != nil {
			continue
		}
		sender, err := net.ListenUDP("udp4", &net.UDPAddr{IP: src})
		if err != nil {
			continue
		}
		for _, dst := range targets {
			_, _ = sender.WriteToUDP(frame, &net.UDPAddr{IP: dst, Port: solicitedPort})
		}
		sender.Close()
	}
}

// localIPv4s is every usable IPv4 address of this host.
func localIPv4s() []net.IP {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var ips []net.IP
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}
		if v4 := ipnet.IP.To4(); v4 != nil {
			ips = append(ips, v4)
		}
	}
	return ips
}

// broadcastTargets is the limited broadcast address plus the directed
// broadcast of every up, non-loopback IPv4 interface, deduplicated:
// interfaces sharing a subnet yield the same address, and sending it twice
// only produces duplicate copies of our own request coming back at us.
func broadcastTargets() []net.IP {
	targets := []net.IP{net.IPv4bcast}
	sent := map[string]bool{net.IPv4bcast.String(): true}

	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return targets
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}
		ip := ipnet.IP.To4()
		mask := ipnet.Mask
		if ip == nil || len(mask) != net.IPv4len {
			continue
		}
		bcast := make(net.IP, net.IPv4len)
		for i := range bcast {
			bcast[i] = ip[i] | ^mask[i]
		}
		if sent[bcast.String()] {
			continue
		}
		sent[bcast.String()] = true
		targets = append(targets, bcast)
	}
	return targets
}

// parseAnnouncement turns a decrypted discovery payload into a Device,
// reporting false for anything that is not a device announcement.
func parseAnnouncement(payload []byte) (Device, bool) {
	// Solicited replies (UDP/7000) carry a retcode ahead of the JSON like
	// any other device-originated frame; the unsolicited announcements on
	// UDP/6667 do not.
	if body, err := protocol35.StripRetcode(payload); err == nil && looksLikeJSON(body) {
		payload = body
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
