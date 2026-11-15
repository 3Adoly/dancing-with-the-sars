package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/dwts/backend/internal/domain"
)

func testRouter(db Pinger) http.Handler { return testRouterWith(db, nil) }

func testRouterWith(db Pinger, stats StatsAPI) http.Handler {
	return NewRouter(Deps{
		Stats:          stats,
		Log:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		DB:             db,
		CORSOrigins:    []string{"http://localhost:5173"},
		RequestTimeout: 2 * time.Second,
		StartedAt:      time.Now().Add(-5 * time.Second),
		Version:        "test",
	})
}

type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

func do(t *testing.T, h http.Handler, method, target string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) ErrorBody {
	t.Helper()
	var body ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("error body is not JSON: %v (%s)", err, rec.Body.String())
	}
	if body.Error.Code == "" || body.Error.Message == "" || body.RequestID == "" {
		t.Fatalf("incomplete error body: %+v", body)
	}
	return body
}

// --- /healthz ---

func TestHealthz(t *testing.T) {
	ready := &stubStats{ready: true, version: 7}
	cases := []struct {
		name       string
		db         Pinger
		stats      StatsAPI
		wantStatus string
		wantDB     string
		wantCube   int64
	}{
		{"no database", nil, nil, "degraded", "not_configured", 0},
		{"db down", fakePinger{err: errors.New("connection refused")}, ready, "degraded", "unavailable", 7},
		{"db ok, cube not loaded", fakePinger{}, &stubStats{}, "degraded", "ok", 0},
		{"db ok, cube loaded", fakePinger{}, ready, "ok", "ok", 7},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := do(t, testRouterWith(c.db, c.stats), http.MethodGet, "/healthz", nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d", rec.Code)
			}
			var h HealthResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &h); err != nil {
				t.Fatal(err)
			}
			if h.Status != c.wantStatus || h.DB != c.wantDB || h.CubeVersion != c.wantCube || h.UptimeSec < 4 || h.Version != "test" {
				t.Errorf("got %+v", h)
			}
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Error("healthz must be no-store")
			}
			if !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
				t.Error("content type")
			}
		})
	}
}

// --- error mapping ---

func TestToAPIError(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{fmt.Errorf("incident 9: %w", domain.ErrNotFound), 404, CodeNotFound},
		{fmt.Errorf("bad: %w", domain.ErrInvalid), 400, CodeInvalidParam},
		{fmt.Errorf("asf: %w", domain.ErrUpstream), 502, CodeUpstreamError},
		{fmt.Errorf("asf: %w", domain.ErrUpstreamTimeout), 504, CodeUpstreamTimeout},
		{context.DeadlineExceeded, 504, CodeUpstreamTimeout},
		{domain.ErrDisabled, 503, CodeChatDisabled},
		{domain.ErrNotReady, 503, CodeNotReady},
		{BadParam("from", "x"), 400, CodeInvalidParam},
		{errors.New("pq: syntax error at or near SELECT"), 500, CodeInternal},
	}
	for _, c := range cases {
		ae := toAPIError(c.err)
		if ae.Status != c.status || ae.Code != c.code {
			t.Errorf("%v → %d %s, want %d %s", c.err, ae.Status, ae.Code, c.status, c.code)
		}
		if c.code == CodeInternal && strings.Contains(ae.Message, "SELECT") {
			t.Error("internal error must not leak the cause")
		}
	}
}

func TestErrorShapeOnUnknownRouteAndMethod(t *testing.T) {
	h := testRouter(nil)
	rec := do(t, h, http.MethodGet, "/api/v1/nope", nil)
	if rec.Code != 404 || decodeError(t, rec).Error.Code != CodeNotFound {
		t.Errorf("404: %d %s", rec.Code, rec.Body)
	}
	rec = do(t, h, http.MethodDelete, "/healthz", nil)
	if rec.Code != 405 || decodeError(t, rec).Error.Code != CodeMethodNotAllowed {
		t.Errorf("405: %d %s", rec.Code, rec.Body)
	}
}

func TestNotImplementedAndChatDisabled(t *testing.T) {
	h := testRouter(nil)
	for _, p := range []string{"/api/v1/incidents", "/api/v1/stats/grid", "/api/v1/incidents/1",
		"/api/v1/stories", "/media/stories/x/a.png"} {
		rec := do(t, h, http.MethodGet, p, nil)
		if rec.Code != http.StatusNotImplemented || decodeError(t, rec).Error.Code != CodeNotImplemented {
			t.Errorf("%s: %d %s", p, rec.Code, rec.Body)
		}
	}
	rec := do(t, h, http.MethodPost, "/api/v1/chat", nil)
	if rec.Code != 503 || decodeError(t, rec).Error.Code != CodeChatDisabled {
		t.Errorf("chat: %d %s", rec.Code, rec.Body)
	}
}

// --- middleware ---

func TestRequestID(t *testing.T) {
	h := testRouter(nil)
	rec := do(t, h, http.MethodGet, "/healthz", map[string]string{HeaderRequestID: "abc-123"})
	if rec.Header().Get(HeaderRequestID) != "abc-123" {
		t.Errorf("incoming id not echoed: %q", rec.Header().Get(HeaderRequestID))
	}
	rec = do(t, h, http.MethodGet, "/healthz", map[string]string{HeaderRequestID: "bad id with spaces <script>"})
	if id := rec.Header().Get(HeaderRequestID); id == "" || strings.Contains(id, " ") {
		t.Errorf("unsafe id must be replaced, got %q", id)
	}
	rec = do(t, h, http.MethodGet, "/nope", nil)
	if body := decodeError(t, rec); body.RequestID != rec.Header().Get(HeaderRequestID) {
		t.Errorf("error requestId %q != header %q", body.RequestID, rec.Header().Get(HeaderRequestID))
	}
}

func TestRecover(t *testing.T) {
	h := RequestID(Recover(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })))
	rec := do(t, h, http.MethodGet, "/", nil)
	if rec.Code != 500 || decodeError(t, rec).Error.Code != CodeInternal {
		t.Errorf("panic → %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "boom") {
		t.Error("panic value must not leak to the client")
	}
}

func TestCORS(t *testing.T) {
	h := testRouter(nil)
	// Preflight from an allowed origin.
	rec := do(t, h, http.MethodOptions, "/api/v1/meta", map[string]string{
		"Origin": "http://localhost:5173", "Access-Control-Request-Method": "GET"})
	if rec.Code != http.StatusOK && rec.Code != http.StatusNoContent {
		t.Errorf("preflight status %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "http://localhost:5173" {
		t.Errorf("allow-origin = %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
	// Simple request from an allowed origin exposes ETag/X-Request-ID.
	rec = do(t, h, http.MethodGet, "/healthz", map[string]string{"Origin": "http://localhost:5173"})
	if exp := rec.Header().Get("Access-Control-Expose-Headers"); !strings.Contains(exp, "Etag") && !strings.Contains(exp, "ETag") {
		t.Errorf("expose headers = %q", exp)
	}
	// Disallowed origin gets no CORS header.
	rec = do(t, h, http.MethodGet, "/healthz", map[string]string{"Origin": "https://evil.example"})
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("disallowed origin must not be echoed")
	}
}

func TestGzip(t *testing.T) {
	h := testRouter(nil)
	rec := do(t, h, http.MethodGet, "/api/v1/meta", map[string]string{"Accept-Encoding": "gzip"})
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Errorf("expected gzip, got %q", rec.Header().Get("Content-Encoding"))
	}
}

func TestTimeoutSetsDeadline(t *testing.T) {
	var has bool
	h := Timeout(50 * time.Millisecond)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, has = r.Context().Deadline()
	}))
	do(t, h, http.MethodGet, "/", nil)
	if !has {
		t.Error("context has no deadline")
	}
}

// --- ETag ---

func TestETag(t *testing.T) {
	a := MakeETag(42, CanonicalQuery(url.Values{"to": {"2000"}, "from": {"1950"}, "types": {"flood"}}))
	b := MakeETag(42, CanonicalQuery(url.Values{"types": {"flood"}, "from": {"1950"}, "to": {"2000"}}))
	if a != b || !strings.HasPrefix(a, `W/"v42-`) {
		t.Fatalf("canonical etags differ or bad format: %s %s", a, b)
	}
	if MakeETag(43, "x") == MakeETag(42, "x") || MakeETag(42, "x") == MakeETag(42, "y") {
		t.Fatal("etag must change with version and query")
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if CheckETag(w, r, a, "public, max-age=300") {
			return
		}
		writeJSON(w, 200, map[string]int{"ok": 1})
	})
	cases := []struct {
		inm  string
		want int
	}{
		{"", 200},
		{a, 304},
		{strings.TrimPrefix(a, "W/"), 304}, // strong form of the same tag (weak comparison)
		{`"other", ` + a, 304},
		{"*", 304},
		{`W/"v41-0"`, 200},
	}
	for _, c := range cases {
		rec := do(t, handler, http.MethodGet, "/", map[string]string{"If-None-Match": c.inm})
		if rec.Code != c.want {
			t.Errorf("If-None-Match %q → %d want %d", c.inm, rec.Code, c.want)
		}
		if rec.Header().Get("ETag") != a || rec.Header().Get("Cache-Control") != "public, max-age=300" {
			t.Errorf("headers missing on %q", c.inm)
		}
		if c.want == 304 && rec.Body.Len() != 0 {
			t.Error("304 must have no body")
		}
	}
}
