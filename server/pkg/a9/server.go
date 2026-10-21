package a9

import (
	"errors"
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// The camera sends a TCP heartbeat every 5 seconds
const tcpTimeout = 30 * time.Second

// Server accepts camera connections. One Server can handle many cameras.
type Server struct {
	Log zerolog.Logger

	// TCPPort is announced to the camera by the bootstrap API
	TCPPort int
	// Host is announced to the camera by the bootstrap API.
	// Empty means: the local IP of the HTTP connection the camera used.
	Host string

	// SettingsPath is a JSON file for per camera settings. Empty: not saved.
	SettingsPath string

	settings settingsStore

	// OnUpdate is called when a camera connects, disconnects, reports its state,
	// or starts or stops streaming. It must not block.
	OnUpdate func(*Device)

	mu      sync.Mutex
	devices map[string]*Device // by UID
	udp     *net.UDPConn
	udpDevs map[string]*Device // by UDP address
}

func NewServer() *Server {
	s := &Server{
		Log:     zerolog.Nop(),
		TCPPort: 6123,
		devices: map[string]*Device{},
		udpDevs: map[string]*Device{},
	}
	s.settings.load()
	return s
}

// Get returns a connected device or nil
func (s *Server) Get(uid string) *Device {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.devices[uid]
}

// Devices returns all connected devices, sorted by UID
func (s *Server) Devices() []*Device {
	s.mu.Lock()
	devs := make([]*Device, 0, len(s.devices))
	for _, d := range s.devices {
		devs = append(devs, d)
	}
	s.mu.Unlock()
	slices.SortFunc(devs, func(a, b *Device) int { return strings.Compare(a.UID, b.UID) })
	return devs
}

// LoadSettings reads SettingsPath. Call it before serving.
func (s *Server) LoadSettings() {
	s.settings.path = s.SettingsPath
	s.settings.load()
}

// ServeTCP accepts control connections from cameras
func (s *Server) ServeTCP(ln net.Listener) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.handleTCP(conn)
	}
}

func (s *Server) handleTCP(conn net.Conn) {
	log := s.Log.With().Str("addr", conn.RemoteAddr().String()).Logger()

	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetKeepAlive(true)
		_ = tc.SetKeepAlivePeriod(15 * time.Second)
	}

	_ = conn.SetReadDeadline(time.Now().Add(tcpTimeout))
	pkt, err := ReadPacket(conn)
	if err != nil {
		log.Debug().Err(err).Msg("[a9] tcp: no registration")
		_ = conn.Close()
		return
	}
	msg, err := ParseMessage(pkt)
	if err != nil || msg.Code != CodeRegisterReq || msg.UID == "" {
		log.Warn().Msgf("[a9] tcp: expected registration, got cmd=%d %q", pkt.Cmd, pkt.Payload)
		_ = conn.Close()
		return
	}

	dev := newDevice(s, msg.UID, conn)
	dev.sendTCP(jsonPacket(map[string]any{"code": CodeRegisterRsp, "status": 200}))
	s.register(dev)

	dev.log.Info().Msg("[a9] camera connected")
	dev.notify()
	go dev.pollInfo()
	err = dev.readTCP()
	dev.log.Info().Err(err).Msg("[a9] camera disconnected")

	s.unregister(dev)
	dev.close()
	dev.notify()
}

func (s *Server) register(dev *Device) {
	s.mu.Lock()
	old := s.devices[dev.UID]
	s.devices[dev.UID] = dev
	s.mu.Unlock()

	if old != nil {
		// the camera reconnected before the old TCP connection timed out
		old.log.Debug().Msg("[a9] replaced by new connection")
		old.close()
	}
}

func (s *Server) unregister(dev *Device) {
	s.mu.Lock()
	if s.devices[dev.UID] == dev {
		delete(s.devices, dev.UID)
	}
	for addr, d := range s.udpDevs {
		if d == dev {
			delete(s.udpDevs, addr)
		}
	}
	s.mu.Unlock()
}

// ServeUDP reads the data channel of all cameras from one socket
func (s *Server) ServeUDP(conn *net.UDPConn) error {
	s.mu.Lock()
	s.udp = conn
	s.mu.Unlock()

	b := make([]byte, 2048)
	for {
		n, addr, err := conn.ReadFromUDP(b)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return err
			}
			continue
		}
		pkt, err := Unmarshal(b[:n])
		if err != nil {
			continue
		}
		s.handleUDP(addr, pkt)
	}
}

func (s *Server) handleUDP(addr *net.UDPAddr, pkt *Packet) {
	key := addr.String()

	s.mu.Lock()
	dev := s.udpDevs[key]
	s.mu.Unlock()

	if dev == nil {
		// a new data channel starts with {"code": 20}
		msg, err := ParseMessage(pkt)
		if pkt.Cmd != CmdJSON || err != nil || msg.Code != CodeUDPReq {
			return
		}
		if dev = s.deviceByIP(addr.IP); dev == nil {
			s.Log.Debug().Str("addr", key).Msg("[a9] udp: unknown camera")
			return
		}
		s.mu.Lock()
		for k, d := range s.udpDevs {
			if d == dev {
				delete(s.udpDevs, k) // old data channel of the same camera
			}
		}
		s.udpDevs[key] = dev
		s.mu.Unlock()
	}

	dev.handleUDP(addr, pkt)
}

// deviceByIP finds the camera of a new data channel. With many cameras behind one IP,
// the one that was asked to open a data channel wins.
func (s *Server) deviceByIP(ip net.IP) *Device {
	s.mu.Lock()
	var found []*Device
	for _, d := range s.devices {
		if d.ip.Equal(ip) {
			found = append(found, d)
		}
	}
	s.mu.Unlock()

	for _, d := range found {
		if d.waitingUDP() {
			return d
		}
	}
	if len(found) > 0 {
		return found[0]
	}
	return nil
}

func (s *Server) sendUDP(b []byte, addr *net.UDPAddr) {
	s.mu.Lock()
	conn := s.udp
	s.mu.Unlock()
	if conn != nil && addr != nil {
		_, _ = conn.WriteToUDP(b, addr)
	}
}
