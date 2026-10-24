// Command catcher runs Echo's real capture API on its own, for the Node
// connector's end-to-end test: the fixture Next.js app posts captures to it
// and the test reads them back from /v1/messages.
//
//	GOWORK=off go run ./connectors/node/fixtures/catcher -addr 127.0.0.1:18925 -mode seal
package main

import (
	"errors"
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18925", "listen address")
	mode := flag.String("mode", catcher.Seal, "seal or observe")
	token := flag.String("token", "", "required X-Echo-Token, if any")
	flag.Parse()

	srv := catcher.NewServer(catcher.NewInbox(1000), catcher.Config{Mode: *mode, Token: *token, Version: "fixture"})
	hs := &http.Server{Addr: *addr, Handler: srv, ReadHeaderTimeout: 5 * time.Second}
	log.Printf("echo catcher (%s) on http://%s", *mode, *addr)
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
