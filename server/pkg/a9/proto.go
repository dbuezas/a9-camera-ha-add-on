// Package a9 talks to A9 / V720 "Naxclow" mini cameras by impersonating their cloud server.
//
// The camera connects to us (DNS for *.naxclow.com must point here):
//   - HTTP :80   - bootstrap API, tells the camera where the "server" is
//   - TCP  :6123 - control channel (registration, heartbeat, JSON commands)
//   - UDP  :6123 - data channel (JPEG fragments and G.711 A-law audio)
//
// Protocol reverse engineered by https://github.com/intx82/a9-v720
package a9

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
)

// Packet commands (header Cmd field)
const (
	CmdJSON         = 0
	CmdJPEG         = 1
	CmdG711         = 4
	CmdHeartbeat    = 100
	CmdRetransmitOK = 605 // ack with the list of received PkgID
)

// Packet flags (header MsgFlag field) for fragmented media
const (
	FlagHead   = 250
	FlagBody   = 251
	FlagEnd    = 252
	FlagSingle = 255
)

// JSON "code" values
const (
	CodeNatReq       = 11  // server -> device: open a data channel
	CodeNatRsp       = 12  // device -> server
	CodeUDPReq       = 20  // device -> server (UDP)
	CodeUDPRsp       = 21  // server -> device (UDP)
	CodeProbeReq     = 50  // server -> device (UDP)
	CodeProbeRsp     = 51  // device -> server (UDP)
	CodeDeviceStatus = 53  // server -> device: enable command mode
	CodeRegisterReq  = 100 // device -> server
	CodeRegisterRsp  = 101 // server -> device
	CodeForward      = 301 // command wrapper, "content" holds the real command
)

// Forwarded command codes (inside CodeForward "content")
const (
	FwdCloseAV        = 0
	FwdOpenAV         = 3
	FwdBaseInfo       = 4
	FwdRetransmission = 298
)

const headerSize = 20

// maxPayload protects from garbage length fields. Real packets are about 1 KB.
const maxPayload = 64 * 1024

// Packet header, little endian:
//
//	u32 payload length, u16 cmd, u8 msg flag, u8 deal flag, [8]byte forward id, u32 package id
type Packet struct {
	Cmd       uint16
	MsgFlag   byte
	DealFlag  byte
	ForwardID [8]byte
	PkgID     uint32
	Payload   []byte
}

// defaultForwardID is what the original server sends
var defaultForwardID = [8]byte{'0', '0', '0', '0', '0', '0', '0', '0'}

func (p *Packet) Marshal() []byte {
	b := make([]byte, headerSize+len(p.Payload))
	binary.LittleEndian.PutUint32(b, uint32(len(p.Payload)))
	binary.LittleEndian.PutUint16(b[4:], p.Cmd)
	b[6] = p.MsgFlag
	b[7] = p.DealFlag
	copy(b[8:], p.ForwardID[:])
	binary.LittleEndian.PutUint32(b[16:], p.PkgID)
	copy(b[headerSize:], p.Payload)
	return b
}

var ErrShortPacket = errors.New("a9: short packet")

// Unmarshal parses one packet from a UDP datagram
func Unmarshal(b []byte) (*Packet, error) {
	if len(b) < headerSize {
		return nil, ErrShortPacket
	}
	size := binary.LittleEndian.Uint32(b)
	if int(size) > len(b)-headerSize {
		return nil, ErrShortPacket
	}
	p := &Packet{
		Cmd:      binary.LittleEndian.Uint16(b[4:]),
		MsgFlag:  b[6],
		DealFlag: b[7],
		PkgID:    binary.LittleEndian.Uint32(b[16:]),
		Payload:  b[headerSize : headerSize+size],
	}
	copy(p.ForwardID[:], b[8:16])
	return p, nil
}

// ReadPacket reads one packet from a stream (TCP), respecting packet borders
func ReadPacket(r io.Reader) (*Packet, error) {
	b := make([]byte, headerSize)
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, err
	}
	size := binary.LittleEndian.Uint32(b)
	if size > maxPayload {
		return nil, errors.New("a9: wrong packet size")
	}
	b = append(b, make([]byte, size)...)
	if _, err := io.ReadFull(r, b[headerSize:]); err != nil {
		return nil, err
	}
	return Unmarshal(b)
}

// Message is a JSON payload. Only the fields used by the server are typed.
type Message struct {
	Code    int             `json:"code"`
	UID     string          `json:"uid,omitempty"`
	Status  int             `json:"status,omitempty"`
	Content json.RawMessage `json:"content,omitempty"`

	DevNatIP   string `json:"devNatIp,omitempty"`
	DevNatPort int    `json:"devNatPort,omitempty"`
	DevTarget  string `json:"devTarget,omitempty"`
}

func ParseMessage(p *Packet) (*Message, error) {
	msg := &Message{}
	if err := json.Unmarshal(p.Payload, msg); err != nil {
		return nil, err
	}
	return msg, nil
}

// ContentCode returns the "code" inside a CodeForward message
func (m *Message) ContentCode() int {
	var c struct {
		Code int `json:"code"`
	}
	if json.Unmarshal(m.Content, &c) != nil {
		return -1
	}
	return c.Code
}

func jsonPacket(v any) []byte {
	b, _ := json.Marshal(v)
	p := &Packet{Cmd: CmdJSON, ForwardID: defaultForwardID, Payload: b}
	return p.Marshal()
}

func emptyPacket(cmd uint16) []byte {
	p := &Packet{Cmd: cmd, ForwardID: defaultForwardID}
	return p.Marshal()
}

// Obj is a JSON object that keeps the key order, like the original app sends it
type Obj []KV

type KV struct {
	Key   string
	Value any
}

func (o Obj) MarshalJSON() ([]byte, error) {
	b := []byte{'{'}
	for i, kv := range o {
		if i > 0 {
			b = append(b, ',')
		}
		k, err := json.Marshal(kv.Key)
		if err != nil {
			return nil, err
		}
		v, err := json.Marshal(kv.Value)
		if err != nil {
			return nil, err
		}
		b = append(b, k...)
		b = append(b, ':')
		b = append(b, v...)
	}
	return append(b, '}'), nil
}
