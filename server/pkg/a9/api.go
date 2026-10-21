package a9

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// APIPrefix is the path of the fake cloud API. The camera calls it on port 80.
const APIPrefix = "/app/api/"

// HandleAPI answers the bootstrap requests of the camera (POST /app/api/...)
func (s *Server) HandleAPI(w http.ResponseWriter, r *http.Request) {
	var data any

	switch strings.TrimPrefix(r.URL.Path, APIPrefix) {
	case "ApiSysDevicesBatch/registerDevices":
		// first boot only: the cloud gives the camera its UID
		data = fmt.Sprintf("0800c00%05d", rand.IntN(100000))

	case "ApiSysDevicesBatch/confirm", "ApiSysDevices/a9bindingAppDevice":

	case "ApiSysDevices/getDevInfo":
		// newer firmware (2024+) asks this first, then calls getA9ConfCheck.
		// userInfo_state is required (intx82/a9-v720#51), null ota fields mean no update.
		data = Obj{
			{"userInfo_state", 1},
			{"ota_url", nil},
			{"ota_version", nil},
		}
		s.Log.Info().Str("uid", r.URL.Query().Get("devicesCode")).Str("addr", r.RemoteAddr).Msg("[a9] dev info")

	case "ApiServer/getA9ConfCheck":
		// tells the camera where its TCP server is
		uid := r.URL.Query().Get("devicesCode")
		data = Obj{
			{"tcpPort", s.TCPPort},
			{"uid", uid},
			{"isBind", "8"},
			{"domain", "v720.naxclow.com"},
			{"updateUrl", nil},
			{"host", s.hostFor(r)},
			{"currTime", strconv.FormatInt(time.Now().Unix(), 10)},
			{"pwd", "deadbeef"},
			{"version", nil},
		}
		s.Log.Info().Str("uid", uid).Str("addr", r.RemoteAddr).Str("path", r.URL.Path).Msg("[a9] bootstrap")

	default:
		s.Log.Warn().Str("path", r.URL.Path).Msg("[a9] unknown api request")
		http.NotFound(w, r)
		return
	}

	body, _ := json.Marshal(Obj{{"code", 200}, {"message", "OK"}, {"data", data}})

	// The real server is nginx in front of Java and answers "HTTP/1.1 200 " without "OK".
	// Write the same bytes, the camera firmware is the only client.
	conn, rw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
		return
	}
	defer conn.Close()
	_, _ = fmt.Fprintf(rw, "HTTP/1.1 200 \r\n"+
		"Server: nginx/1.14.0 (Ubuntu)\r\n"+
		"Date: %s\r\n"+
		"Content-Type: application/json\r\n"+
		"Content-Length: %d\r\n"+
		"Connection: close\r\n\r\n%s",
		time.Now().UTC().Format(http.TimeFormat), len(body), body)
	_ = rw.Flush()
}

func (s *Server) hostFor(r *http.Request) string {
	if s.Host != "" {
		return s.Host
	}
	if addr, ok := r.Context().Value(http.LocalAddrContextKey).(*net.TCPAddr); ok {
		return addr.IP.String()
	}
	return ""
}
