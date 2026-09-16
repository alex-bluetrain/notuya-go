package protocol

// Command codes, shared across all Tuya protocol versions (confirmed
// against tinytuya's core/command_types.py — these are not 3.5-specific).
const (
	SessKeyNegStart  uint32 = 0x03
	SessKeyNegResp   uint32 = 0x04
	SessKeyNegFinish uint32 = 0x05
	Control          uint32 = 0x07
	HeartBeat        uint32 = 0x09 // keeps an idle persistent connection alive
	DPQuery          uint32 = 0x0a
	ControlNew       uint32 = 0x0d
	DPQueryNew       uint32 = 0x10
	ReqDevInfo       uint32 = 0x25 // solicited discovery, broadcast to UDP/7000
)
