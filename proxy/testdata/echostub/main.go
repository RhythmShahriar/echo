// Command echostub stands in for `rhythm-echo up` in the Docker Compose
// seal test until the CLI exists: the catcher API, the SMTP sink and the
// proxy, with the flags docs/seal/examples use.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/proxy"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "up" {
		fmt.Fprintln(os.Stderr, "usage: echostub up [flags]")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("up", flag.ExitOnError)
	observe := fs.Bool("observe", false, "")
	_ = fs.Bool("seal", true, "")
	listen := fs.String("listen", "127.0.0.1:8025", "")
	smtpAddr := fs.String("smtp", "127.0.0.1:1025", "")
	proxyAddr := fs.String("proxy", "", "")
	mitm := fs.Bool("mitm", false, "")
	token := fs.String("token", "", "")
	allow := fs.String("allow", "", "")
	_ = fs.Parse(os.Args[2:])

	mode := catcher.Seal
	if *observe {
		mode = catcher.Observe
	}
	var allowed []string
	if *allow != "" {
		allowed = strings.Split(*allow, ",")
	}
	s := catcher.NewServer(catcher.NewInbox(1000), catcher.Config{Mode: mode, Allow: allowed, Token: *token})
	go func() { log.Fatal(catcher.NewSMTP(s, *smtpAddr).ListenAndServe()) }()
	if *proxyAddr != "" {
		var ca *proxy.CA
		if *mitm {
			var err error
			if ca, err = proxy.LoadCA(""); err != nil {
				log.Fatal(err)
			}
		}
		p, err := proxy.New(proxy.Options{Server: s, CA: ca, MITM: *mitm, Allow: allowed, Token: *token})
		if err != nil {
			log.Fatal(err)
		}
		go func() { log.Fatal(p.ListenAndServe(*proxyAddr)) }()
	}
	srv := &http.Server{Addr: *listen, Handler: s, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("echostub %s: api %s smtp %s proxy %s", mode, *listen, *smtpAddr, *proxyAddr)
	log.Fatal(srv.ListenAndServe())
}
