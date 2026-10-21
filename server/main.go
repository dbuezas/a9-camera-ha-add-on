// a9-cam is a fake cloud server for A9 / V720 mini cameras.
// It serves the cameras as MJPEG, JPEG snapshot and RTSP (MJPEG + PCMA audio).
package main

import (
	"embed"
	"encoding/json"
	"flag"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // the add-on image has no time zone files, HA passes TZ

	"github.com/AlexxIT/go2rtc/pkg/core"
	"github.com/AlexxIT/go2rtc/pkg/magic"
	"github.com/AlexxIT/go2rtc/pkg/mjpeg"
	"github.com/AlexxIT/go2rtc/pkg/rtsp"
	"github.com/dbuezas/a9-camera-ha-add-on/server/internal/hub"
	"github.com/dbuezas/a9-camera-ha-add-on/server/pkg/a9"
	"github.com/rs/zerolog"
)

//go:embed web
var web embed.FS

var (
	log     zerolog.Logger
	server  *a9.Server
	streams *hub.Hub
)

func main() {
	httpAddr := flag.String("http", ":80", "HTTP address (the camera calls port 80)")
	camPort := flag.Int("port", 6123, "camera TCP and UDP port")
	rtspAddr := flag.String("rtsp", ":8556", "RTSP address, empty to disable")
	host := flag.String("host", "", "IP announced to the cameras (default: the IP they connected to)")
	level := flag.String("log", "", "log level: trace, debug, info, warn, error (default: add-on option or info)")
	flag.Parse()

	if *level == "" {
		*level = addonLogLevel()
	}
	lvl, err := zerolog.ParseLevel(*level)
	if err != nil || *level == "" {
		lvl = zerolog.InfoLevel
	}
	zerolog.TimeFieldFormat = time.RFC3339Nano // the console writer shows milliseconds
	log = zerolog.New(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: "15:04:05.000"}).
		Level(lvl).With().Timestamp().Logger()

	server = a9.NewServer()
	server.Log = log
	server.TCPPort = *camPort
	server.Host = *host
	server.OnUpdate = bus.publish

	streams = hub.New(func(name string) (core.Producer, error) {
		return server.Dial("a9:" + name)
	})

	camAddr := ":" + strconv.Itoa(*camPort)

	tcp, err := net.Listen("tcp", camAddr)
	if err != nil {
		log.Fatal().Err(err).Msg("[main] camera tcp")
	}
	go func() { log.Fatal().Err(server.ServeTCP(tcp)).Msg("[main] camera tcp") }()

	udpAddr, _ := net.ResolveUDPAddr("udp", camAddr)
	udp, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		log.Fatal().Err(err).Msg("[main] camera udp")
	}
	go func() { log.Fatal().Err(server.ServeUDP(udp)).Msg("[main] camera udp") }()

	if *rtspAddr != "" {
		ln, err := net.Listen("tcp", *rtspAddr)
		if err != nil {
			log.Fatal().Err(err).Msg("[main] rtsp")
		}
		go serveRTSP(ln)
	}

	ln, err := net.Listen("tcp", *httpAddr)
	if err != nil {
		log.Fatal().Err(err).Msg("[main] http")
	}
	log.Info().Str("http", *httpAddr).Int("camera", *camPort).Str("rtsp", *rtspAddr).Msg("[main] listen")
	go discover(ln.Addr().(*net.TCPAddr).Port)
	log.Fatal().Err(http.Serve(ln, routes())).Msg("[main] http")
}

func routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+a9.APIPrefix, server.HandleAPI)
	mux.HandleFunc("GET /dev/list", apiList)
	mux.HandleFunc("GET /dev/{uid}/live", apiMJPEG)
	mux.HandleFunc("GET /dev/{uid}/snapshot", apiSnapshot)
	mux.HandleFunc("GET /dev/{uid}/cmd", apiCommand)
	mux.HandleFunc("GET /api/devices", apiDevices)
	mux.HandleFunc("GET /api/events", apiEvents)
	mux.HandleFunc("POST /api/devices/{uid}/command", apiCommandJSON)
	mux.HandleFunc("POST /api/devices/{uid}/refresh", apiRefresh)
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, web, "web/index.html")
	})
	return mux
}

func apiList(w http.ResponseWriter, _ *http.Request) {
	type item struct {
		UID       string         `json:"uid"`
		Host      string         `json:"host"`
		Port      int            `json:"port"`
		Streaming bool           `json:"streaming"`
		Since     time.Time      `json:"connected_at"`
		Info      map[string]any `json:"info,omitempty"`
	}
	items := []item{}
	for _, d := range server.Devices() {
		host, port, _ := net.SplitHostPort(d.RemoteAddr())
		p, _ := strconv.Atoi(port)
		items = append(items, item{
			UID: d.UID, Host: host, Port: p, Streaming: d.Streaming(), Since: d.ConnectedAt, Info: d.Info(),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(items)
}

func apiMJPEG(w http.ResponseWriter, r *http.Request) {
	uid := r.PathValue("uid")
	cons := mjpeg.NewConsumer()
	cons.WithRequest(r)

	stream := streams.Get(uid)
	if err := stream.AddConsumer(cons); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer stream.RemoveConsumer(cons)

	log.Debug().Str("uid", uid).Str("addr", r.RemoteAddr).Msg("[http] mjpeg start")

	h := w.Header()
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "close")
	h.Set("Pragma", "no-cache")

	_, _ = cons.WriteTo(mjpeg.NewWriter(w))

	log.Debug().Str("uid", uid).Str("addr", r.RemoteAddr).Msg("[http] mjpeg stop")
}

// apiSnapshot returns a fresh frame. With ?max_age=SECONDS a recent frame is returned
// without waking the camera, which saves battery.
func apiSnapshot(w http.ResponseWriter, r *http.Request) {
	uid := r.PathValue("uid")
	if maxAge, err := strconv.Atoi(r.URL.Query().Get("max_age")); err == nil {
		if dev := server.Get(uid); dev != nil {
			if b, at := dev.LastFrame(); b != nil && time.Since(at) <= time.Duration(maxAge)*time.Second {
				writeJPEG(w, b)
				return
			}
		}
	}

	cons := magic.NewKeyframe()
	cons.WithRequest(r)

	stream := streams.Get(uid)
	if err := stream.AddConsumer(cons); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	// the first frame takes about 1 second, the camera needs a new data channel
	timer := time.AfterFunc(15*time.Second, func() { _ = cons.Stop() })
	once := &core.OnceBuffer{}
	_, _ = cons.WriteTo(once)
	timer.Stop()
	stream.RemoveConsumer(cons)

	b := once.Buffer()
	if len(b) == 0 {
		http.Error(w, "timeout", http.StatusGatewayTimeout)
		return
	}
	writeJPEG(w, mjpeg.FixJPEG(b))
}

func writeJPEG(w http.ResponseWriter, b []byte) {
	h := w.Header()
	h.Set("Content-Type", "image/jpeg")
	h.Set("Content-Length", strconv.Itoa(len(b)))
	h.Set("Cache-Control", "no-cache")
	_, _ = w.Write(b)
}

// apiCommand forwards the query to the camera, in the same order, ex: ?code=202&IrLed=1
func apiCommand(w http.ResponseWriter, r *http.Request) {
	dev := server.Get(r.PathValue("uid"))
	if dev == nil {
		http.Error(w, a9.ErrNotConnected.Error(), http.StatusServiceUnavailable)
		return
	}

	var cmd a9.Obj
	for _, pair := range strings.Split(r.URL.RawQuery, "&") {
		k, v, _ := strings.Cut(pair, "=")
		if k == "" {
			continue
		}
		var value any = v
		if i, err := strconv.Atoi(v); err == nil {
			value = i
		}
		cmd = append(cmd, a9.KV{Key: k, Value: value})
	}
	if len(cmd) == 0 {
		http.Error(w, "empty command", http.StatusBadRequest)
		return
	}

	dev.SendCommand(cmd)
	log.Info().Str("uid", dev.UID).Msgf("[http] command %s", r.URL.RawQuery)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(cmd)
}

func serveRTSP(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go handleRTSP(rtsp.NewServer(conn))
	}
}

func handleRTSP(conn *rtsp.Conn) {
	var stream *hub.Stream

	conn.Listen(func(msg any) {
		if msg != rtsp.MethodDescribe || len(conn.URL.Path) < 2 {
			return
		}
		name := conn.URL.Path[1:]
		conn.SessionName = "a9-cam"
		conn.Medias = []*core.Media{
			{Kind: core.KindVideo, Direction: core.DirectionSendonly, Codecs: []*core.Codec{{Name: core.CodecJPEG}}},
			{Kind: core.KindAudio, Direction: core.DirectionSendonly, Codecs: []*core.Codec{{Name: core.CodecPCMA}}},
		}
		s := streams.Get(name)
		if err := s.AddConsumer(conn); err != nil {
			log.Debug().Err(err).Str("uid", name).Msg("[rtsp]")
			return
		}
		stream = s
		log.Debug().Str("uid", name).Str("addr", conn.Connection.RemoteAddr).Msg("[rtsp] start")
	})

	if err := conn.Accept(); err != nil {
		if err != io.EOF {
			log.Debug().Err(err).Msg("[rtsp] accept")
		}
	} else if stream != nil {
		_ = conn.Handle()
	}

	if stream != nil {
		stream.RemoveConsumer(conn)
		log.Debug().Str("addr", conn.Connection.RemoteAddr).Msg("[rtsp] stop")
	}
	_ = conn.Close()
}
