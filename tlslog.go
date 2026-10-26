package patitows

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// TLSSummaryInterval is how often rejected TLS handshakes are summarized.
const TLSSummaryInterval = time.Minute

// tlsErrorLog is the http.Server error log. A public port receives a steady
// stream of scanner handshakes; instead of one line each, the known kinds
// are counted and summarized every TLSSummaryInterval. Unknown handshake
// errors (ACME failures among them) and every other message are logged in
// full.
type tlsErrorLog struct {
	mu     sync.Mutex
	counts map[string]int
	ips    map[string]bool
	done   chan struct{}
	once   sync.Once
}

const handshakePrefix = "http: TLS handshake error from "

// Kinds of rejected handshakes, in summary order.
var tlsKinds = []struct{ name, match string }{
	{"no SNI", "missing server name"},
	{"unknown host", "not configured in HostWhitelist"},
	{"protocol", "unsupported application protocols"},
	{"version", "unsupported versions"},
	{"cipher", "no cipher suite supported"},
	{"aborted", "EOF"},
	{"aborted", "connection reset by peer"},
	{"aborted", "broken pipe"},
	{"aborted", "i/o timeout"},
	{"not TLS", "first record does not look like a TLS handshake"},
}

func newTLSErrorLog() *tlsErrorLog {
	t := &tlsErrorLog{counts: map[string]int{}, ips: map[string]bool{}, done: make(chan struct{})}
	go func() {
		tick := time.NewTicker(TLSSummaryInterval)
		defer tick.Stop()
		for {
			select {
			case <-tick.C:
				t.flush()
			case <-t.done:
				t.flush()
				return
			}
		}
	}()
	return t
}

func (t *tlsErrorLog) logger() *log.Logger { return log.New(t, "", 0) }

func (t *tlsErrorLog) stop() { t.once.Do(func() { close(t.done) }) }

func (t *tlsErrorLog) Write(p []byte) (int, error) {
	line := strings.TrimRight(string(p), "\n")
	rest, ok := strings.CutPrefix(line, handshakePrefix)
	if !ok {
		log.Print(line)
		return len(p), nil
	}
	addr, reason, _ := strings.Cut(rest, ": ")
	kind := ""
	for _, k := range tlsKinds {
		if strings.Contains(reason, k.match) {
			kind = k.name
			break
		}
	}
	if kind == "" {
		log.Print(line)
		return len(p), nil
	}
	ip := addr
	if i := strings.LastIndex(addr, ":"); i > 0 {
		ip = strings.Trim(addr[:i], "[]")
	}
	t.mu.Lock()
	t.counts[kind]++
	t.ips[ip] = true
	t.mu.Unlock()
	return len(p), nil
}

func (t *tlsErrorLog) flush() {
	t.mu.Lock()
	counts, ips := t.counts, len(t.ips)
	t.counts, t.ips = map[string]int{}, map[string]bool{}
	t.mu.Unlock()

	total := 0
	var parts []string
	seen := map[string]bool{}
	for _, k := range tlsKinds {
		if n := counts[k.name]; n > 0 && !seen[k.name] {
			seen[k.name] = true
			total += n
			parts = append(parts, fmt.Sprintf("%s %d", k.name, n))
		}
	}
	if total == 0 {
		return
	}
	log.Printf("TLS handshakes rejected: %d from %d IPs (%s)", total, ips, strings.Join(parts, ", "))
}
