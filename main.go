// Command patitows is a multi-site web server. This first version reads the
// sites from an XML configuration, obtains and renews their certificates
// with ACME TLS-ALPN-01 and answers each site with a test page.
package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

const letsEncryptStaging = "https://acme-staging-v02.api.letsencrypt.org/directory"

func main() {
	configPath := flag.String("config", "", "path to the XML configuration file (required)")
	flag.Parse()
	if *configPath == "" {
		log.Fatal("patitows: -config is required")
	}

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("patitows: %v", err)
	}

	m := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(cfg.Hosts()...),
		Cache:      autocert.DirCache(cfg.ACME.Cache),
		Email:      cfg.ACME.Email,
	}
	if cfg.ACME.Staging {
		m.Client = &acme.Client{DirectoryURL: letsEncryptStaging}
	}

	// m.TLSConfig() advertises the acme-tls/1 protocol, which is what makes
	// the TLS-ALPN-01 challenge work on the HTTPS listener itself.
	tlsConfig := m.TLSConfig()
	tlsConfig.MinVersion = tls.VersionTLS12

	if cfg.Listen.HTTP != "" {
		go func() {
			srv := &http.Server{
				Addr:              cfg.Listen.HTTP,
				Handler:           http.HandlerFunc(redirectToHTTPS),
				ReadHeaderTimeout: 10 * time.Second,
			}
			log.Printf("patitows: HTTP redirect listening on %s", cfg.Listen.HTTP)
			log.Fatal(srv.ListenAndServe())
		}()
	}

	srv := &http.Server{
		Addr:              cfg.Listen.HTTPS,
		Handler:           newRouter(cfg.Sites),
		TLSConfig:         tlsConfig,
		ReadHeaderTimeout: 10 * time.Second,
	}
	for _, s := range cfg.Sites {
		log.Printf("patitows: site %q: %s", s.Name, strings.Join(s.Hosts, ", "))
	}
	log.Printf("patitows: HTTPS listening on %s (staging=%v)", cfg.Listen.HTTPS, cfg.ACME.Staging)
	log.Fatal(srv.ListenAndServeTLS("", ""))
}

// router dispatches each request to its site by host header.
type router struct {
	sites map[string]*Site
}

func newRouter(sites []*Site) *router {
	r := &router{sites: map[string]*Site{}}
	for _, s := range sites {
		for _, h := range s.Hosts {
			r.sites[h] = s
		}
	}
	return r
}

func (rt *router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	site, ok := rt.sites[requestHost(r)]
	if !ok {
		http.Error(w, "unknown host", http.StatusMisdirectedRequest)
		return
	}
	testPage(w, r, site)
}

// requestHost returns the lowercase host header without port.
func requestHost(r *http.Request) string {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.ToLower(strings.TrimSuffix(host, "."))
}

func redirectToHTTPS(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "https://"+requestHost(r)+r.URL.RequestURI(), http.StatusMovedPermanently)
}

func testPage(w http.ResponseWriter, r *http.Request, site *Site) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "patitows\nsite: %s\nhost: %s\nproto: %s\ntls: %v\n", site.Name, r.Host, r.Proto, r.TLS != nil)
}
