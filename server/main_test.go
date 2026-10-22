package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AlexxIT/go2rtc/pkg/core"
	"github.com/AlexxIT/go2rtc/pkg/mjpeg"
	"github.com/AlexxIT/go2rtc/pkg/rtsp"
	"github.com/dbuezas/a9-camera-ha-add-on/server/internal/fakecam"
	"github.com/dbuezas/a9-camera-ha-add-on/server/internal/hub"
	"github.com/dbuezas/a9-camera-ha-add-on/server/pkg/a9"
	"github.com/pion/rtp"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

var (
	once    sync.Once
	camAddr string
	tsURL   string
	nextUID atomic.Int32
)

// setup starts the servers once for all tests, and a new fake camera for each test
func setup(t *testing.T) (*fakecam.Cam, string, string) {
	t.Helper()
	once.Do(func() {
		log = zerolog.Nop()
		server = a9.NewServer()
		server.OnUpdate = bus.publish
		streams = hub.New(func(name string) (core.Producer, error) { return server.Dial("a9:" + name) })

		var tcp net.Listener
		var udp *net.UDPConn
		for {
			var err error
			tcp, err = net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			if udp, err = net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: tcp.Addr().(*net.TCPAddr).Port}); err == nil {
				break
			}
			_ = tcp.Close()
		}
		go server.ServeTCP(tcp)
		go server.ServeUDP(udp)
		camAddr = tcp.Addr().String()
		tsURL = httptest.NewServer(routes()).URL
	})

	uid := fmt.Sprintf("0800c00%05d", nextUID.Add(1))
	cam, err := fakecam.Dial(camAddr, uid)
	require.NoError(t, err)
	t.Cleanup(cam.Close)
	require.Eventually(t, func() bool { return server.Get(uid) != nil }, 2*time.Second, 10*time.Millisecond)
	require.Greater(t, len(cam.Frames[0]), 3000, "frames must be split in fragments")
	return cam, uid, tsURL
}

func TestHTTPList(t *testing.T) {
	_, uid, url := setup(t)
	res, err := http.Get(url + "/dev/list")
	require.NoError(t, err)
	defer res.Body.Close()
	var list []map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&list))
	var found map[string]any
	for _, item := range list {
		if item["uid"] == uid {
			found = item
		}
	}
	require.NotNil(t, found)
	require.Equal(t, "127.0.0.1", found["host"])
}

func TestHTTPHomepage(t *testing.T) {
	_, _, url := setup(t)
	res, err := http.Get(url + "/")
	require.NoError(t, err)
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	require.Equal(t, 200, res.StatusCode)
	require.Contains(t, string(b), "<title>A9 Cameras</title>")
}

func TestHTTPSnapshot(t *testing.T) {
	cam, uid, url := setup(t)
	for range 3 { // each snapshot opens and closes the stream
		res, err := http.Get(url + "/dev/" + uid + "/snapshot")
		require.NoError(t, err)
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		require.Equal(t, 200, res.StatusCode)
		require.Equal(t, "image/jpeg", res.Header.Get("Content-Type"))
		_, err = jpeg.Decode(bytes.NewReader(b))
		require.NoError(t, err)
	}
	require.Eventually(t, func() bool { return cam.Closes.Load() == 3 }, 2*time.Second, 10*time.Millisecond)
	require.False(t, server.Get(uid).Streaming())
}

func TestHTTPSnapshotUnknown(t *testing.T) {
	_, _, url := setup(t)
	res, err := http.Get(url + "/dev/nope/snapshot")
	require.NoError(t, err)
	res.Body.Close()
	require.Equal(t, http.StatusServiceUnavailable, res.StatusCode)
}

func TestHTTPMJPEG(t *testing.T) {
	cam, uid, url := setup(t)

	// two viewers share one camera stream
	read := func() (*http.Response, *multipart.Reader) {
		res, err := http.Get(url + "/dev/" + uid + "/live")
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(res.Header.Get("Content-Type"), "multipart/x-mixed-replace"))
		return res, multipart.NewReader(res.Body, "frame")
	}
	res1, mr1 := read()
	res2, mr2 := read()

	for _, mr := range []*multipart.Reader{mr1, mr2, mr1, mr2} {
		part, err := mr.NextPart()
		require.NoError(t, err)
		b, _ := io.ReadAll(part)
		require.True(t, bytes.Equal(b, cam.Frames[0]) || bytes.Equal(b, cam.Frames[1]))
	}
	require.EqualValues(t, 1, cam.Opens.Load(), "one camera stream for two viewers")

	res1.Body.Close()
	time.Sleep(300 * time.Millisecond)
	require.True(t, server.Get(uid).Streaming(), "one viewer left")
	res2.Body.Close()
	require.Eventually(t, func() bool { return !server.Get(uid).Streaming() }, 3*time.Second, 20*time.Millisecond)
	require.Eventually(t, func() bool { return cam.Closes.Load() == 1 }, time.Second, 10*time.Millisecond)
}

// ?raw is for the web UI behind HA ingress: same parts, a content type HA does not touch
func TestHTTPMJPEGRaw(t *testing.T) {
	cam, uid, url := setup(t)

	res, err := http.Get(url + "/dev/" + uid + "/live?raw")
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, "application/octet-stream", res.Header.Get("Content-Type"))
	part, err := multipart.NewReader(res.Body, "frame").NextPart()
	require.NoError(t, err)
	b, _ := io.ReadAll(part)
	require.True(t, bytes.Equal(b, cam.Frames[0]) || bytes.Equal(b, cam.Frames[1]))
}

// The camera disconnects while a viewer watches: the viewer must be closed, not hang
func TestHTTPMJPEGCameraGone(t *testing.T) {
	cam, uid, url := setup(t)
	res, err := http.Get(url + "/dev/" + uid + "/live")
	require.NoError(t, err)
	defer res.Body.Close()
	br := bufio.NewReader(res.Body)
	_, err = br.ReadString('\n')
	require.NoError(t, err)

	cam.Close()
	done := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, br); close(done) }()
	select {
	case <-done:
	case <-time.After(35 * time.Second): // TCP read timeout of the camera connection is 30 s
		t.Fatal("viewer still open")
	}
}

func TestHTTPCommand(t *testing.T) {
	cam, uid, url := setup(t)
	res, err := http.Get(url + "/dev/" + uid + "/cmd?code=202&IrLed=1")
	require.NoError(t, err)
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	require.JSONEq(t, `{"code":202,"IrLed":1}`, string(b))

	select {
	case c := <-cam.Commands:
		require.EqualValues(t, 202, c["code"])
		require.EqualValues(t, 1, c["IrLed"])
	case <-time.After(2 * time.Second):
		t.Fatal("no command")
	}
}

func TestRTSP(t *testing.T) {
	cam, uid, _ := setup(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	go serveRTSP(ln)

	client := rtsp.NewClient("rtsp://" + ln.Addr().String() + "/" + uid)
	require.NoError(t, client.Dial())
	require.NoError(t, client.Describe())

	var kinds []string
	frames := make(chan []byte, 10)
	audio := make(chan int, 10)
	for _, media := range client.Medias {
		kinds = append(kinds, media.Kind+":"+media.Codecs[0].Name)
		track, err := client.GetTrack(media, media.Codecs[0])
		require.NoError(t, err)
		sender := core.NewSender(media, track.Codec)
		if media.Kind == core.KindVideo {
			sender.Handler = mjpeg.RTPDepay(func(p *rtp.Packet) { frames <- p.Payload })
		} else {
			sender.Handler = func(p *rtp.Packet) { audio <- len(p.Payload) }
		}
		sender.HandleRTP(track)
	}
	require.ElementsMatch(t, []string{"video:JPEG", "audio:PCMA"}, kinds)

	require.NoError(t, client.Play())
	go func() { _ = client.Start() }()

	for range 3 {
		select {
		case b := <-frames:
			// RTP JPEG rebuilds the headers, so compare the picture
			img, err := jpeg.Decode(bytes.NewReader(b))
			require.NoError(t, err)
			require.Equal(t, 320, img.Bounds().Dx())
		case <-time.After(5 * time.Second):
			t.Fatal("no rtsp video")
		}
	}
	select {
	case n := <-audio:
		require.Equal(t, 800, n)
	case <-time.After(2 * time.Second):
		t.Fatal("no rtsp audio")
	}

	_ = client.Stop()
	require.Eventually(t, func() bool { return cam.Closes.Load() == 1 }, 3*time.Second, 20*time.Millisecond)
}

func TestRTSPUnknown(t *testing.T) {
	setup(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	go serveRTSP(ln)

	client := rtsp.NewClient("rtsp://" + ln.Addr().String() + "/nope")
	require.NoError(t, client.Dial())
	require.Error(t, client.Describe())
	_ = client.Close()
}

func TestAPIEventsAndCommand(t *testing.T) {
	_, uid, url := setup(t)

	res, err := http.Get(url + "/api/events")
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, "text/event-stream", res.Header.Get("Content-Type"))
	events := make(chan map[string]any, 100)
	go func() {
		sc := bufio.NewScanner(res.Body)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				var d map[string]any
				_ = json.Unmarshal([]byte(data), &d)
				events <- d
			}
		}
	}()

	// waits for an event of this camera that matches
	wait := func(match func(d map[string]any) bool) map[string]any {
		t.Helper()
		timeout := time.After(3 * time.Second)
		for {
			select {
			case d := <-events:
				if d["uid"] == uid && match(d) {
					return d
				}
			case <-timeout:
				t.Fatal("no matching event")
			}
		}
	}

	// the base info asked on connect arrives as an event
	d := wait(func(d map[string]any) bool { return d["info"].(map[string]any)["version"] != nil })
	require.Equal(t, true, d["online"])
	require.EqualValues(t, 0, d["info"].(map[string]any)["IrLed"])

	res2, err := http.Post(url+"/api/devices/"+uid+"/command", "application/json", strings.NewReader(`{"code":202,"IrLed":1}`))
	require.NoError(t, err)
	res2.Body.Close()
	require.Equal(t, http.StatusNoContent, res2.StatusCode)

	// the camera echo updates the state right away
	wait(func(d map[string]any) bool { return d["info"].(map[string]any)["IrLed"] == float64(1) })

	res3, err := http.Get(url + "/api/devices")
	require.NoError(t, err)
	var list []map[string]any
	require.NoError(t, json.NewDecoder(res3.Body).Decode(&list))
	res3.Body.Close()
	for _, d := range list {
		if d["uid"] == uid {
			require.EqualValues(t, 1, d["info"].(map[string]any)["IrLed"])
		}
	}

	res4, err := http.Post(url+"/api/devices/"+uid+"/command", "application/json", strings.NewReader(`[1]`))
	require.NoError(t, err)
	res4.Body.Close()
	require.Equal(t, http.StatusBadRequest, res4.StatusCode)
}

func TestAPIEventsOffline(t *testing.T) {
	cam, uid, url := setup(t)
	res, err := http.Get(url + "/api/events")
	require.NoError(t, err)
	defer res.Body.Close()

	cam.Close()
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	deadline := time.Now().Add(3 * time.Second)
	for sc.Scan() && time.Now().Before(deadline) {
		if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
			var d map[string]any
			_ = json.Unmarshal([]byte(data), &d)
			if d["uid"] == uid && d["online"] == false {
				return
			}
		}
	}
	t.Fatal("no offline event")
}

func TestHTTPSnapshotMaxAge(t *testing.T) {
	cam, uid, url := setup(t)
	get := func(q string) []byte {
		res, err := http.Get(url + "/dev/" + uid + "/snapshot" + q)
		require.NoError(t, err)
		defer res.Body.Close()
		require.Equal(t, 200, res.StatusCode)
		b, _ := io.ReadAll(res.Body)
		return b
	}
	get("")
	require.EqualValues(t, 1, cam.Opens.Load())
	get("?max_age=60") // recent frame, the camera is not asked again
	require.EqualValues(t, 1, cam.Opens.Load())
	time.Sleep(1100 * time.Millisecond)
	get("?max_age=1") // too old
	require.EqualValues(t, 2, cam.Opens.Load())
}
