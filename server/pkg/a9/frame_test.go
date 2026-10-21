package a9

import (
	"bytes"
	"encoding/binary"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/require"
)

func fakeJPEG(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i * 7)
	}
	b[0], b[1], b[n-2], b[n-1] = 0xFF, 0xD8, 0xFF, 0xD9
	return b
}

// fragments splits a frame like the camera: 1000 byte payloads, the last one with the trailer
func fragments(frame []byte, firstPID uint32) []*Packet {
	tail := make([]byte, trailerSize)
	binary.LittleEndian.PutUint32(tail[1:], uint32(len(frame)))
	data := append(bytes.Clone(frame), tail...)

	var pkts []*Packet
	for i := 0; len(data) > 0; i++ {
		n := min(1000, len(data))
		flag := byte(FlagBody)
		if i == 0 {
			flag = FlagHead
		}
		if n == len(data) {
			flag = FlagEnd
		}
		pkts = append(pkts, &Packet{Cmd: CmdJPEG, MsgFlag: flag, PkgID: firstPID + uint32(i), Payload: data[:n]})
		data = data[n:]
	}
	return pkts
}

func TestAssembler(t *testing.T) {
	frame := fakeJPEG(24659) // size of a real frame
	var a assembler
	var got []byte
	for _, p := range fragments(frame, 100) {
		if f := a.push(p); f != nil {
			got = f
		}
	}
	require.Equal(t, frame, got)
	require.Equal(t, 1, a.Frames)
}

func TestAssemblerReorder(t *testing.T) {
	frame := fakeJPEG(9000)
	pkts := fragments(frame, 0)
	// keep head first and end last, shuffle the middle
	mid := pkts[1 : len(pkts)-1]
	rand.Shuffle(len(mid), func(i, j int) { mid[i], mid[j] = mid[j], mid[i] })

	var a assembler
	var got []byte
	for _, p := range pkts {
		if f := a.push(p); f != nil {
			got = f
		}
	}
	require.Equal(t, frame, got)
}

func TestAssemblerLoss(t *testing.T) {
	var a assembler
	pkts := fragments(fakeJPEG(9000), 0)
	pkts = append(pkts[:3], pkts[4:]...) // lose one fragment
	for _, p := range pkts {
		require.Nil(t, a.push(p))
	}
	require.Equal(t, 1, a.Dropped)

	// the next frame is fine again
	frame := fakeJPEG(5000)
	var got []byte
	for _, p := range fragments(frame, 20) {
		if f := a.push(p); f != nil {
			got = f
		}
	}
	require.Equal(t, frame, got)
}

func TestAssemblerLostEnd(t *testing.T) {
	var a assembler
	pkts := fragments(fakeJPEG(5000), 0)
	for _, p := range pkts[:len(pkts)-1] {
		a.push(p)
	}
	frame := fakeJPEG(3000)
	var got []byte
	for _, p := range fragments(frame, 10) {
		if f := a.push(p); f != nil {
			got = f
		}
	}
	require.Equal(t, frame, got)
	require.Equal(t, 1, a.Dropped)
}

func TestAssemblerBodyWithoutHead(t *testing.T) {
	var a assembler
	pkts := fragments(fakeJPEG(5000), 0)
	for _, p := range pkts[1:] { // joined in the middle of a frame
		require.Nil(t, a.push(p))
	}
	require.Equal(t, 0, a.Frames)
}

// Real captures: every frame must be complete and a valid JPEG start/end
func TestAssemblerCapture(t *testing.T) {
	for _, name := range []string{"udp_cam1.bin", "udp_cam2.bin"} {
		t.Run(name, func(t *testing.T) {
			var a assembler
			var audio int
			for _, p := range readFixture(t, name) {
				switch p.Cmd {
				case CmdJPEG:
					if f := a.push(p); f != nil {
						require.Equal(t, []byte{0xFF, 0xD9}, f[len(f)-2:])
					}
				case CmdG711:
					require.Len(t, p.Payload, 800)
					audio++
				}
			}
			require.Equal(t, 30, a.Frames)
			require.Equal(t, 0, a.Dropped)
			require.Greater(t, audio, 20)
		})
	}
}
