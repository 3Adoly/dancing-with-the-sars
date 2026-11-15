package fetch

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

// newFetcher returns a Fetcher caching under a temp dir with instant retries.
// slept collects the wait the retry loop asked for, without waiting.
func newFetcher(t *testing.T, retries int) (f *Fetcher, slept *[]time.Duration, logs *bytes.Buffer) {
	t.Helper()
	logs = &bytes.Buffer{}
	slept = new([]time.Duration)
	f = &Fetcher{
		HC:         http.DefaultClient,
		RawDir:     t.TempDir(),
		Log:        slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		MaxRetries: retries,
		Backoff:    time.Second,
		sleep: func(_ context.Context, d time.Duration) error {
			*slept = append(*slept, d)
			return nil
		},
	}
	return f, slept, logs
}

func readAll(t *testing.T, rc io.ReadCloser, err error) string {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func cachePath(f *Fetcher, source, url string) string {
	sum := sha1.Sum([]byte(url))
	return filepath.Join(f.RawDir, source, hex.EncodeToString(sum[:])+".gz")
}

func TestGetCachesToGzipAndHitsNetworkOnce(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		io.WriteString(w, "hello, quake world")
	}))
	defer srv.Close()
	f, _, logs := newFetcher(t, 2)
	ctx := context.Background()

	rc, err := f.Get(ctx, "usgs", srv.URL+"/q")
	if got := readAll(t, rc, err); got != "hello, quake world" {
		t.Fatalf("first body = %q", got)
	}
	rc, err = f.Get(ctx, "usgs", srv.URL+"/q")
	if got := readAll(t, rc, err); got != "hello, quake world" {
		t.Fatalf("cached body = %q", got)
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("server hit %d times, want 1 (second Get must come from the cache)", n)
	}

	// The cache file is gzip at RawDir/<source>/<sha1(url)>.gz, with no temp leftovers.
	path := cachePath(f, "usgs", srv.URL+"/q")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cache file missing: %v", err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("cache file is not gzip: %v", err)
	}
	if b, _ := io.ReadAll(zr); string(b) != "hello, quake world" {
		t.Errorf("gunzipped cache = %q", b)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("cache dir has %d entries, want just the cache file: %v", len(entries), entries)
	}
	if !strings.Contains(logs.String(), "fetch downloaded") || !strings.Contains(logs.String(), "fetch cache hit") {
		t.Errorf("logs should show one download and one cache hit:\n%s", logs)
	}
}

func TestGetSeparatesSourcesAndURLs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, r.URL.Path)
	}))
	defer srv.Close()
	f, _, _ := newFetcher(t, 0)
	ctx := context.Background()
	for _, tc := range []struct{ source, path string }{{"a", "/1"}, {"a", "/2"}, {"b", "/1"}} {
		rc, err := f.Get(ctx, tc.source, srv.URL+tc.path)
		if got := readAll(t, rc, err); got != tc.path {
			t.Errorf("%s %s: body %q", tc.source, tc.path, got)
		}
	}
}

func TestRefreshBypassesCache(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "v"+string(rune('0'+hits.Add(1))))
	}))
	defer srv.Close()
	f, _, _ := newFetcher(t, 0)
	ctx := context.Background()
	rc, err := f.Get(ctx, "s", srv.URL)
	if got := readAll(t, rc, err); got != "v1" {
		t.Fatalf("first = %q", got)
	}
	f.Refresh = true
	rc, err = f.Get(ctx, "s", srv.URL)
	if got := readAll(t, rc, err); got != "v2" {
		t.Fatalf("refresh = %q, want v2", got)
	}
	f.Refresh = false
	rc, err = f.Get(ctx, "s", srv.URL)
	if got := readAll(t, rc, err); got != "v2" {
		t.Errorf("after refresh the cache must hold the new body, got %q", got)
	}
}

func TestRetriesOn503HonoringRetryAfter(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch hits.Add(1) {
		case 1:
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(http.StatusServiceUnavailable)
		case 2:
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
		default:
			io.WriteString(w, "finally")
		}
	}))
	defer srv.Close()
	f, slept, logs := newFetcher(t, 4)

	rc, err := f.Get(context.Background(), "usgs", srv.URL)
	if got := readAll(t, rc, err); got != "finally" {
		t.Fatalf("body = %q", got)
	}
	if n := hits.Load(); n != 3 {
		t.Errorf("server hit %d times, want 3", n)
	}
	if len(*slept) != 2 || (*slept)[0] != 3*time.Second || (*slept)[1] != 0 {
		t.Errorf("waits = %v, want [3s 0s] (Retry-After honored, including 0)", *slept)
	}
	if !strings.Contains(logs.String(), "retrying") {
		t.Errorf("retries must be logged:\n%s", logs)
	}
	if _, err := os.Stat(cachePath(f, "usgs", srv.URL)); err != nil {
		t.Errorf("the final 200 must be cached: %v", err)
	}
}

func TestBackoffIsExponentialWithJitterAndCapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	f, slept, _ := newFetcher(t, 4)
	f.MaxWait = 5 * time.Second // caps the 8s step

	_, err := f.Get(context.Background(), "s", srv.URL)
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusBadGateway {
		t.Fatalf("err = %v, want a 502 StatusError", err)
	}
	if !strings.Contains(err.Error(), "5 attempts") {
		t.Errorf("error should say how many attempts were made: %v", err)
	}
	if len(*slept) != 4 {
		t.Fatalf("waits = %v, want 4 (MaxRetries)", *slept)
	}
	for i, base := range []time.Duration{1, 2, 4, 8} {
		base *= time.Second
		lo, hi := base, base+base/2
		if base > f.MaxWait {
			lo, hi = f.MaxWait, f.MaxWait
		}
		if d := (*slept)[i]; d < lo || d > hi {
			t.Errorf("wait %d = %v, want within [%v, %v]", i, d, lo, hi)
		}
	}
	if _, err := os.Stat(cachePath(f, "s", srv.URL)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a failed fetch must not be cached (stat err = %v)", err)
	}
}

func TestNon2xxIsNotRetriedOrCached(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()
	f, slept, _ := newFetcher(t, 4)

	_, err := f.Get(context.Background(), "s", srv.URL)
	var se *StatusError
	if !errors.As(err, &se) || se.Code != 404 {
		t.Fatalf("err = %v, want a 404 StatusError", err)
	}
	if hits.Load() != 1 || len(*slept) != 0 {
		t.Errorf("404 must not be retried: hits=%d waits=%v", hits.Load(), *slept)
	}
	if _, err := os.Stat(cachePath(f, "s", srv.URL)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("404 body must not be cached (stat err = %v)", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(f.RawDir, "s")); len(entries) != 0 {
		t.Errorf("cache dir should be empty, has %v", entries)
	}
}

// A connection cut mid-body is retried, and the partial body is never installed.
func TestTruncatedBodyIsRetriedAndLeavesNoPartialFile(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("Content-Length", "100")
			io.WriteString(w, "only a prefix") // handler returns early: the client sees an unexpected EOF
			return
		}
		io.WriteString(w, "complete body")
	}))
	defer srv.Close()
	f, slept, _ := newFetcher(t, 2)

	rc, err := f.Get(context.Background(), "s", srv.URL)
	if got := readAll(t, rc, err); got != "complete body" {
		t.Fatalf("body = %q", got)
	}
	if hits.Load() != 2 || len(*slept) != 1 {
		t.Errorf("hits=%d waits=%v, want one retry", hits.Load(), *slept)
	}
	entries, _ := os.ReadDir(filepath.Join(f.RawDir, "s"))
	if len(entries) != 1 || strings.HasPrefix(entries[0].Name(), ".tmp") {
		t.Errorf("only the final cache file may remain: %v", entries)
	}
}

func TestNetworkErrorIsRetriedThenReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing listens any more
	f, slept, _ := newFetcher(t, 2)

	_, err := f.Get(context.Background(), "s", url)
	if err == nil {
		t.Fatal("want a connection error")
	}
	if len(*slept) != 2 {
		t.Errorf("waits = %v, want 2 retries", *slept)
	}
}

func TestLimiterGatesNetworkNotCacheHits(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "x")
	}))
	defer srv.Close()
	f, _, _ := newFetcher(t, 0)
	f.Limiter = rate.NewLimiter(rate.Every(40*time.Millisecond), 1)
	ctx := context.Background()

	start := time.Now()
	for _, p := range []string{"/1", "/2", "/3"} {
		rc, err := f.Get(ctx, "s", srv.URL+p)
		readAll(t, rc, err)
	}
	if d := time.Since(start); d < 70*time.Millisecond {
		t.Errorf("3 downloads took %v, the limiter should space them by 40ms", d)
	}
	start = time.Now()
	for range 5 {
		rc, err := f.Get(ctx, "s", srv.URL+"/1") // cached
		readAll(t, rc, err)
	}
	if d := time.Since(start); d > 30*time.Millisecond {
		t.Errorf("cache hits took %v, they must not wait on the limiter", d)
	}
}

func TestContextCancelStopsRetrying(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	f, _, _ := newFetcher(t, 10)
	ctx, cancel := context.WithCancel(context.Background())
	f.sleep = func(ctx context.Context, d time.Duration) error {
		cancel()
		return ctx.Err()
	}
	_, err := f.Get(ctx, "s", srv.URL)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestSecretsAreRedactedFromErrorsAndLogs(t *testing.T) {
	const key = "SUPERSECRETKEY123"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	f, _, logs := newFetcher(t, 1)
	f.Secrets = []string{key}

	_, err := f.Get(context.Background(), "firms", srv.URL+"/api/area/csv/"+key+"/MODIS_SP")
	if err == nil {
		t.Fatal("want an error")
	}
	srv.Close()
	_, err2 := f.Get(context.Background(), "firms", srv.URL+"/api/area/csv/"+key+"/MODIS_SP/2")
	for _, text := range []string{err.Error(), err2.Error(), logs.String()} {
		if strings.Contains(text, key) {
			t.Errorf("secret leaked: %s", text)
		}
	}
	if !strings.Contains(err.Error(), "***") {
		t.Errorf("redacted URL expected in %q", err)
	}
}

func TestNonGzipCacheFileIsReplaced(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		io.WriteString(w, "fresh")
	}))
	defer srv.Close()
	f, _, logs := newFetcher(t, 0)
	path := cachePath(f, "s", srv.URL)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not gzip at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, err := f.Get(context.Background(), "s", srv.URL)
	if got := readAll(t, rc, err); got != "fresh" || hits.Load() != 1 {
		t.Errorf("body=%q hits=%d, want a re-download over the corrupt file", got, hits.Load())
	}
	if !strings.Contains(logs.String(), "unreadable") {
		t.Errorf("corrupt cache entry should be logged:\n%s", logs)
	}
}

func TestGetJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad" {
			io.WriteString(w, "{not json")
			return
		}
		io.WriteString(w, `{"count": 3, "name": "usgs"}`)
	}))
	defer srv.Close()
	f, _, _ := newFetcher(t, 0)
	var v struct {
		Count int    `json:"count"`
		Name  string `json:"name"`
	}
	if err := f.GetJSON(context.Background(), "s", srv.URL+"/ok", &v); err != nil || v.Count != 3 || v.Name != "usgs" {
		t.Errorf("GetJSON = %+v, %v", v, err)
	}
	if err := f.GetJSON(context.Background(), "s", srv.URL+"/bad", &v); err == nil {
		t.Error("invalid JSON must be an error")
	}
}

func TestInvalidSourceNames(t *testing.T) {
	f, _, _ := newFetcher(t, 0)
	for _, s := range []string{"", ".", "..", "a/b", "../x", `a\b`} {
		if _, err := f.Get(context.Background(), s, "http://127.0.0.1:1/"); err == nil || !strings.Contains(err.Error(), "invalid source") {
			// `a\b` is a plain file name on Linux; only the separator cases are rejected there.
			if s == `a\b` && filepath.Separator == '/' {
				continue
			}
			t.Errorf("source %q: err = %v, want invalid source name", s, err)
		}
	}
}

func TestRetryAfterParsing(t *testing.T) {
	future := time.Now().Add(90 * time.Second).UTC().Format(http.TimeFormat)
	tests := []struct {
		in       string
		min, max time.Duration
	}{
		{"", -1, -1},
		{"7", 7 * time.Second, 7 * time.Second},
		{"0", 0, 0},
		{"-5", 0, 0},
		{"garbage", -1, -1},
		{future, 80 * time.Second, 90 * time.Second},
		{"Mon, 02 Jan 2006 15:04:05 GMT", 0, 0}, // a past date means "now"
	}
	for _, tc := range tests {
		if got := retryAfter(tc.in); got < tc.min || got > tc.max {
			t.Errorf("retryAfter(%q) = %v, want within [%v, %v]", tc.in, got, tc.min, tc.max)
		}
	}
}
