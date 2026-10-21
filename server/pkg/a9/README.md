# A9 / V720 cameras

Fake cloud server for A9 / V720 "Naxclow" mini cameras. Protocol from https://github.com/intx82/a9-v720.

## Flow

```
camera                                server
  | HTTP POST /app/api/ApiServer/getA9ConfCheck  -> {"host": server IP, "tcpPort": 6123}
  | TCP  {"code":100,"uid":...}                  -> {"code":101,"status":200}
  | TCP  heartbeat every 5 s                     -> heartbeat
  |                                              <- TCP {"code":11} open a data channel
  | UDP  {"code":20} from a new port             -> {"code":21}
  | UDP  {"code":51}                             -> {"code":50}
  | UDP  {"code":51}                             -> TCP {"code":53}, forward 298, forward 4 (base info)
  | TCP  forward 4 + base info                   -> TCP forward 3 (open audio + video)
  | UDP  JPEG fragments + G.711 A-law            -> UDP 605 ack with received ids, every 100 ms
  |                                              <- TCP forward 0 (close)
```

## Porting to go2rtc

The package only uses `pkg/core` of go2rtc, plus zerolog and pion/rtp, which go2rtc already has. Copy it to `pkg/a9` and add `internal/a9/a9.go`:

```go
package a9

import (
	"net"
	"net/http"

	"github.com/AlexxIT/go2rtc/internal/api"
	"github.com/AlexxIT/go2rtc/internal/app"
	"github.com/AlexxIT/go2rtc/internal/streams"
	"github.com/AlexxIT/go2rtc/pkg/a9"
)

func Init() {
	var conf struct {
		Mod struct {
			Listen string `yaml:"listen"`
		} `yaml:"a9"`
	}
	app.LoadConfig(&conf)
	if conf.Mod.Listen == "" {
		return
	}

	srv := a9.NewServer()
	srv.Log = app.GetLogger("a9")

	streams.HandleFunc("a9", srv.Dial) // streams: { cam: a9:0800c0035826 }

	// The camera calls the bootstrap API on port 80, so go2rtc must listen there
	// or a proxy must forward /app/api/ to it.
	api.HandleFunc(a9.APIPrefix, func(w http.ResponseWriter, r *http.Request) { srv.HandleAPI(w, r) })

	tcp, _ := net.Listen("tcp", conf.Mod.Listen)
	go srv.ServeTCP(tcp)
	udpAddr, _ := net.ResolveUDPAddr("udp", conf.Mod.Listen)
	udp, _ := net.ListenUDP("udp", udpAddr)
	go srv.ServeUDP(udp)
}
```

`Producer` implements `sync.Locker` only for the small stream hub of the add-on. go2rtc does not use it.
