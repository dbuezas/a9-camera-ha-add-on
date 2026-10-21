package a9

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"math/rand/v2"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPacketRoundTrip(t *testing.T) {
	p := &Packet{Cmd: CmdJPEG, MsgFlag: FlagBody, PkgID: 1234, ForwardID: defaultForwardID, Payload: []byte("hello")}
	b := p.Marshal()
	require.Len(t, b, headerSize+5)

	q, err := Unmarshal(b)
	require.NoError(t, err)
	require.Equal(t, p, q)

	_, err = Unmarshal(b[:10])
	require.ErrorIs(t, err, ErrShortPacket)
	_, err = Unmarshal(b[:headerSize+2]) // length says 5
	require.ErrorIs(t, err, ErrShortPacket)
}

// Same header bytes as the registration answer in a capture (the JSON there had spaces)
func TestPacketCapture(t *testing.T) {
	b := jsonPacket(Obj{{"code", CodeRegisterRsp}, {"status", 200}})
	require.Equal(t, "19000000000000003030303030303030000000007b22636f6465223a3130312c22737461747573223a3230307d",
		hexString(b))
}

// TCP can split and join packets anywhere. The Python server did not handle this.
func TestReadPacketStream(t *testing.T) {
	var stream []byte
	var want []*Packet
	for i := range 50 {
		p := &Packet{Cmd: CmdJSON, PkgID: uint32(i), Payload: bytes.Repeat([]byte{'x'}, rand.IntN(300))}
		want = append(want, p)
		stream = append(stream, p.Marshal()...)
	}

	r := &chunkReader{b: stream}
	for _, p := range want {
		got, err := ReadPacket(r)
		require.NoError(t, err)
		require.Equal(t, p.PkgID, got.PkgID)
		require.Equal(t, p.Payload, got.Payload)
	}
	_, err := ReadPacket(r)
	require.ErrorIs(t, err, io.EOF)
}

func TestReadPacketGarbage(t *testing.T) {
	b := make([]byte, headerSize)
	binary.LittleEndian.PutUint32(b, 1<<30)
	_, err := ReadPacket(bytes.NewReader(b))
	require.Error(t, err)
}

func TestObjOrder(t *testing.T) {
	b, err := json.Marshal(Obj{{"code", 301}, {"target", "x"}, {"content", Obj{{"code", 202}, {"IrLed", 1}}}})
	require.NoError(t, err)
	require.Equal(t, `{"code":301,"target":"x","content":{"code":202,"IrLed":1}}`, string(b))
}

func TestForwardContentCode(t *testing.T) {
	// real message from firmware 202302031142, with its odd spaces
	raw := `{"code":301 , "target": "00112233445566778899aabbccddeeff", "content": { "code":       4, "IrLed":      0, "version":  "202302031142" } }`
	msg, err := ParseMessage(&Packet{Payload: []byte(raw)})
	require.NoError(t, err)
	require.Equal(t, CodeForward, msg.Code)
	require.Equal(t, FwdBaseInfo, msg.ContentCode())
}

// chunkReader returns random small chunks, like TCP can do
type chunkReader struct{ b []byte }

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.b) == 0 {
		return 0, io.EOF
	}
	n := min(len(p), 1+rand.IntN(7), len(r.b))
	copy(p, r.b[:n])
	r.b = r.b[n:]
	return n, nil
}

func hexString(b []byte) string {
	const digits = "0123456789abcdef"
	s := make([]byte, 0, 2*len(b))
	for _, c := range b {
		s = append(s, digits[c>>4], digits[c&15])
	}
	return string(s)
}

// readFixture reads datagrams saved by the capture tool: [u32 LE length][datagram]...
// The files hold real camera images, so they are not in git. Tests skip without them.
func readFixture(t *testing.T, name string) []*Packet {
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Skipf("no capture %s", name)
	}
	var pkts []*Packet
	for len(b) >= 4 {
		n := binary.LittleEndian.Uint32(b)
		p, err := Unmarshal(b[4 : 4+n])
		require.NoError(t, err)
		pkts = append(pkts, p)
		b = b[4+n:]
	}
	return pkts
}
