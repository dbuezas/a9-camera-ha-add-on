package a9

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVideoTS(t *testing.T) {
	// a burst 10 ms apart, then irregular gaps: always whole frames (9000), never backwards
	var got []uint32
	var prev uint32
	for i, ms := range []uint32{0, 10, 20, 30, 140, 470, 520, 760} {
		prev = videoTS(prev, i == 0, ms*90)
		got = append(got, prev/frameStep)
	}
	require.Equal(t, []uint32{0, 1, 2, 3, 4, 5, 6, 8}, got)
}

func TestAudioTS(t *testing.T) {
	require.Equal(t, uint32(800), audioTS(800, 900, 800))   // on time
	require.Equal(t, uint32(800), audioTS(800, 2300, 800))  // late, within 2 packets
	require.Equal(t, uint32(4000), audioTS(800, 4000, 800)) // packets lost: jump to the clock
}
