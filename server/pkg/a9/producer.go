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

// minFrameGap is the smallest time between two video frames, in 90 kHz units (50 ms).
// Frames closer together are dropped: FFmpeg needs a growing time for each frame.
const minFrameGap = 90000 / 20

// videoTS gives a frame its arrival time. The camera sends a burst of old frames when a stream
// starts. Times that run ahead of the clock make players hold the video back (a frozen picture),
// so the time is never moved forward; frames too close to the previous one are dropped.
func videoTS(prev, now uint32) (uint32, bool) {
	if prev != 0 && now < prev+minFrameGap {
		return 0, false
	}
	return now, true
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
	start := time.Now()
	var lastVideo uint32
	var audioSeq uint16
	var nextAudio uint32

	sub := &Subscriber{
		OnVideo: func(jpeg []byte) {
			p.Recv += len(jpeg)
			ts, ok := videoTS(lastVideo, uint32(time.Since(start)*90000/time.Second))
			if !ok {
				return
			}
			lastVideo = ts
			p.write(core.KindVideo, &rtp.Packet{
				Header:  rtp.Header{Timestamp: ts},
				Payload: jpeg,
			})
		},
		OnAudio: func(alaw []byte) {
			p.Recv += len(alaw)
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
