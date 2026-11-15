package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"log/slog"
	"net/http"
	"net/url"
	"runtime/debug"
	"sort"
	"strings"
	"time"

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

// Timeout puts a deadline on the request context. Handlers and stores must honour ctx;
// a context.DeadlineExceeded error is mapped to 504 upstream_timeout by writeError.
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
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

// Compress gzips JSON/text responses (level 5).
func Compress() func(http.Handler) http.Handler {
	return middleware.Compress(5, "application/json", "text/plain", "application/yaml")
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
