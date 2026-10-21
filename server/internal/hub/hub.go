// Package hub is a small version of go2rtc internal/streams: one producer per stream,
// shared by all consumers, started with the first consumer and stopped after the last.
package hub

import (
	"errors"
	"slices"
	"sync"

	"github.com/AlexxIT/go2rtc/pkg/core"
)

type DialFunc func(name string) (core.Producer, error)

type Hub struct {
	dial    DialFunc
	mu      sync.Mutex
	streams map[string]*Stream
}

func New(dial DialFunc) *Hub {
	return &Hub{dial: dial, streams: map[string]*Stream{}}
}

// Get returns the stream with this name, it is created on first use
func (h *Hub) Get(name string) *Stream {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.streams[name]
	if s == nil {
		s = &Stream{name: name, dial: h.dial}
		h.streams[name] = s
	}
	return s
}

type Stream struct {
	name      string
	dial      DialFunc
	mu        sync.Mutex
	prod      core.Producer
	consumers []core.Consumer
}

var ErrNoTracks = errors.New("hub: codecs not matched")

func (s *Stream) AddConsumer(cons core.Consumer) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	prod := s.prod
	if prod == nil {
		var err error
		if prod, err = s.dial(s.name); err != nil {
			return err
		}
	}

	var matched bool
	for _, consMedia := range cons.GetMedias() {
		for _, prodMedia := range prod.GetMedias() {
			if prodMedia.Direction != core.DirectionRecvonly {
				continue
			}
			prodCodec, consCodec := prodMedia.MatchMedia(consMedia)
			if prodCodec == nil {
				continue
			}
			track, err := prod.GetTrack(prodMedia, prodCodec)
			if err != nil {
				continue
			}
			if err = addTrack(prod, cons, consMedia, consCodec, track); err != nil {
				continue
			}
			matched = true
			break
		}
	}

	if !matched {
		if s.prod == nil {
			_ = prod.Stop()
		}
		return ErrNoTracks
	}

	s.consumers = append(s.consumers, cons)
	if s.prod == nil {
		s.prod = prod
		go s.run(prod)
	}
	return nil
}

func (s *Stream) RemoveConsumer(cons core.Consumer) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if i := slices.Index(s.consumers, cons); i >= 0 {
		s.consumers = slices.Delete(s.consumers, i, i+1)
	}
	stopConsumer(s.prod, cons)
	if len(s.consumers) == 0 && s.prod != nil {
		_ = s.prod.Stop()
		s.prod = nil
	}
}

// run waits for the producer to end. If it ends by itself (camera gone), all consumers are closed.
func (s *Stream) run(prod core.Producer) {
	_ = prod.Start()

	s.mu.Lock()
	if s.prod != prod {
		s.mu.Unlock()
		return // stopped by RemoveConsumer
	}
	s.prod = nil
	consumers := s.consumers
	s.consumers = nil
	s.mu.Unlock()

	_ = prod.Stop()
	for _, cons := range consumers {
		_ = cons.Stop()
	}
}

// addTrack holds the producer lock if it has one: a running track must not get new
// children while it writes packets
func addTrack(prod core.Producer, cons core.Consumer, media *core.Media, codec *core.Codec, track *core.Receiver) error {
	if l, ok := prod.(sync.Locker); ok {
		l.Lock()
		defer l.Unlock()
	}
	return cons.AddTrack(media, codec, track)
}

// stopConsumer detaches the consumer senders from the producer tracks
func stopConsumer(prod core.Producer, cons core.Consumer) {
	if l, ok := prod.(sync.Locker); ok {
		l.Lock()
		defer l.Unlock()
	}
	_ = cons.Stop()
}
