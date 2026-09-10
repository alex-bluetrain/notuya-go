package discovery

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/averstraeten/notuya-go/internal/protocol"
	"github.com/averstraeten/notuya-go/internal/protocol35"
)

const (
	unsolicitedPort = 6667 // devices broadcast their presence here periodically
	solicitedPort   = 7000 // "app" solicitation + device replies

	ivLen = 12
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
	if err == nil {
		defer replyConn.Close()
		go trySolicit(replyConn)
	}

	deadline := time.Now().Add(timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = conn.SetReadDeadline(deadline)
	if replyConn != nil {
		_ = replyConn.SetReadDeadline(deadline)
	}

	found := make(map[string]Device)
	key := discoveryKey()

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
			if _, payload, err := protocol35.DecodeFrame(key, buf[:n]); err == nil {
				var d Device
				if json.Unmarshal(payload, &d) == nil && d.ID != "" {
					found[d.ID] = d
				}
			}
		}
	}

	done := make(chan struct{})
	go func() { drain(replyConn); close(done) }()
	drain(conn)
	<-done

	devices := make([]Device, 0, len(found))
	for _, d := range found {
		devices = append(devices, d)
	}
	return devices, nil
}

// trySolicit sends the solicited-discovery request ({"from":"app","ip":…})
// as a broadcast on UDP/7000, prompting v3.5 devices to reply sooner than
// waiting for their next periodic unsolicited announcement. Best-effort:
// errors (including a platform refusing the broadcast send outright) are
// silently ignored — the caller falls back to passive listening.
func trySolicit(replyConn *net.UDPConn) {
	localIP := localIPv4(replyConn)
	if localIP == "" {
		return
	}
	body, err := json.Marshal(map[string]string{"from": "app", "ip": localIP})
	if err != nil {
		return
	}
	iv := make([]byte, ivLen)
	if _, err := rand.Read(iv); err != nil {
		return
	}
	frame, err := protocol35.EncodeFrame(discoveryKey(), iv, 1, protocol.ReqDevInfo, body)
	if err != nil {
		return
	}

	dst := &net.UDPAddr{IP: net.IPv4bcast, Port: solicitedPort}
	sender, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return
	}
	defer sender.Close()
	_, _ = sender.WriteToUDP(frame, dst)
}

func localIPv4(conn *net.UDPConn) string {
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return ""
	}
	if addr.IP != nil && !addr.IP.IsUnspecified() {
		return addr.IP.String()
	}
	ifaces, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range ifaces {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}
		if v4 := ipnet.IP.To4(); v4 != nil {
			return v4.String()
		}
	}
	return ""
}
