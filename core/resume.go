package easyblobkit

import (
	"context"
	"io"
	"net/http"
	"strconv"
)

// maxResumeAttempts bounds how many times a dropped upstream transfer is
// resumed before the fetch fails.
const maxResumeAttempts = 8

// resumableBody is an io.ReadCloser over an absolute URL that transparently
// resumes an interrupted transfer with a Range request. A CDN that drops a
// multi-GB download mid-stream (the reported ~7.6 GB cut) is retried from the
// last byte received, so the whole object still lands in the CAS — and a client
// teed off this reader sees a continuous stream instead of a truncated one.
type resumableBody struct {
	ctx     context.Context
	client  *http.Client
	url     string
	headers map[string]string

	total    int64 // expected size (0 = unknown → a short read cannot be resumed)
	offset   int64 // bytes read so far
	resp     *http.Response
	attempts int
	done     bool
}

// openResumable starts a resumable GET. It issues the first request eagerly so
// a non-2xx status surfaces immediately.
func openResumable(ctx context.Context, client *http.Client, url string, headers map[string]string) (*resumableBody, error) {
	r := &resumableBody{ctx: ctx, client: client, url: url, headers: headers}
	if err := r.open(0); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *resumableBody) open(offset int64) error {
	req, err := http.NewRequestWithContext(r.ctx, http.MethodGet, r.url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", UserAgent)
	for k, v := range r.headers {
		req.Header.Set(k, v)
	}
	if offset > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(offset, 10)+"-")
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_ = resp.Body.Close()
		return &UpstreamStatusError{Path: r.url, Status: resp.StatusCode}
	}
	if offset == 0 && resp.ContentLength > 0 {
		r.total = resp.ContentLength
	}
	if offset > 0 && resp.StatusCode == http.StatusOK {
		// The server ignored our Range and restarted the body: rewind.
		r.offset = 0
	}
	r.resp = resp
	return nil
}

// Read reads from the current response, resuming with a Range request when the
// upstream drops before the expected total.
func (r *resumableBody) Read(p []byte) (int, error) {
	for {
		if r.done {
			return 0, io.EOF
		}
		if r.resp == nil {
			if r.attempts >= maxResumeAttempts {
				return 0, io.ErrUnexpectedEOF
			}
			r.attempts++
			if err := r.open(r.offset); err != nil {
				return 0, err
			}
		}
		n, err := r.resp.Body.Read(p)
		if n > 0 {
			r.offset += int64(n)
			if err == nil {
				return n, nil
			}
			// The body ended or dropped after delivering bytes: close it and
			// decide on the NEXT Read (deliver these bytes now either way).
			_ = r.resp.Body.Close()
			r.resp = nil
			// A clean EOF, or reaching the advertised total, is completion; a
			// drop before the total is a resume.
			if err == io.EOF || (r.total > 0 && r.offset >= r.total) {
				r.done = true
			}
			return n, nil
		}
		if err == nil {
			continue
		}
		// n == 0 with an error: a clean end (EOF) or a drop to resume.
		_ = r.resp.Body.Close()
		r.resp = nil
		if err == io.EOF || r.total == 0 || r.offset >= r.total {
			r.done = true
			return 0, io.EOF
		}
		// else: drop before the total → loop resumes from r.offset.
	}
}

func (r *resumableBody) Close() error {
	if r.resp != nil {
		return r.resp.Body.Close()
	}
	return nil
}

// ContentLength is the upstream's advertised size (0 when unknown), for the
// active-downloads view.
func (r *resumableBody) ContentLength() int64 { return r.total }
