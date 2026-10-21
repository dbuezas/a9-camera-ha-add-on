package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/dbuezas/a9-camera-ha-add-on/server/pkg/a9"
)

// deviceJSON is what the Home Assistant integration sees of a camera
type deviceJSON struct {
	UID         string         `json:"uid"`
	Online      bool           `json:"online"`
	Streaming   bool           `json:"streaming"`
	Host        string         `json:"host"`
	Port        int            `json:"port"`
	ConnectedAt time.Time      `json:"connected_at"`
	Info        map[string]any `json:"info"`
}

func toJSON(d *a9.Device) deviceJSON {
	host, port, _ := net.SplitHostPort(d.RemoteAddr())
	p, _ := strconv.Atoi(port)
	info := d.Info()
	if info == nil {
		info = map[string]any{}
	}
	return deviceJSON{
		UID: d.UID, Online: d.Online(), Streaming: d.Streaming(),
		Host: host, Port: p, ConnectedAt: d.ConnectedAt, Info: info,
	}
}

func apiDevices(w http.ResponseWriter, _ *http.Request) {
	items := []deviceJSON{}
	for _, d := range server.Devices() {
		items = append(items, toJSON(d))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(items)
}

// apiCommand sends a JSON object to the camera, ex: {"code":202,"IrLed":1}.
// The key order is kept, the camera wants "code" first.
func apiCommandJSON(w http.ResponseWriter, r *http.Request) {
	dev := server.Get(r.PathValue("uid"))
	if dev == nil {
		http.Error(w, a9.ErrNotConnected.Error(), http.StatusServiceUnavailable)
		return
	}
	cmd, err := decodeObj(r.Body)
	if err != nil || len(cmd) == 0 {
		http.Error(w, "body must be a JSON object with a code", http.StatusBadRequest)
		return
	}
	dev.SendCommand(cmd)
	log.Info().Str("uid", dev.UID).Msgf("[api] command %s", mustJSON(cmd))
	w.WriteHeader(http.StatusNoContent)
}

func apiRefresh(w http.ResponseWriter, r *http.Request) {
	dev := server.Get(r.PathValue("uid"))
	if dev == nil {
		http.Error(w, a9.ErrNotConnected.Error(), http.StatusServiceUnavailable)
		return
	}
	dev.RefreshInfo()
	w.WriteHeader(http.StatusNoContent)
}

func decodeObj(r interface{ Read([]byte) (int, error) }) (a9.Obj, error) {
	dec := json.NewDecoder(r)
	dec.UseNumber()
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, errors.New("not an object")
	}
	var obj a9.Obj
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, err
		}
		var v any
		if err = dec.Decode(&v); err != nil {
			return nil, err
		}
		if n, ok := v.(json.Number); ok {
			if i, err := n.Int64(); err == nil {
				v = i
			}
		}
		obj = append(obj, a9.KV{Key: t.(string), Value: v})
	}
	return obj, nil
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// events sends every device change to the connected integrations (Server-Sent Events)
type events struct {
	mu      sync.Mutex
	clients map[chan []byte]struct{}
}

var bus = &events{clients: map[chan []byte]struct{}{}}

func (e *events) publish(d *a9.Device) {
	b := mustJSON(toJSON(d))
	e.mu.Lock()
	defer e.mu.Unlock()
	for c := range e.clients {
		select {
		case c <- b:
		default:
			// a slow client loses the connection and gets the full state again on reconnect
			close(c)
			delete(e.clients, c)
		}
	}
}

func apiEvents(w http.ResponseWriter, r *http.Request) {
	c := make(chan []byte, 64)
	bus.mu.Lock()
	bus.clients[c] = struct{}{}
	bus.mu.Unlock()
	defer func() {
		bus.mu.Lock()
		if _, ok := bus.clients[c]; ok {
			delete(bus.clients, c)
			close(c)
		}
		bus.mu.Unlock()
	}()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	rc := http.NewResponseController(w)

	write := func(b []byte) error {
		var buf bytes.Buffer
		fmt.Fprintf(&buf, "event: device\ndata: %s\n\n", b)
		if _, err := w.Write(buf.Bytes()); err != nil {
			return err
		}
		return rc.Flush()
	}

	for _, d := range server.Devices() {
		if write(mustJSON(toJSON(d))) != nil {
			return
		}
	}

	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	for {
		select {
		case b, ok := <-c:
			if !ok || write(b) != nil {
				return
			}
		case <-ping.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil || rc.Flush() != nil {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}

// discover tells Home Assistant that the add-on runs, so the integration can set itself up
func discover(httpPort int) {
	token := os.Getenv("SUPERVISOR_TOKEN")
	if token == "" {
		return
	}
	body := mustJSON(map[string]any{
		"service": "a9_cam",
		"config":  map[string]any{"host": "127.0.0.1", "port": httpPort},
	})
	req, _ := http.NewRequest("POST", "http://supervisor/discovery", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Warn().Err(err).Msg("[main] discovery")
		return
	}
	_ = res.Body.Close()
	log.Debug().Int("status", res.StatusCode).Msg("[main] discovery")
}

// addonLogLevel reads the log level from the add-on options, if they exist
func addonLogLevel() string {
	b, err := os.ReadFile("/data/options.json")
	if err != nil {
		return ""
	}
	var opts struct {
		LogLevel string `json:"log_level"`
	}
	_ = json.Unmarshal(b, &opts)
	return opts.LogLevel
}
