package a9_test

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dbuezas/a9-camera-ha-add-on/server/internal/fakecam"
	"github.com/dbuezas/a9-camera-ha-add-on/server/pkg/a9"
	"github.com/stretchr/testify/require"
)

// startServer listens on a free port, TCP and UDP on the same number like the real setup
func startServer(t *testing.T) (*a9.Server, string) {
	t.Helper()
	for range 20 {
		tcp, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		addr := tcp.Addr().(*net.TCPAddr)
		udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: addr.IP, Port: addr.Port})
		if err != nil {
			_ = tcp.Close()
			continue
		}
		srv := a9.NewServer()
		srv.TCPPort = addr.Port
		go srv.ServeTCP(tcp)
		go srv.ServeUDP(udp)
		t.Cleanup(func() { _ = tcp.Close(); _ = udp.Close() })
		return srv, addr.String()
	}
	t.Fatal("no free port")
	return nil, ""
}

func waitDevice(t *testing.T, srv *a9.Server, uid string) *a9.Device {
	t.Helper()
	var dev *a9.Device
	require.Eventually(t, func() bool { dev = srv.Get(uid); return dev != nil }, 2*time.Second, 10*time.Millisecond)
	return dev
}

func TestStream(t *testing.T) {
	srv, addr := startServer(t)
	cam, err := fakecam.Dial(addr, "0800c0035826")
	require.NoError(t, err)
	defer cam.Close()

	dev := waitDevice(t, srv, "0800c0035826")
	require.False(t, dev.Streaming())

	video := make(chan []byte, 100)
	audio := make(chan []byte, 100)
	sub := &a9.Subscriber{
		OnVideo: func(b []byte) { video <- b },
		OnAudio: func(b []byte) { audio <- b },
	}
	dev.Subscribe(sub)
	require.True(t, dev.Streaming())

	for i := range 6 {
		select {
		case f := <-video:
			require.Equal(t, cam.Frames[i%2], f)
		case <-time.After(3 * time.Second):
			t.Fatal("no video")
		}
	}
	require.Len(t, <-audio, 800)

	// the camera must get acks for the packets it sent
	require.Eventually(t, func() bool { return cam.AckedPIDs.Load() > 20 }, time.Second, 10*time.Millisecond)
	require.EqualValues(t, 1, cam.NATs.Load())
	require.EqualValues(t, 1, cam.Opens.Load())

	// base info is kept
	require.Equal(t, "202302031142", dev.Info()["version"])

	dev.Unsubscribe(sub)
	require.Eventually(t, func() bool { return cam.Closes.Load() == 1 }, time.Second, 10*time.Millisecond)
	require.False(t, dev.Streaming())

	// a new subscriber opens a new data channel, like the real camera wants
	video2 := make(chan []byte, 100)
	sub2 := &a9.Subscriber{OnVideo: func(b []byte) { video2 <- b }}
	dev.Subscribe(sub2)
	select {
	case <-video2:
	case <-time.After(3 * time.Second):
		t.Fatal("no video after restart")
	}
	require.EqualValues(t, 2, cam.NATs.Load())
	dev.Unsubscribe(sub2)
}

func TestTwoCameras(t *testing.T) {
	srv, addr := startServer(t)
	cams := map[string]*fakecam.Cam{}
	for i, uid := range []string{"0800c0035826", "0800c0035072"} {
		cam, err := fakecam.Dial(addr, uid)
		require.NoError(t, err)
		cam.Frames = [][]byte{fakecam.FakeJPEG(byte(10 + i))}
		defer cam.Close()
		cams[uid] = cam
	}
	require.Eventually(t, func() bool { return len(srv.Devices()) == 2 }, 2*time.Second, 10*time.Millisecond)

	// Both cameras are on 127.0.0.1 here, so the data channel can only be matched by
	// IP for one camera at a time. On a real network each camera has its own IP.
	for uid, cam := range cams {
		dev := srv.Get(uid)
		got := make(chan []byte, 10)
		sub := &a9.Subscriber{OnVideo: func(b []byte) { got <- b }}
		dev.Subscribe(sub)
		select {
		case f := <-got:
			require.Equal(t, cam.Frames[0], f, uid)
		case <-time.After(3 * time.Second):
			t.Fatal("no video from", uid)
		}
		dev.Unsubscribe(sub)
		require.Eventually(t, func() bool { return cam.Closes.Load() == 1 }, time.Second, 10*time.Millisecond)
	}
}

// When fragments are lost the frames are dropped, then the watchdog opens a new data channel
func TestWatchdog(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	srv, addr := startServer(t)
	cam, err := fakecam.Dial(addr, "0800c0035826")
	require.NoError(t, err)
	defer cam.Close()
	cam.DropFragment = 1 // every frame broken

	dev := waitDevice(t, srv, "0800c0035826")
	sub := &a9.Subscriber{OnVideo: func([]byte) {}}
	dev.Subscribe(sub)
	defer dev.Unsubscribe(sub)

	require.Eventually(t, func() bool { return cam.NATs.Load() >= 2 }, 8*time.Second, 50*time.Millisecond)
}

func TestReconnect(t *testing.T) {
	srv, addr := startServer(t)
	cam, err := fakecam.Dial(addr, "0800c0035826")
	require.NoError(t, err)
	dev := waitDevice(t, srv, "0800c0035826")

	// same camera connects again (ex: after a WiFi drop), the old connection is dropped
	cam2, err := fakecam.Dial(addr, "0800c0035826")
	require.NoError(t, err)
	defer cam2.Close()

	select {
	case <-dev.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("old device not closed")
	}
	require.Eventually(t, func() bool { d := srv.Get("0800c0035826"); return d != nil && d != dev }, time.Second, 10*time.Millisecond)

	cam.Close()
	time.Sleep(50 * time.Millisecond)
	require.NotNil(t, srv.Get("0800c0035826"), "closing the old connection must not remove the new one")

	cam2.Close()
	require.Eventually(t, func() bool { return srv.Get("0800c0035826") == nil }, 2*time.Second, 10*time.Millisecond)
}

func TestCommand(t *testing.T) {
	srv, addr := startServer(t)
	cam, err := fakecam.Dial(addr, "0800c0035826")
	require.NoError(t, err)
	defer cam.Close()

	dev := waitDevice(t, srv, "0800c0035826")
	dev.SendCommand(a9.Obj{{Key: "code", Value: 202}, {Key: "IrLed", Value: 1}})

	select {
	case c := <-cam.Commands:
		require.EqualValues(t, 202, c["code"])
		require.EqualValues(t, 1, c["IrLed"])
	case <-time.After(2 * time.Second):
		t.Fatal("no command")
	}
}

func TestRegistrationGarbage(t *testing.T) {
	srv, addr := startServer(t)
	conn, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	_, _ = conn.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\n\r\n"))
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err = io.ReadAll(conn)
	// closed by the server: EOF or reset, but not a timeout
	var ne net.Error
	require.False(t, errors.As(err, &ne) && ne.Timeout(), err)
	require.Empty(t, srv.Devices())
}

func TestAPI(t *testing.T) {
	srv := a9.NewServer()
	ts := httptest.NewServer(http.HandlerFunc(srv.HandleAPI))
	defer ts.Close()

	res, err := http.Post(ts.URL+"/app/api/ApiServer/getA9ConfCheck?devicesCode=0800c0035826&random=BCDEFG&token=dc4501c89a", "", nil)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, 200, res.StatusCode)
	require.Equal(t, "application/json", res.Header.Get("Content-Type"))

	var body struct {
		Code int `json:"code"`
		Data struct {
			TCPPort int    `json:"tcpPort"`
			UID     string `json:"uid"`
			Host    string `json:"host"`
		} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.Equal(t, 200, body.Code)
	require.Equal(t, 6123, body.Data.TCPPort)
	require.Equal(t, "0800c0035826", body.Data.UID)
	require.Equal(t, "127.0.0.1", body.Data.Host) // the IP the camera connected to

	res2, err := http.Post(ts.URL+"/app/api/ApiSysDevicesBatch/registerDevices?batch=A9_X4_V12", "", nil)
	require.NoError(t, err)
	b, _ := io.ReadAll(res2.Body)
	res2.Body.Close()
	require.True(t, strings.Contains(string(b), `"data":"0800c00`), string(b))

	res3, err := http.Post(ts.URL+"/app/api/Unknown", "", nil)
	require.NoError(t, err)
	res3.Body.Close()
	require.Equal(t, 404, res3.StatusCode)
}
