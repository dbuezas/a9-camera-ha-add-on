package a9

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVideoTS(t *testing.T) {
	// a burst of frames 10 ms apart, then 140 ms apart: times stay on the clock
	var prev uint32
	var kept []uint32
	for _, ms := range []uint32{100, 110, 120, 160, 300, 440} {
		if ts, ok := videoTS(prev, ms*90); ok {
			prev = ts
			kept = append(kept, ms)
		}
	}
	require.Equal(t, []uint32{100, 160, 300, 440}, kept)
	require.Equal(t, uint32(440*90), prev)
}

func TestAudioTS(t *testing.T) {
	require.Equal(t, uint32(800), audioTS(800, 900, 800))   // on time
	require.Equal(t, uint32(800), audioTS(800, 2300, 800))  // late, within 2 packets
	require.Equal(t, uint32(4000), audioTS(800, 4000, 800)) // packets lost: jump to the clock
}
