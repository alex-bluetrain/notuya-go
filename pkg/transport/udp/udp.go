// Package udp is the transport for Tuya LAN discovery: 6699 frames over
// UDP broadcast, encrypted under Tuya's fixed, well-known discovery key
// rather than any device's local_key. It sends and receives frames; what
// an announcement contains is pkg/discovery's business.
package udp

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"net"
	"time"

	v35 "github.com/alex-bluetrain/notuya-go/pkg/transport/v35"
)

// Ports used by discovery.
const (
	AnnouncePort      = 6667 // devices broadcast their presence here periodically
	AnnouncePortPlain = 6666 // legacy, unencrypted announcements
	SolicitPort       = 7000 // "app" solicitation and the device replies
)

// reqDevInfo is the solicited-discovery command code. It is the only
// command this transport sends, so it lives here rather than in a session
// layer that UDP discovery does not have.
const reqDevInfo uint32 = 0x25

// Key is Tuya's well-known discovery key: md5("yGAdlopoPVldABfn").
func Key() []byte {
	sum := md5.Sum([]byte("yGAdlopoPVldABfn"))
	return sum[:]
}

// Packet is one received datagram's payload. Payload is the decrypted
// frame body, or the raw datagram when it was not an encrypted frame (the
// plaintext announcements some older firmware sends on 6666).
type Packet struct {
	From      *net.UDPAddr
	Cmd       uint32
	Payload   []byte
	Encrypted bool
}

// Listener receives discovery datagrams on one UDP port.
type Listener struct{ c *net.UDPConn }

// Listen binds a UDP port on every interface.
func Listen(port int) (*Listener, error) {
	c, err := net.ListenUDP("udp4", &net.UDPAddr{Port: port})
	if err != nil {
		return nil, fmt.Errorf("udp: listening on :%d: %w", port, err)
	}
	return &Listener{c: c}, nil
}

// SetDeadline bounds Read.
func (l *Listener) SetDeadline(t time.Time) error { return l.c.SetReadDeadline(t) }

// Close unblocks a pending Read.
func (l *Listener) Close() error { return l.c.Close() }

// Read blocks for the next datagram. A datagram that is neither a valid
// encrypted frame nor JSON is skipped rather than returned.
func (l *Listener) Read() (Packet, error) {
	buf := make([]byte, 4096)
	for {
		n, from, err := l.c.ReadFromUDP(buf)
		if err != nil {
			return Packet{}, err
		}
		raw := buf[:n]
		if f, err := v35.Decode(Key(), raw); err == nil {
			return Packet{From: from, Cmd: f.Cmd, Payload: f.Payload, Encrypted: true}, nil
		}
		if json.Valid(raw) {
			return Packet{From: from, Payload: append([]byte(nil), raw...)}, nil
		}
	}
}

// Solicit broadcasts the device-info request on SolicitPort from every
// local IPv4 interface. body builds the request for the interface it is
// sent from, because the request tells the device where to reply.
// Best-effort: interfaces that refuse a broadcast send are skipped.
func Solicit(body func(src net.IP) []byte) {
	targets := broadcastTargets()
	for _, src := range localIPv4s() {
		iv, err := v35.NewIV()
		if err != nil {
			continue
		}
		raw, err := v35.Encode(Key(), iv, v35.Frame{Seqno: 1, Cmd: reqDevInfo, Payload: body(src)})
		if err != nil {
			continue
		}
		// Bound to the interface it advertises: from an unbound socket the
		// kernel could route the packet out of a different one.
		sender, err := net.ListenUDP("udp4", &net.UDPAddr{IP: src})
		if err != nil {
			continue
		}
		for _, dst := range targets {
			_, _ = sender.WriteToUDP(raw, &net.UDPAddr{IP: dst, Port: SolicitPort})
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
// broadcast of every non-loopback IPv4 interface, deduplicated: interfaces
// sharing a subnet yield the same address, and sending it twice only
// duplicates the copies of our own request that come back.
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
		if !sent[bcast.String()] {
			sent[bcast.String()] = true
			targets = append(targets, bcast)
		}
	}
	return targets
}
