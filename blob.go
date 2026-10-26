package patitows

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pablo-botella/mskblob"
)

// blobSlot holds the blob currently served for one <blob> resource and
// swaps it without interrupting requests: each request holds a reference
// to the blob it started with, and a replaced blob is closed when its last
// request finishes.
type blobSlot struct {
	cfg     *BlobConfig
	current atomic.Pointer[blobRef]
	mu      sync.Mutex // serializes replacements
}

type blobRef struct {
	blob    *mskblob.Blob
	path    string
	refs    atomic.Int64
	retired atomic.Bool
	once    sync.Once
}

func (r *blobRef) close() {
	r.once.Do(func() { r.blob.Close() })
}

func (r *blobRef) release() {
	if r.refs.Add(-1) == 0 && r.retired.Load() {
		r.close()
	}
}

func newBlobSlot(cfg *BlobConfig) *blobSlot {
	return &blobSlot{cfg: cfg}
}

// acquire returns the current blob with a reference taken, or nil when the
// slot has no blob. The caller must release it.
func (s *blobSlot) acquire() *blobRef {
	for {
		r := s.current.Load()
		if r == nil {
			return nil
		}
		r.refs.Add(1)
		if s.current.Load() == r {
			return r
		}
		r.release()
	}
}

// replace opens the blob at path and makes it current. On error the
// previous blob stays in place.
func (s *blobSlot) replace(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := mskblob.Open(path)
	if err != nil {
		return fmt.Errorf("blob %q: %w", s.cfg.ID, err)
	}
	old := s.current.Swap(&blobRef{blob: b, path: path})
	if old != nil {
		old.retired.Store(true)
		if old.refs.Load() == 0 {
			old.close()
		}
	}
	return nil
}

// path returns the file of the current blob, or "".
func (s *blobSlot) path() string {
	if r := s.current.Load(); r != nil {
		return r.path
	}
	return ""
}

// closeNow closes the current blob; used on shutdown.
func (s *blobSlot) closeNow() {
	if r := s.current.Swap(nil); r != nil {
		r.retired.Store(true)
		if r.refs.Load() == 0 {
			r.close()
		}
	}
}

// serve answers a request whose path starts with the slot's base. Only
// static entries are served; "/dir/" serves "dir/index.html" and "/dir"
// redirects to "/dir/" when that index exists.
func (s *blobSlot) serve(w http.ResponseWriter, r *http.Request) {
	ref := s.acquire()
	if ref == nil {
		http.Error(w, "resource not available", http.StatusServiceUnavailable)
		return
	}
	defer ref.release()

	rel := strings.TrimPrefix(r.URL.Path, s.cfg.Base)
	if rel == "" || strings.HasSuffix(rel, "/") {
		rel += "index.html"
	}
	it := ref.blob.GetByURL(rel)
	if it == nil || it.RestType&mskblob.Static == 0 {
		if idx := ref.blob.GetByURL(rel + "/index.html"); idx != nil && idx.RestType&mskblob.Static != 0 {
			target := r.URL.Path + "/"
			if r.URL.RawQuery != "" {
				target += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, target, http.StatusMovedPermanently)
			return
		}
		http.NotFound(w, r)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%08x"`, it.CRC32))
	http.ServeContent(w, r, it.URL, time.Time{}, ref.blob.Reader(it))
}
