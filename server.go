package patitows

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

const letsEncryptStaging = "https://acme-staging-v02.api.letsencrypt.org/directory"

// ShutdownTimeout bounds how long a stop waits for in-flight requests.
const ShutdownTimeout = 30 * time.Second

// Server is one running patitows instance.
type Server struct {
	cfg     *Config
	sites   map[string]*siteHandler // by host
	slots   map[string]*blobSlot    // by blob id
	servers []*http.Server
	stopped chan struct{}
	stop    sync.Once
}

type siteHandler struct {
	site  *Site
	slots []*blobSlot // longest base first
}

// NewServer prepares an instance from its configuration and opens every
// blob. A blob that fails to open is logged and answers 503 until it is
// replaced.
func NewServer(cfg *Config) *Server {
	s := &Server{
		cfg:     cfg,
		sites:   map[string]*siteHandler{},
		slots:   map[string]*blobSlot{},
		stopped: make(chan struct{}),
	}
	for _, site := range cfg.Sites {
		h := &siteHandler{site: site}
		for _, b := range site.Blobs {
			slot := newBlobSlot(b)
			if err := slot.replace(b.Path); err != nil {
				log.Printf("%v", err)
			} else {
				log.Printf("blob %q: %s", b.ID, b.Path)
			}
			s.slots[b.ID] = slot
			h.slots = append(h.slots, slot)
		}
		sortByBaseLength(h.slots)
		for _, host := range site.Hosts {
			s.sites[host] = h
		}
		log.Printf("site %q: %s", site.Name, strings.Join(site.Hosts, ", "))
	}
	return s
}

func sortByBaseLength(slots []*blobSlot) {
	for i := 1; i < len(slots); i++ {
		for j := i; j > 0 && len(slots[j].cfg.Base) > len(slots[j-1].cfg.Base); j-- {
			slots[j], slots[j-1] = slots[j-1], slots[j]
		}
	}
}

// Run starts the listeners and blocks until Stop is called or a listener
// fails.
func (s *Server) Run() error {
	m := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(s.cfg.Hosts()...),
		Cache:      autocert.DirCache(s.cfg.ACME.Cache),
		Email:      s.cfg.ACME.Email,
	}
	if s.cfg.ACME.Staging {
		m.Client = &acme.Client{DirectoryURL: letsEncryptStaging}
	}
	// m.TLSConfig() advertises the acme-tls/1 protocol, which is what makes
	// the TLS-ALPN-01 challenge work on the HTTPS listener itself.
	tlsConfig := m.TLSConfig()
	tlsConfig.MinVersion = tls.VersionTLS12

	errc := make(chan error, 2)
	if s.cfg.Listen.HTTP != "" {
		ln, err := net.Listen("tcp", s.cfg.Listen.HTTP)
		if err != nil {
			return err
		}
		srv := &http.Server{Handler: http.HandlerFunc(redirectToHTTPS), ReadHeaderTimeout: 10 * time.Second}
		s.servers = append(s.servers, srv)
		log.Printf("HTTP redirect listening on %s", s.cfg.Listen.HTTP)
		go func() { errc <- srv.Serve(ln) }()
	}
	ln, err := net.Listen("tcp", s.cfg.Listen.HTTPS)
	if err != nil {
		s.Stop()
		return err
	}
	srv := &http.Server{Handler: s, TLSConfig: tlsConfig, ReadHeaderTimeout: 10 * time.Second}
	s.servers = append(s.servers, srv)
	log.Printf("HTTPS listening on %s (staging=%v)", s.cfg.Listen.HTTPS, s.cfg.ACME.Staging)
	go func() { errc <- srv.ServeTLS(ln, "", "") }()

	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			<-s.stopped
			return nil
		}
		s.Stop()
		return err
	case <-s.stopped:
		return nil
	}
}

// Stop shuts the listeners down gracefully, waiting up to ShutdownTimeout
// for in-flight requests, then closes the blobs. It reports whether every
// request finished in time.
func (s *Server) Stop() error {
	var result error
	s.stop.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), ShutdownTimeout)
		defer cancel()
		for _, srv := range s.servers {
			if err := srv.Shutdown(ctx); err != nil {
				result = err
			}
		}
		for _, slot := range s.slots {
			slot.closeNow()
		}
		close(s.stopped)
	})
	return result
}

// ReloadBlob re-reads the configuration file and replaces the blob with
// the given id if its file changed. Any other change in the file needs a
// restart and is ignored here.
func (s *Server) ReloadBlob(id string) (string, error) {
	slot, ok := s.slots[id]
	if !ok {
		return "", fmt.Errorf("unknown blob id %q", id)
	}
	cfg, err := LoadConfig(s.cfg.Path)
	if err != nil {
		return "", err
	}
	b := cfg.Blob(id)
	if b == nil {
		return "", fmt.Errorf("blob id %q no longer in %s (restart needed)", id, s.cfg.Path)
	}
	if b.Path == slot.path() {
		return b.Path, nil
	}
	if err := slot.replace(b.Path); err != nil {
		return "", err
	}
	slot.cfg.File, slot.cfg.Path = b.File, b.Path
	log.Printf("blob %q replaced: %s", id, b.Path)
	return b.Path, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h, ok := s.sites[requestHost(r)]
	if !ok {
		http.Error(w, "unknown host", http.StatusMisdirectedRequest)
		return
	}
	for _, slot := range h.slots {
		if strings.HasPrefix(r.URL.Path, slot.cfg.Base) {
			slot.serve(w, r)
			return
		}
		if r.URL.Path+"/" == slot.cfg.Base {
			http.Redirect(w, r, slot.cfg.Base, http.StatusMovedPermanently)
			return
		}
	}
	http.NotFound(w, r)
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
