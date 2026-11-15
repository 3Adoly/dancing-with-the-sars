// Package fetch downloads source files with a gzip disk cache, retries and an
// optional rate limiter. It imports only internal/platform (the default HTTP
// client), never the ingest pipeline.
package fetch

import (
	"compress/gzip"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/dwts/backend/internal/platform"
)

// Defaults used by New and for zero values of the tunable fields.
const (
	DefaultMaxRetries = 4                // retries after the first attempt: waits of 1s, 2s, 4s, 8s
	DefaultBackoff    = time.Second      // base of the exponential backoff
	DefaultMaxWait    = 60 * time.Second // cap on any single wait, Retry-After included
	cacheExt          = ".gz"
)

// Fetcher downloads URLs for a source and caches every successful body,
// gzip-compressed, at RawDir/<source>/<sha1(url)>.gz (ruling R-B.1). A cache
// hit makes no network request. Cached bodies are decompressed on read.
//
// The zero value is not ready; use New or set at least RawDir. A Fetcher is
// safe for concurrent use as long as its fields are not changed meanwhile.
type Fetcher struct {
	HC         *http.Client  // nil: platform.NewHTTPClient(5 minutes); its Timeout covers the whole body, so size it for the biggest file
	RawDir     string        // cache root; required
	Log        *slog.Logger  // nil: slog.Default()
	MaxRetries int           // retries after the first attempt on 429/5xx/network errors; 0 = none
	Refresh    bool          // ignore cached bodies and download again (the cache is overwritten)
	Limiter    *rate.Limiter // optional; waited on before every network attempt, never on a cache hit

	Backoff time.Duration // base wait before retry n is Backoff*2^n plus up to 50% jitter; 0 = DefaultBackoff
	MaxWait time.Duration // cap on a single wait, also applied to Retry-After; 0 = DefaultMaxWait
	// Secrets are substrings (an API key inside a URL) replaced by "***" in
	// every log line and error message this Fetcher produces.
	Secrets []string

	sleep func(ctx context.Context, d time.Duration) error // test hook; nil = real sleep
}

// New returns a Fetcher with the default retry policy.
func New(hc *http.Client, rawDir string, log *slog.Logger) *Fetcher {
	return &Fetcher{HC: hc, RawDir: rawDir, Log: log, MaxRetries: DefaultMaxRetries}
}

// StatusError is a non-2xx response. It is never cached.
type StatusError struct {
	URL  string // redacted
	Code int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("fetch %s: unexpected status %d %s", e.URL, e.Code, http.StatusText(e.Code))
}

var defaultHC = sync.OnceValue(func() *http.Client { return platform.NewHTTPClient(5 * time.Minute) })

// Get returns the body of url, from the disk cache when present, otherwise
// downloaded (with retries) and cached first. The caller must Close it.
// source names the cache subdirectory and must be a plain name.
func (f *Fetcher) Get(ctx context.Context, source, url string) (io.ReadCloser, error) {
	if source == "" || source != filepath.Base(source) || source == "." || source == ".." {
		return nil, fmt.Errorf("fetch: invalid source name %q", source)
	}
	if f.RawDir == "" {
		return nil, errors.New("fetch: RawDir is not set")
	}
	sum := sha1.Sum([]byte(url))
	dir := filepath.Join(f.RawDir, source)
	path := filepath.Join(dir, hex.EncodeToString(sum[:])+cacheExt)
	log := f.log().With("source", source, "url", f.redact(url))

	if !f.Refresh {
		rc, err := openCached(path)
		switch {
		case err == nil:
			log.Debug("fetch cache hit", "file", path)
			return rc, nil
		case errors.Is(err, os.ErrNotExist):
		default:
			log.Warn("fetch cache entry unreadable, downloading again", "file", path, "err", err)
		}
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("fetch: create cache dir: %w", err)
	}
	if err := f.download(ctx, url, dir, path, log); err != nil {
		return nil, err
	}
	rc, err := openCached(path)
	if err != nil {
		return nil, fmt.Errorf("fetch: reopen cached body: %w", err)
	}
	return rc, nil
}

// GetJSON fetches url (through the cache) and decodes the JSON body into v.
func (f *Fetcher) GetJSON(ctx context.Context, source, url string, v any) error {
	rc, err := f.Get(ctx, source, url)
	if err != nil {
		return err
	}
	defer rc.Close()
	if err := json.NewDecoder(rc).Decode(v); err != nil {
		return fmt.Errorf("fetch %s: decode json: %w", f.redact(url), err)
	}
	return nil
}

// download fetches url with retries and atomically installs the gzip-compressed
// body at path: written to a temp file in dir, synced, then renamed.
func (f *Fetcher) download(ctx context.Context, url, dir, path string, log *slog.Logger) error {
	var lastErr error
	for attempt := 0; ; attempt++ {
		start := time.Now()
		n, wait, err := f.attempt(ctx, url, dir, path)
		if err == nil {
			log.Info("fetch downloaded", "bytes", n, "attempt", attempt+1, "dur", time.Since(start).Round(time.Millisecond))
			return nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !retryable(err) || attempt >= f.MaxRetries {
			if attempt > 0 {
				return fmt.Errorf("%w (after %d attempts)", lastErr, attempt+1)
			}
			return lastErr
		}
		if wait < 0 {
			wait = f.backoff(attempt)
		}
		wait = min(wait, f.maxWait())
		log.Warn("fetch failed, retrying", "attempt", attempt+1, "wait", wait, "err", err)
		if err := f.doSleep(ctx, wait); err != nil {
			return err
		}
	}
}

// attempt performs one request. On a 2xx it returns the number of compressed
// bytes cached. On failure wait carries the server's Retry-After, or -1 when
// the response had none.
func (f *Fetcher) attempt(ctx context.Context, rawURL, dir, path string) (n int64, wait time.Duration, err error) {
	if f.Limiter != nil {
		if err := f.Limiter.Wait(ctx); err != nil {
			return 0, -1, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, -1, fmt.Errorf("fetch: bad url: %s", f.redact(err.Error()))
	}
	resp, err := f.client().Do(req)
	if err != nil {
		return 0, -1, f.transportErr(rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10)) // let the connection be reused
		return 0, retryAfter(resp.Header.Get("Retry-After")), &StatusError{URL: f.redact(rawURL), Code: resp.StatusCode}
	}
	n, err = writeCache(dir, path, resp.Body)
	if err != nil {
		return 0, -1, f.transportErr(rawURL, err)
	}
	return n, -1, nil
}

// writeCache gzips r into a temp file next to path and renames it into place,
// so a reader never sees a partial file and a failed download leaves nothing.
func writeCache(dir, path string, r io.Reader) (n int64, err error) {
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return 0, fmt.Errorf("fetch: create temp file: %w", err)
	}
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	zw := gzip.NewWriter(tmp)
	if _, err = io.Copy(zw, r); err != nil {
		return 0, fmt.Errorf("fetch: read body: %w", err)
	}
	if err = zw.Close(); err != nil {
		return 0, fmt.Errorf("fetch: compress body: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		return 0, fmt.Errorf("fetch: sync cache file: %w", err)
	}
	st, err := tmp.Stat()
	if err != nil {
		return 0, fmt.Errorf("fetch: stat cache file: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return 0, fmt.Errorf("fetch: close cache file: %w", err)
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return 0, fmt.Errorf("fetch: install cache file: %w", err)
	}
	return st.Size(), nil
}

// retryable reports whether err is worth another attempt: 429 and 5xx
// responses, and network or body-read failures. Other statuses (404, 403 ...)
// and local errors (disk, bad URL) are final.
func retryable(err error) bool {
	var se *StatusError
	if errors.As(err, &se) {
		return se.Code == http.StatusTooManyRequests || se.Code >= 500
	}
	var ne *netErr
	return errors.As(err, &ne)
}

// netErr marks a transport or body-transfer failure (retryable).
type netErr struct {
	err error
	msg string // already redacted
}

func (e *netErr) Error() string { return e.msg }
func (e *netErr) Unwrap() error { return e.err }

// transportErr classifies a failure of the request or of copying the body.
// Disk errors are final; everything else (connection reset, timeout, short
// body) becomes a retryable netErr whose text carries the redacted URL
// instead of the raw one and stays matchable with errors.Is/As.
func (f *Fetcher) transportErr(rawURL string, err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	var local *os.PathError
	if errors.As(err, &local) {
		return err
	}
	return &netErr{err: err, msg: fmt.Sprintf("fetch %s: %s", f.redact(rawURL), f.redact(err.Error()))}
}

func (f *Fetcher) redact(s string) string {
	for _, sec := range f.Secrets {
		if sec != "" {
			s = strings.ReplaceAll(s, sec, "***")
		}
	}
	return s
}

// backoff is Backoff*2^attempt plus up to 50% jitter.
func (f *Fetcher) backoff(attempt int) time.Duration {
	base := f.Backoff
	if base <= 0 {
		base = DefaultBackoff
	}
	d := base << min(attempt, 16)
	return d + rand.N(d/2+1)
}

func (f *Fetcher) maxWait() time.Duration {
	if f.MaxWait > 0 {
		return f.MaxWait
	}
	return DefaultMaxWait
}

func (f *Fetcher) doSleep(ctx context.Context, d time.Duration) error {
	if f.sleep != nil {
		return f.sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (f *Fetcher) client() *http.Client {
	if f.HC != nil {
		return f.HC
	}
	return defaultHC()
}

func (f *Fetcher) log() *slog.Logger {
	if f.Log != nil {
		return f.Log
	}
	return slog.Default()
}

// retryAfter parses a Retry-After header: delta-seconds or an HTTP date.
// It returns -1 when the header is absent or unusable.
func retryAfter(h string) time.Duration {
	h = strings.TrimSpace(h)
	if h == "" {
		return -1
	}
	if secs, err := strconv.Atoi(h); err == nil {
		return time.Duration(max(secs, 0)) * time.Second
	}
	if t, err := http.ParseTime(h); err == nil {
		return max(time.Until(t), 0)
	}
	return -1
}

// openCached opens path as a gzip stream. A missing file reports os.ErrNotExist;
// a file that is not gzip reports an error so the caller downloads again.
func openCached(path string) (io.ReadCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	zr, err := gzip.NewReader(f)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("cache file %s is not valid gzip: %w", path, err)
	}
	return &cachedBody{zr: zr, f: f}, nil
}

type cachedBody struct {
	zr *gzip.Reader
	f  *os.File
}

func (c *cachedBody) Read(p []byte) (int, error) { return c.zr.Read(p) }
func (c *cachedBody) Close() error {
	zerr := c.zr.Close()
	if err := c.f.Close(); err != nil {
		return err
	}
	return zerr
}
