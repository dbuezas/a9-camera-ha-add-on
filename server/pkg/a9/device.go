package a9

import (
	"encoding/binary"
	"encoding/json"
	"maps"
	"net"
	"slices"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// Values the original app uses to identify the "client" (phone)
const (
	cliTarget = "00112233445566778899aabbccddeeff"
	cliToken  = "deadc0de"
)

const (
	ackInterval = 100 * time.Millisecond // the camera waits for acks to send more
	// restart the data channel if no frame arrives for this long
	watchdogTimeout = 5 * time.Second
	statsInterval   = 10 * time.Second
	// base info holds the battery level, so ask for it from time to time
	infoInterval = time.Minute
)

// Subscriber gets the media of a camera. Callbacks are called from the network goroutine
// and must not block. Each frame is a new slice, so it can be kept.
type Subscriber struct {
	OnVideo func(jpeg []byte)
	OnAudio func(alaw []byte) // G.711 A-law, 8000 Hz, mono
}

// Device is one connected camera
type Device struct {
	UID         string
	ConnectedAt time.Time

	srv  *Server
	log  zerolog.Logger
	conn net.Conn
	ip   net.IP

	out chan []byte // TCP write queue, keeps the order and never blocks the caller

	mu      sync.Mutex
	subs    []*Subscriber
	session *session
	udpAddr *net.UDPAddr
	info    map[string]any

	lastFrame   []byte
	lastFrameAt time.Time

	done      chan struct{}
	closeOnce sync.Once
}

// session is one "streaming wanted" period: from the first subscriber to the last
type session struct {
	stop      chan struct{}
	probed    bool // answered the first probe
	ready     bool // sent the command mode sequence
	opened    bool // sent "open audio and video"
	switched  bool // sent "video only" after the first frame
	natAt     time.Time
	lastFrame time.Time
	pids      []uint32 // received since the last ack
	acking    bool
	asm       assembler
	packets   int // media packets, for the stats log
	statsAt   time.Time
}

func newDevice(s *Server, uid string, conn net.Conn) *Device {
	d := &Device{
		UID:         uid,
		ConnectedAt: time.Now(),
		srv:         s,
		log:         s.Log.With().Str("uid", uid).Logger(),
		conn:        conn,
		out:         make(chan []byte, 64),
		done:        make(chan struct{}),
	}
	if addr, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		d.ip = addr.IP
	}
	go d.writeTCP()
	return d
}

// Online is false after the camera disconnected
func (d *Device) Online() bool {
	select {
	case <-d.done:
		return false
	default:
		return true
	}
}

// Done is closed when the camera disconnects
func (d *Device) Done() <-chan struct{} {
	return d.done
}

func (d *Device) RemoteAddr() string {
	return d.conn.RemoteAddr().String()
}

// Info returns the last "base info" the camera sent (firmware version, IR LED state...)
func (d *Device) Info() map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	return maps.Clone(d.info)
}

// LastFrame returns the newest JPEG frame and when it arrived
func (d *Device) LastFrame() ([]byte, time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lastFrame, d.lastFrameAt
}

// RefreshInfo asks the camera for its base info. The answer updates Info.
func (d *Device) RefreshInfo() {
	d.forward(Obj{{"unixTimer", time.Now().Unix()}, {"code", FwdBaseInfo}})
}

func (d *Device) pollInfo() {
	d.RefreshInfo()
	ticker := time.NewTicker(infoInterval)
	defer ticker.Stop()
	for {
		select {
		case <-d.done:
			return
		case <-ticker.C:
			d.RefreshInfo()
		}
	}
}

// notify tells the server owner that something changed. Must be called without d.mu locked.
func (d *Device) notify() {
	if f := d.srv.OnUpdate; f != nil {
		f(d)
	}
}

// Settings returns the server side settings of this camera
func (d *Device) Settings() Settings {
	return d.srv.settings.get(d.UID)
}

// SetAudio turns the camera audio on or off, also while streaming
func (d *Device) SetAudio(on bool) error {
	st := d.Settings()
	st.Audio = on
	if err := d.srv.settings.set(d.UID, st); err != nil {
		return err
	}
	d.mu.Lock()
	reopen := d.session != nil && d.session.opened
	d.mu.Unlock()
	if reopen {
		d.forward(Obj{{"code", d.openCode()}})
	}
	d.notify()
	return nil
}

func (d *Device) openCode() int {
	if d.Settings().Audio {
		return FwdOpenAV
	}
	return FwdOpenV
}

// Streaming tells if the camera is asked to send media
func (d *Device) Streaming() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.session != nil
}

// Subscribe starts the stream with the first subscriber
func (d *Device) Subscribe(sub *Subscriber) {
	d.mu.Lock()
	d.subs = append(d.subs, sub)
	started := d.session == nil
	if started {
		d.startSession()
	}
	d.mu.Unlock()
	if started {
		d.notify()
	}
}

// Unsubscribe stops the stream after the last subscriber
func (d *Device) Unsubscribe(sub *Subscriber) {
	d.mu.Lock()
	if i := slices.Index(d.subs, sub); i >= 0 {
		d.subs = slices.Delete(d.subs, i, i+1)
	}
	stopped := len(d.subs) == 0 && d.session != nil
	if stopped {
		close(d.session.stop)
		d.session = nil
		d.log.Debug().Msg("[a9] stop stream")
		d.forward(Obj{{"code", FwdCloseAV}})
	}
	d.mu.Unlock()
	if stopped {
		d.notify()
	}
}

// SendCommand sends a command to the camera, ex: {"code": 202, "IrLed": 1}
func (d *Device) SendCommand(content Obj) {
	d.forward(content)
}

// startSession must be called with d.mu locked
func (d *Device) startSession() {
	s := &session{stop: make(chan struct{}), statsAt: time.Now()}
	d.session = s
	d.log.Debug().Msg("[a9] start stream")
	d.requestNAT(s)
	go d.runSession(s)
}

// requestNAT asks the camera to open a new UDP data channel. Must be called with d.mu locked.
func (d *Device) requestNAT(s *session) {
	s.probed = false
	s.ready = false
	s.opened = false
	s.switched = false
	s.acking = false
	s.pids = s.pids[:0]
	s.natAt = time.Now()
	d.sendTCP(jsonPacket(Obj{
		{"code", CodeNatReq},
		{"cliTarget", cliTarget},
		{"cliToken", cliToken},
		// nobody else must connect
		{"cliIp", "255.255.255.255"},
		{"cliPort", 0},
		{"cliNatIp", "255.255.255.255"},
		{"cliNatPort", 0},
	}))
}

// waitingUDP tells if the camera was asked for a data channel and did not probe it yet
func (d *Device) waitingUDP() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.session != nil && !d.session.probed
}

func (d *Device) runSession(s *session) {
	ticker := time.NewTicker(ackInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stop:
			return
		case <-d.done:
			return
		case <-ticker.C:
		}

		d.mu.Lock()
		if s.acking {
			ack := make([]byte, 4*len(s.pids))
			for i, id := range s.pids {
				binary.LittleEndian.PutUint32(ack[4*i:], id)
			}
			s.pids = s.pids[:0]
			pkt := &Packet{Cmd: CmdRetransmitOK, ForwardID: defaultForwardID, Payload: ack}
			d.srv.sendUDP(pkt.Marshal(), d.udpAddr)
		}

		last := s.lastFrame
		if s.natAt.After(last) {
			last = s.natAt
		}
		if time.Since(s.statsAt) >= statsInterval {
			d.log.Debug().Int("frames", s.asm.Frames).Int("dropped", s.asm.Dropped).
				Int("packets", s.packets).Msg("[a9] stats")
			s.statsAt = time.Now()
		}
		if time.Since(last) > watchdogTimeout {
			d.log.Warn().Int("frames", s.asm.Frames).Int("dropped", s.asm.Dropped).
				Int("packets", s.packets).Msg("[a9] no video, restarting the data channel")
			d.requestNAT(s)
		}
		d.mu.Unlock()
	}
}

func (d *Device) readTCP() error {
	for {
		_ = d.conn.SetReadDeadline(time.Now().Add(tcpTimeout))
		pkt, err := ReadPacket(d.conn)
		if err != nil {
			return err
		}

		switch pkt.Cmd {
		case CmdHeartbeat:
			d.sendTCP(emptyPacket(CmdHeartbeat))
		case CmdJSON:
			d.handleTCPMessage(pkt)
		default:
			d.log.Debug().Msgf("[a9] tcp: unknown cmd=%d", pkt.Cmd)
		}
	}
}

func (d *Device) handleTCPMessage(pkt *Packet) {
	msg, err := ParseMessage(pkt)
	if err != nil {
		d.log.Debug().Msgf("[a9] tcp: bad json %q", pkt.Payload)
		return
	}

	switch msg.Code {
	case CodeNatRsp:
		d.log.Debug().Msgf("[a9] data channel from %s:%d", msg.DevNatIP, msg.DevNatPort)

	case CodeForward:
		switch msg.ContentCode() {
		case FwdBaseInfo:
			var info map[string]any
			_ = json.Unmarshal(msg.Content, &info)
			delete(info, "code")
			d.mu.Lock()
			d.info = info
			s := d.session
			open := s != nil && s.ready && !s.opened
			if open {
				s.opened = true
			}
			d.mu.Unlock()
			d.log.Debug().Msgf("[a9] base info %s", msg.Content)
			if open {
				// The camera does not start a stream in "video only" mode, so always start with
				// audio and video, and switch after the first frame if audio is off.
				d.forward(Obj{{"code", FwdOpenAV}})
			}
			d.notify()
		case FwdOpenAV, FwdOpenV:
			d.log.Debug().Msg("[a9] camera opened the stream")
		case FwdCloseAV:
			d.log.Debug().Msg("[a9] camera closed the stream")
		case FwdRetransmission:
		default:
			// The camera echoes a setting it applied, ex: {"code":202,"IrLed":1}
			var fields map[string]any
			if json.Unmarshal(msg.Content, &fields) != nil {
				break
			}
			delete(fields, "code")
			d.mu.Lock()
			if d.info == nil {
				d.info = map[string]any{}
			}
			maps.Copy(d.info, fields)
			d.mu.Unlock()
			d.log.Debug().Msgf("[a9] applied %s", msg.Content)
			d.notify()
		}

	default:
		d.log.Debug().Msgf("[a9] tcp: %s", pkt.Payload)
	}
}

func (d *Device) handleUDP(addr *net.UDPAddr, pkt *Packet) {
	d.mu.Lock()
	d.udpAddr = addr
	s := d.session

	switch pkt.Cmd {
	case CmdHeartbeat:
		d.mu.Unlock()
		d.srv.sendUDP(emptyPacket(CmdHeartbeat), addr)
		return

	case CmdJSON:
		d.mu.Unlock()
		d.handleUDPMessage(addr, pkt)
		return

	case CmdJPEG:
		if s == nil {
			break
		}
		s.packets++
		s.pids = append(s.pids, pkt.PkgID)
		frame := s.asm.push(pkt)
		if frame == nil {
			break
		}
		s.lastFrame = time.Now()
		s.acking = true
		d.lastFrame, d.lastFrameAt = frame, s.lastFrame
		videoOnly := !s.switched && !d.srv.settings.get(d.UID).Audio
		if videoOnly {
			s.switched = true
		}
		subs := slices.Clone(d.subs)
		d.mu.Unlock()
		if videoOnly {
			d.forward(Obj{{"code", FwdOpenV}})
		}
		for _, sub := range subs {
			if sub.OnVideo != nil {
				sub.OnVideo(frame)
			}
		}
		return

	case CmdG711:
		if s == nil {
			break
		}
		s.packets++
		s.pids = append(s.pids, pkt.PkgID)
		if !d.srv.settings.get(d.UID).Audio {
			break // late packets after turning audio off
		}
		audio := slices.Clone(pkt.Payload)
		subs := slices.Clone(d.subs)
		d.mu.Unlock()
		for _, sub := range subs {
			if sub.OnAudio != nil {
				sub.OnAudio(audio)
			}
		}
		return
	}

	d.mu.Unlock()
}

func (d *Device) handleUDPMessage(addr *net.UDPAddr, pkt *Packet) {
	msg, err := ParseMessage(pkt)
	if err != nil {
		return
	}

	switch msg.Code {
	case CodeUDPReq:
		d.srv.sendUDP(jsonPacket(Obj{{"code", CodeUDPRsp}, {"ip", "255.255.255.255"}, {"port", 0}}), addr)

	case CodeProbeRsp:
		d.mu.Lock()
		s := d.session
		var probe, ready bool
		if s != nil {
			if !s.probed {
				s.probed, probe = true, true
			} else if !s.ready {
				s.ready, ready = true, true
			}
		}
		d.mu.Unlock()

		if probe {
			d.srv.sendUDP(jsonPacket(Obj{{"code", CodeProbeReq}}), addr)
		}
		if ready {
			// switch the camera to command mode and ask for base info, the answer starts the stream
			d.sendTCP(jsonPacket(Obj{{"code", CodeDeviceStatus}, {"status", 1}}))
			d.forward(Obj{{"code", FwdRetransmission}})
			d.forward(Obj{{"unixTimer", time.Now().Unix()}, {"code", FwdBaseInfo}})
		}

	default:
		d.log.Debug().Msgf("[a9] udp: %s", pkt.Payload)
	}
}

func (d *Device) forward(content Obj) {
	d.sendTCP(jsonPacket(Obj{{"code", CodeForward}, {"target", cliTarget}, {"content", content}}))
}

func (d *Device) sendTCP(b []byte) {
	select {
	case d.out <- b:
	case <-d.done:
	default:
		d.log.Warn().Msg("[a9] tcp send queue full")
	}
}

func (d *Device) writeTCP() {
	for {
		select {
		case b := <-d.out:
			_ = d.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if _, err := d.conn.Write(b); err != nil {
				d.log.Debug().Err(err).Msg("[a9] tcp write")
				d.close()
				return
			}
		case <-d.done:
			return
		}
	}
}

func (d *Device) close() {
	d.closeOnce.Do(func() {
		close(d.done)
		_ = d.conn.Close()
	})
}
