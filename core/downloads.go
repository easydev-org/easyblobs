package easyblobkit

import (
	"io"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// ActiveDownload is one in-flight upstream fetch being streamed into the CAS
// (and, when a client tee is attached, to the client too). It is the wire shape
// of GET /artifacts/system/downloads.
type ActiveDownload struct {
	ID      int64     `json:"id"`
	URL     string    `json:"url"`
	Format  string    `json:"format,omitempty"`
	Started time.Time `json:"started"`
	// Written is the bytes fetched so far; Total is the upstream's advertised
	// size (0 when unknown, e.g. chunked responses) — so progress is
	// Written/Total when Total>0, else just a running byte count.
	Written int64 `json:"written"`
	Total   int64 `json:"total"`
	// Tee is true when the bytes are ALSO streamed to a client (an on-demand
	// pull-through), false for a background fetch.
	Tee bool `json:"tee"`
	// ElapsedSec / RateBps are derived, for a UI progress bar.
	ElapsedSec float64 `json:"elapsed_sec"`
	RateBps    int64   `json:"rate_bps"`
}

// Downloads is a process-wide registry of in-flight upstream downloads, so the
// admin API can report what is being fetched and how far along it is.
type Downloads struct {
	mu  sync.Mutex
	seq int64
	m   map[int64]*dlEntry
}

type dlEntry struct {
	id      int64
	url     string
	format  string
	tee     bool
	start   time.Time
	written int64 // atomic
	total   int64
}

var activeDownloads = &Downloads{m: make(map[int64]*dlEntry)}

// ActiveDownloads returns the process-wide in-flight download registry.
func ActiveDownloads() *Downloads { return activeDownloads }

// begin registers a download and returns its entry (to be passed to end).
func (d *Downloads) begin(url, format string, tee bool, total int64) *dlEntry {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seq++
	e := &dlEntry{id: d.seq, url: url, format: format, tee: tee, start: time.Now(), total: total}
	d.m[e.id] = e
	return e
}

// end removes a download from the registry (deferred by the fetcher).
func (d *Downloads) end(e *dlEntry) {
	if e == nil {
		return
	}
	d.mu.Lock()
	delete(d.m, e.id)
	d.mu.Unlock()
}

// List returns a snapshot of the active downloads (sorted by id).
func (d *Downloads) List() []ActiveDownload {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	out := make([]ActiveDownload, 0, len(d.m))
	for _, e := range d.m {
		w := atomic.LoadInt64(&e.written)
		el := now.Sub(e.start).Seconds()
		var rate int64
		if el > 0 {
			rate = int64(float64(w) / el)
		}
		out = append(out, ActiveDownload{
			ID: e.id, URL: e.url, Format: e.format, Started: e.start,
			Written: w, Total: e.total, Tee: e.tee, ElapsedSec: el, RateBps: rate,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// countingReader wraps an upstream body and tallies the bytes read, so the
// active-download registry reflects live progress.
type countingReader struct {
	r io.Reader
	e *dlEntry
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		atomic.AddInt64(&c.e.written, int64(n))
	}
	return n, err
}

// TrackDownload registers an in-flight download for an arbitrary upstream body
// (used by the OCI blob path, which does not go through fetchAbsolute) and
// wraps it so the bytes read are counted. Call done() when the fetch finishes
// or fails.
func TrackDownload(url string, total int64, tee bool, body io.ReadCloser) (io.ReadCloser, func()) {
	e := activeDownloads.begin(url, "", tee, total)
	return &countReadCloser{r: body, e: e}, func() { activeDownloads.end(e) }
}

type countReadCloser struct {
	r io.ReadCloser
	e *dlEntry
}

func (c *countReadCloser) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		atomic.AddInt64(&c.e.written, int64(n))
	}
	return n, err
}

func (c *countReadCloser) Close() error { return c.r.Close() }
