// fakecam connects simulated cameras to a server, for development and the integration tests.
//
//	go run ./cmd/fakecam -server 127.0.0.1:6123 -uid 0800c0000001
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/dbuezas/a9-camera-ha-add-on/server/internal/fakecam"
)

func main() {
	addr := flag.String("server", "127.0.0.1:6123", "server camera port (TCP and UDP)")
	uid := flag.String("uid", "0800c0000001", "camera UID")
	flag.Parse()

	cam, err := fakecam.Dial(*addr, *uid)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("connected", *uid)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig
	cam.Close()
}
