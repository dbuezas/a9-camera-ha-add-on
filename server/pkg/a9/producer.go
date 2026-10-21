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
	var videoTS uint32
	var audioSeq uint16
	var audioTS uint32

	sub := &Subscriber{
		OnVideo: func(jpeg []byte) {
			p.Recv += len(jpeg)
			// Frames come in bursts. A step below one frame (10 fps) makes FFmpeg see
			// two frames with the same time, so keep at least one frame between them.
			ts := uint32(time.Since(start) * 90000 / time.Second)
			if videoTS != 0 && ts < videoTS+frameStep {
				ts = videoTS + frameStep
			}
			videoTS = ts
			p.write(core.KindVideo, &rtp.Packet{
				Header:  rtp.Header{Timestamp: ts},
				Payload: jpeg,
			})
		},
		OnAudio: func(alaw []byte) {
			p.Recv += len(alaw)
			audioSeq++
			p.write(core.KindAudio, &rtp.Packet{
				Header: rtp.Header{
					Version:        2,
					Marker:         true,
					SequenceNumber: audioSeq,
					Timestamp:      audioTS,
				},
				Payload: alaw,
			})
			audioTS += uint32(len(alaw)) // 1 byte = 1 sample
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
