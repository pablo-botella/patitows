// Command patitows is a multi-site web server: it reads its sites from an
// XML configuration, obtains and renews their certificates with ACME
// TLS-ALPN-01 and serves their mskblob resources. Blobs are replaced hot
// with the patito command.
package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/pablo-botella/patitows"
)

func main() {
	configPath := flag.String("config", "", "path to the XML configuration file (required)")
	flag.Parse()
	if *configPath == "" {
		log.Fatal("patitows: -config is required")
	}

	cfg, err := patitows.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("patitows: %v", err)
	}
	log.SetPrefix("patitows[" + cfg.ID + "] ")

	srv := patitows.NewServer(cfg)

	ctl, err := patitows.ListenControl(cfg.ID, srv)
	switch {
	case err == patitows.ErrControlUnsupported:
		log.Printf("warning: %v; instance id not enforced, patito cannot reach this instance", err)
	case err != nil:
		log.Fatal(err)
	default:
		defer ctl.Close()
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		s := <-sig
		log.Printf("%v received, stopping", s)
		if err := srv.Stop(); err != nil {
			log.Printf("stop: %v", err)
		}
	}()

	if err := srv.Run(); err != nil {
		log.Fatal(err)
	}
	log.Printf("stopped")
}
