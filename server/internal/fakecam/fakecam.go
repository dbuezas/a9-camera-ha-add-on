// Package fakecam simulates an A9 camera for tests. It follows the message order
// seen in captures of real cameras (firmware 202302031142 and 202303131805).
package fakecam

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dbuezas/a9-camera-ha-add-on/server/pkg/a9"
)

type Cam struct {
	UID string
	// Frames are sent in a loop while streaming. Default: one small fake JPEG.
	Frames [][]byte
	// DropFragment drops this fragment index of every frame (-1: none)
	DropFragment int

	tcp net.Conn
	srv string

	mu        sync.Mutex
	udp       *net.UDPConn
	streaming chan struct{}
	audio     bool
	pid       uint32

	Acks      atomic.Int32 // ack packets received
	AckedPIDs atomic.Int32
	Opens     atomic.Int32 // "open stream" commands
	AudioSent atomic.Int32
	Closes    atomic.Int32
	NATs      atomic.Int32
	Commands  chan map[string]any // forwarded commands other than the protocol ones

	state map[string]any // settings reported in base info
}

// FakeJPEG returns a real 320x240 JPEG, different for each seed
func FakeJPEG(seed byte) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 320, 240))
	for y := range 240 {
		for x := range 320 {
			v := byte(x*y) ^ seed*37
			img.Set(x, y, color.RGBA{R: v, G: byte(x) + seed, B: byte(y * 3), A: 255})
		}
	}
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80})
	return buf.Bytes()
}

// Dial connects to the server TCP address and registers
func Dial(addr, uid string) (*Cam, error) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	c := &Cam{
		UID:          uid,
		Frames:       [][]byte{FakeJPEG(1), FakeJPEG(2)},
		DropFragment: -1,
		tcp:          conn,
		srv:          addr,
		Commands:     make(chan map[string]any, 16),
		state: map[string]any{
			"IrLed": 0, "devPower": 100, "instLed": 1, "mirrorFlip": 4, "wifiName": "test", "version": "202302031142",
		},
	}
	c.sendTCP(map[string]any{"code": 100, "uid": uid, "token": "deadbeef", "domain": "v720.naxclow.com"})
	pkt, err := a9.ReadPacket(conn)
	if err != nil {
		return nil, err
	}
	if msg, _ := a9.ParseMessage(pkt); msg == nil || msg.Code != a9.CodeRegisterRsp {
		return nil, net.ErrClosed
	}
	go c.readTCP()
	return c, nil
}

func (c *Cam) Close() {
	_ = c.tcp.Close()
	c.mu.Lock()
	c.stopStreaming()
	if c.udp != nil {
		_ = c.udp.Close()
	}
	c.mu.Unlock()
}

func (c *Cam) sendTCP(v any) {
	b, _ := json.Marshal(v)
	_, _ = c.tcp.Write((&a9.Packet{Cmd: a9.CmdJSON, Payload: b}).Marshal())
}

func (c *Cam) sendUDP(p *a9.Packet) {
	c.mu.Lock()
	u := c.udp
	c.mu.Unlock()
	if u != nil {
		_, _ = u.Write(p.Marshal())
	}
}

func (c *Cam) sendUDPJSON(v any) {
	b, _ := json.Marshal(v)
	c.sendUDP(&a9.Packet{Cmd: a9.CmdJSON, Payload: b})
}

func (c *Cam) readTCP() {
	for {
		pkt, err := a9.ReadPacket(c.tcp)
		if err != nil {
			c.Close()
			return
		}
		if pkt.Cmd != a9.CmdJSON {
			continue
		}
		var msg map[string]any
		if json.Unmarshal(pkt.Payload, &msg) != nil {
			continue
		}
		switch code(msg) {
		case a9.CodeNatReq:
			c.NATs.Add(1)
			c.openUDP()
		case a9.CodeForward:
			content, _ := msg["content"].(map[string]any)
			switch code(content) {
			case a9.FwdBaseInfo:
				info := map[string]any{"code": 4}
				c.mu.Lock()
				for k, v := range c.state {
					info[k] = v
				}
				c.mu.Unlock()
				c.sendTCP(map[string]any{"code": 301, "target": msg["target"], "content": info})
			case a9.FwdOpenAV, a9.FwdOpenV:
				c.mu.Lock()
				running := c.streaming != nil
				c.mu.Unlock()
				if code(content) == a9.FwdOpenV && !running {
					// like real cameras: "video only" works on a running stream, but does not start one
					c.sendTCP(msg)
					continue
				}
				c.Opens.Add(1)
				c.mu.Lock()
				c.audio = code(content) == a9.FwdOpenAV
				c.mu.Unlock()
				c.sendTCP(msg)
				c.startStreaming()
			case a9.FwdCloseAV:
				c.Closes.Add(1)
				c.sendTCP(msg)
				c.mu.Lock()
				c.stopStreaming()
				c.mu.Unlock()
			case a9.FwdRetransmission:
			default:
				// a real camera applies the setting and echoes the command
				c.mu.Lock()
				for k, v := range content {
					if k != "code" {
						c.state[k] = v
					}
				}
				c.mu.Unlock()
				c.sendTCP(msg)
				select {
				case c.Commands <- content:
				default:
				}
			}
		}
	}
}

// openUDP does what the camera does after {"code": 11}: a new socket and port each time
func (c *Cam) openUDP() {
	raddr, _ := net.ResolveUDPAddr("udp", c.srv)
	u, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		return
	}
	c.mu.Lock()
	c.stopStreaming()
	if c.udp != nil {
		_ = c.udp.Close()
	}
	c.udp = u
	c.mu.Unlock()

	go c.readUDP(u)
	c.sendUDPJSON(map[string]any{"code": 20})
	c.sendTCP(map[string]any{"code": 12, "status": 200, "devNatIp": "127.0.0.1",
		"devNatPort": u.LocalAddr().(*net.UDPAddr).Port, "cliTarget": "00112233445566778899aabbccddeeff"})
	c.sendUDP(&a9.Packet{Cmd: a9.CmdHeartbeat})
	c.sendUDPJSON(map[string]any{"code": 51, "devTarget": c.UID, "status": 200})
}

func (c *Cam) readUDP(u *net.UDPConn) {
	b := make([]byte, 2048)
	for {
		n, err := u.Read(b)
		if err != nil {
			return
		}
		pkt, err := a9.Unmarshal(b[:n])
		if err != nil {
			continue
		}
		switch pkt.Cmd {
		case a9.CmdRetransmitOK:
			c.Acks.Add(1)
			c.AckedPIDs.Add(int32(len(pkt.Payload) / 4))
		case a9.CmdJSON:
			msg, _ := a9.ParseMessage(pkt)
			if msg != nil && msg.Code == a9.CodeProbeReq {
				c.sendUDPJSON(map[string]any{"code": 51, "devTarget": c.UID, "status": 200})
			}
		}
	}
}

// startStreaming sends video at 10 fps and audio every 100 ms
func (c *Cam) startStreaming() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.streaming != nil {
		return
	}
	stop := make(chan struct{})
	c.streaming = stop

	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for i := 0; ; i++ {
			c.sendFrame(c.Frames[i%len(c.Frames)])
			c.mu.Lock()
			audio := c.audio
			c.mu.Unlock()
			if audio {
				c.AudioSent.Add(1)
				c.sendUDP(&a9.Packet{Cmd: a9.CmdG711, MsgFlag: a9.FlagSingle, PkgID: c.nextPID(), Payload: make([]byte, 800)})
			}
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
		}
	}()
}

// stopStreaming must be called with c.mu locked
func (c *Cam) stopStreaming() {
	if c.streaming != nil {
		close(c.streaming)
		c.streaming = nil
	}
}

func (c *Cam) nextPID() uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.pid
	c.pid++
	return id
}

func (c *Cam) sendFrame(frame []byte) {
	tail := make([]byte, 5)
	binary.LittleEndian.PutUint32(tail[1:], uint32(len(frame)))
	data := append(append([]byte{}, frame...), tail...)

	for i := 0; len(data) > 0; i++ {
		n := min(1000, len(data))
		flag := byte(a9.FlagBody)
		if i == 0 {
			flag = a9.FlagHead
		}
		if n == len(data) {
			flag = a9.FlagEnd
		}
		pid := c.nextPID()
		if i != c.DropFragment {
			c.sendUDP(&a9.Packet{Cmd: a9.CmdJPEG, MsgFlag: flag, PkgID: pid, Payload: data[:n]})
		}
		data = data[n:]
	}
}

func code(m map[string]any) int {
	f, _ := m["code"].(float64)
	return int(f)
}
