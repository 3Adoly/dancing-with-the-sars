package httpapi

import (
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"net/http"
	"net/url"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
)

type ctxKey int

const (
	ctxKeyRequestID ctxKey = iota
	ctxKeyLogger
)

// HeaderRequestID is echoed on every response.
const HeaderRequestID = "X-Request-ID"

// RequestIDFrom returns the request ID stored by RequestID (or "").
func RequestIDFrom(ctx context.Context) string {
	s, _ := ctx.Value(ctxKeyRequestID).(string)
	return s
}

// RequestID reuses a safe incoming X-Request-ID (≤64 chars of [A-Za-z0-9._-]) or generates one,
// stores it in the context and echoes it in the response header.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(HeaderRequestID)
		if !validRequestID(id) {
			id = newRequestID()
		}
		w.Header().Set(HeaderRequestID, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKeyRequestID, id)))
	})
}

func validRequestID(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

func newRequestID() string {
	var b [8]byte
	_, _ = rand.Read(b[:]) // crypto/rand never fails on supported platforms
	return hex.EncodeToString(b[:])
}

// Logger stores a request-scoped slog logger in the context and logs one line per request.
func Logger(base *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			l := base.With("reqId", RequestIDFrom(r.Context()))
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r.WithContext(context.WithValue(r.Context(), ctxKeyLogger, l)))
			status := ww.Status()
			if status == 0 {
				status = http.StatusOK
			}
			lvl := slog.LevelInfo
			if status >= 500 {
				lvl = slog.LevelError
			}
			l.Log(r.Context(), lvl, "http", "method", r.Method, "path", r.URL.Path, "status", status,
				"bytes", ww.BytesWritten(), "durMs", float64(time.Since(start).Microseconds())/1000)
		})
	}
}

// Recover turns a panic into a 500 with the standard error JSON and logs the stack.
// http.ErrAbortHandler is re-panicked (net/http uses it to abort a response on purpose).
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			if rec == http.ErrAbortHandler {
				panic(rec)
			}
			loggerFrom(r.Context()).Error("panic", "panic", fmt.Sprint(rec), "stack", string(debug.Stack()))
			writeError(w, r, &APIError{Status: http.StatusInternalServerError, Code: CodeInternal, Message: "internal error"})
		}()
		next.ServeHTTP(w, r)
	})
}

// Timeout puts a deadline on the request context. Handlers and stores must honour ctx; a
// context.DeadlineExceeded error is mapped to 504 upstream_timeout by writeError. If a handler
// returns after the deadline without writing anything, the middleware writes that 504 itself.
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r.WithContext(ctx))
			if errors.Is(ctx.Err(), context.DeadlineExceeded) && ww.Status() == 0 && ww.BytesWritten() == 0 {
				writeError(w, r.WithContext(context.WithoutCancel(ctx)), context.DeadlineExceeded)
			}
		})
	}
}

// HeadNoBody drops the body of HEAD responses (headers, status and cache headers are kept).
// net/http already discards it on the wire; this also saves the encoding work and keeps
// in-process callers (tests, recorders) honest.
func HeadNoBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w = headWriter{w}
		}
		next.ServeHTTP(w, r)
	})
}

type headWriter struct{ http.ResponseWriter }

func (h headWriter) Write(p []byte) (int, error) { return len(p), nil }

// MaxQueryBytes caps the raw query string; longer queries get 400 invalid_param.
const MaxQueryBytes = 4096

// QueryGuard rejects oversized query strings and query strings that are not valid UTF-8
// with 400 invalid_param, before any handler parses them.
func QueryGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := r.URL.RawQuery
		if len(raw) > MaxQueryBytes {
			writeError(w, r, &APIError{Status: http.StatusBadRequest, Code: CodeInvalidParam,
				Message: fmt.Sprintf("query string too long (max %d bytes)", MaxQueryBytes)})
			return
		}
		if raw != "" {
			if dec, err := url.QueryUnescape(raw); err != nil || !utf8.ValidString(dec) {
				writeError(w, r, &APIError{Status: http.StatusBadRequest, Code: CodeInvalidParam,
					Message: "query string is not valid percent-encoded UTF-8"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// CORS allows the configured origins (GET/POST/OPTIONS) and exposes ETag + X-Request-ID.
func CORS(origins []string) func(http.Handler) http.Handler {
	return cors.Handler(cors.Options{
		AllowedOrigins:   origins,
		AllowedMethods:   []string{http.MethodGet, http.MethodPost, http.MethodOptions},
		AllowedHeaders:   []string{"Accept", "Content-Type", "If-None-Match", HeaderRequestID},
		ExposedHeaders:   []string{"ETag", HeaderRequestID},
		AllowCredentials: false,
		MaxAge:           300,
	})
}

// GzipMinBytes is the smallest response body that gets gzipped (Plan_Detailed §8.1: "gzip when >1 KB").
const GzipMinBytes = 1024

var gzipTypes = []string{"application/json", "text/plain", "application/yaml", "text/csv"}

// Gzip compresses JSON/text responses of at least minBytes when the client sends
// Accept-Encoding: gzip. Images, partial (206) responses, 304s and small bodies pass through.
// It always adds "Vary: Accept-Encoding" so caches keep both variants apart.
func Gzip(minBytes int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Add("Vary", "Accept-Encoding")
			if !acceptsGzip(r) || r.Method == http.MethodHead {
				next.ServeHTTP(w, r)
				return
			}
			gw := &gzipWriter{rw: w, min: minBytes}
			defer func() {
				if p := recover(); p != nil {
					panic(p) // let Recover answer; never flush a half-built response
				}
				gw.finish()
			}()
			next.ServeHTTP(gw, r)
		})
	}
}

func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		enc, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(enc), "gzip") {
			continue
		}
		for _, p := range strings.Split(params, ";") {
			if k, v, ok := strings.Cut(strings.TrimSpace(p), "="); ok && strings.EqualFold(k, "q") {
				q, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
				return err == nil && q > 0
			}
		}
		return true
	}
	return false
}

// gzipWriter buffers the first minBytes, then decides once whether to compress.
type gzipWriter struct {
	rw      http.ResponseWriter
	min     int
	status  int
	buf     []byte
	decided bool
	gz      *gzip.Writer
}

func (g *gzipWriter) Header() http.Header { return g.rw.Header() }

func (g *gzipWriter) WriteHeader(code int) {
	if g.status == 0 {
		g.status = code
	}
	// Bodyless statuses cannot be compressed: decide immediately.
	if code == http.StatusNotModified || code == http.StatusNoContent || code < 200 {
		g.decide(false)
	}
}

func (g *gzipWriter) Write(p []byte) (int, error) {
	if g.status == 0 {
		g.status = http.StatusOK
	}
	if g.decided {
		if g.gz != nil {
			return g.gz.Write(p)
		}
		return g.rw.Write(p)
	}
	g.buf = append(g.buf, p...)
	if len(g.buf) >= g.min {
		g.decide(true)
	}
	return len(p), nil
}

func (g *gzipWriter) decide(bigEnough bool) {
	if g.decided {
		return
	}
	g.decided = true
	if g.status == 0 {
		g.status = http.StatusOK
	}
	h := g.rw.Header()
	if bigEnough && compressible(h) && g.status != http.StatusPartialContent {
		h.Del("Content-Length")
		h.Set("Content-Encoding", "gzip")
		g.rw.WriteHeader(g.status)
		g.gz, _ = gzip.NewWriterLevel(g.rw, 5)
		_, _ = g.gz.Write(g.buf)
	} else {
		g.rw.WriteHeader(g.status)
		if len(g.buf) > 0 {
			_, _ = g.rw.Write(g.buf)
		}
	}
	g.buf = nil
}

func (g *gzipWriter) finish() {
	if !g.decided {
		if g.status == 0 && len(g.buf) == 0 {
			return // handler wrote nothing; let net/http send its default
		}
		g.decide(false)
	}
	if g.gz != nil {
		_ = g.gz.Close()
	}
}

func compressible(h http.Header) bool {
	if h.Get("Content-Encoding") != "" || h.Get("Content-Range") != "" {
		return false
	}
	ct := strings.ToLower(h.Get("Content-Type"))
	for _, t := range gzipTypes {
		if strings.HasPrefix(ct, t) {
			return true
		}
	}
	return false
}

// --- ETag helpers (Plan_Detailed §12) ---

// CanonicalQuery returns the query string with keys sorted and values sorted per key,
// so that ?b=2&a=1 and ?a=1&b=2 share one ETag.
func CanonicalQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		vs := append([]string(nil), q[k]...)
		sort.Strings(vs)
		for _, v := range vs {
			if sb.Len() > 0 {
				sb.WriteByte('&')
			}
			sb.WriteString(url.QueryEscape(k))
			sb.WriteByte('=')
			sb.WriteString(url.QueryEscape(v))
		}
	}
	return sb.String()
}

// MakeETag builds a weak ETag W/"v{version}-{fnv64(parts)}".
func MakeETag(version int64, parts ...string) string {
	h := fnv.New64a()
	for _, p := range parts {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}
	return fmt.Sprintf(`W/"v%d-%x"`, version, h.Sum64())
}

// CheckETag sets ETag + Cache-Control on w and, when the request's If-None-Match matches
// (weak comparison, list or "*"), writes 304 and returns true. Call it BEFORE computing the body.
func CheckETag(w http.ResponseWriter, r *http.Request, etag, cacheControl string) bool {
	w.Header().Set("ETag", etag)
	if cacheControl != "" {
		w.Header().Set("Cache-Control", cacheControl)
	}
	inm := r.Header.Get("If-None-Match")
	if inm == "" || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		return false
	}
	want := strings.TrimPrefix(etag, "W/")
	for _, cand := range strings.Split(inm, ",") {
		cand = strings.TrimSpace(cand)
		if cand == "*" || strings.TrimPrefix(cand, "W/") == want {
			w.WriteHeader(http.StatusNotModified)
			return true
		}
	}
	return false
}
