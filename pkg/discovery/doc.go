// Package discovery implements Tuya LAN device discovery: the unsolicited
// UDP/6667 broadcast listener and the solicited UDP/7000 request/reply
// pattern, both keyed with Tuya's well-known discovery key (not a device's
// local_key).
package discovery
