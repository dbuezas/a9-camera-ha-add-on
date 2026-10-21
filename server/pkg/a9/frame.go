package a9

import (
	"encoding/binary"
	"slices"
)

// The last fragment of a JPEG frame ends with a 5 byte trailer: 0x00 + u32 LE frame size
const trailerSize = 5

// maxFragments limits memory if the end fragment never arrives. A frame is about 25 fragments.
const maxFragments = 256

type fragment struct {
	id   uint32
	data []byte
}

// assembler joins JPEG fragments back into frames.
// Fragments are sorted by PkgID, so UDP reordering inside a frame is fine.
// A frame with a missing fragment fails the size check and is dropped.
type assembler struct {
	frags   []fragment
	started bool

	Frames  int
	Dropped int
}

// push returns a complete JPEG frame or nil
func (a *assembler) push(p *Packet) []byte {
	switch p.MsgFlag {
	case FlagSingle:
		a.reset()
		a.Frames++
		return slices.Clone(p.Payload)

	case FlagHead:
		if a.started {
			a.Dropped++ // previous frame never ended
		}
		a.reset()
		a.started = true
		a.add(p)

	case FlagBody:
		if a.started {
			a.add(p)
		}

	case FlagEnd:
		if !a.started {
			return nil
		}
		a.add(p)
		frame := a.join()
		a.reset()
		if frame == nil {
			a.Dropped++
			return nil
		}
		a.Frames++
		return frame
	}
	return nil
}

func (a *assembler) add(p *Packet) {
	if len(a.frags) >= maxFragments {
		a.reset()
		a.Dropped++
		return
	}
	a.frags = append(a.frags, fragment{id: p.PkgID, data: slices.Clone(p.Payload)})
}

func (a *assembler) join() []byte {
	slices.SortFunc(a.frags, func(x, y fragment) int {
		return int(int64(x.id) - int64(y.id))
	})

	last := a.frags[len(a.frags)-1].data
	if len(last) < trailerSize {
		return nil
	}
	want := int(binary.LittleEndian.Uint32(last[len(last)-4:]))

	frame := make([]byte, 0, want+trailerSize)
	for _, f := range a.frags {
		frame = append(frame, f.data...)
	}
	frame = frame[:len(frame)-trailerSize]

	if len(frame) != want || len(frame) < 4 || frame[0] != 0xFF || frame[1] != 0xD8 {
		return nil
	}
	return frame
}

func (a *assembler) reset() {
	a.frags = a.frags[:0]
	a.started = false
}
