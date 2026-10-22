package a9

import (
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/AlexxIT/go2rtc/pkg/core"
	"github.com/pion/rtp"
)

// Producer is a go2rtc producer for one camera: JPEG video and PCMA audio
type Producer struct {
	core.Connection

	dev  *Device
	mu   sync.Mutex // guards Receivers and the packet writes
	stop chan struct{}
	once sync.Once
}

// Lock and Unlock let the caller add consumers to running tracks safely.
// go2rtc core.Receiver reads its children without a lock while it writes packets.
func (p *Producer) Lock()   { p.mu.Lock() }
func (p *Producer) Unlock() { p.mu.Unlock() }

var ErrNotConnected = errors.New("a9: camera not connected")

// frameStep is one frame at the camera's 10 fps, in 90 kHz units
const frameStep = 90000 / 10

// videoTS gives a frame its arrival time, rounded to whole frames (10 fps), and at least one
// frame after the previous one. FFmpeg guesses the frame rate from the first frames: with
// irregular times it sometimes guesses 90000 fps and then sends hundreds of copies of each
// frame, which freezes WebRTC players. Whole frame steps make it guess 10 fps.
// After a burst the times run ahead of the clock for a moment, then the clock catches up.
func videoTS(prev uint32, first bool, now uint32) uint32 {
	ts := (now + frameStep/2) / frameStep * frameStep
	if !first && ts < prev+frameStep {
		ts = prev + frameStep
	}
	return ts
}

// audioTS gives an audio packet its time: the previous time plus its length, or the clock
// after lost packets, so audio does not fall behind the video.
func audioTS(next, now uint32, samples int) uint32 {
	if now > next+2*uint32(samples) {
		return now
	}
	return next
}

// Dial opens a producer for "a9:UID" or "a9://UID"
func (s *Server) Dial(rawURL string) (core.Producer, error) {
	uid := strings.TrimPrefix(strings.TrimPrefix(rawURL, "a9:"), "//")
	dev := s.Get(uid)
	if dev == nil {
		return nil, ErrNotConnected
	}
	return NewProducer(dev), nil
}

func NewProducer(dev *Device) *Producer {
	return &Producer{
		Connection: core.Connection{
			ID:         core.NewID(),
			FormatName: "a9",
			Protocol:   "udp",
			RemoteAddr: dev.RemoteAddr(),
			Source:     "a9:" + dev.UID,
			Medias: []*core.Media{
				{
					Kind:      core.KindVideo,
					Direction: core.DirectionRecvonly,
					Codecs: []*core.Codec{
						{Name: core.CodecJPEG, ClockRate: 90000, PayloadType: core.PayloadTypeRAW},
					},
				},
				{
					Kind:      core.KindAudio,
					Direction: core.DirectionRecvonly,
					Codecs: []*core.Codec{
						{Name: core.CodecPCMA, ClockRate: 8000},
					},
				},
			},
		},
		dev:  dev,
		stop: make(chan struct{}),
	}
}

func (p *Producer) GetTrack(media *core.Media, codec *core.Codec) (*core.Receiver, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.Connection.GetTrack(media, codec)
}

// write sends a packet to the receiver of this kind, if a consumer wants it
func (p *Producer) write(kind string, pkt *rtp.Packet) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, r := range p.Receivers {
		if r.Codec.Kind() == kind {
			r.WriteRTP(pkt)
			return
		}
	}
}

// Start blocks until Stop or until the camera disconnects
func (p *Producer) Start() error {
	// times start at the first video frame. Audio before it is dropped: FFmpeg would fill
	// the gap with copies of the first frame and send them as one burst.
	var start time.Time
	var lastVideo uint32
	var audioSeq uint16
	var nextAudio uint32

	sub := &Subscriber{
		OnVideo: func(jpeg []byte) {
			p.Recv += len(jpeg)
			first := start.IsZero()
			if first {
				start = time.Now()
			}
			ts := videoTS(lastVideo, first, uint32(time.Since(start)*90000/time.Second))
			lastVideo = ts
			p.write(core.KindVideo, &rtp.Packet{
				Header:  rtp.Header{Timestamp: ts},
				Payload: jpeg,
			})
		},
		OnAudio: func(alaw []byte) {
			p.Recv += len(alaw)
			if start.IsZero() {
				return
			}
			ts := audioTS(nextAudio, uint32(time.Since(start)*8000/time.Second), len(alaw))
			audioSeq++
			p.write(core.KindAudio, &rtp.Packet{
				Header: rtp.Header{
					Version:        2,
					Marker:         true,
					SequenceNumber: audioSeq,
					Timestamp:      ts,
				},
				Payload: alaw,
			})
			nextAudio = ts + uint32(len(alaw)) // 1 byte = 1 sample
		},
	}

	p.dev.Subscribe(sub)
	defer p.dev.Unsubscribe(sub)

	select {
	case <-p.stop:
		return nil
	case <-p.dev.Done():
		return ErrNotConnected
	}
}

func (p *Producer) Stop() error {
	p.once.Do(func() { close(p.stop) })
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, r := range p.Receivers {
		r.Close()
	}
	return nil
}
