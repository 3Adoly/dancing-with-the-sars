package httpapi

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestMethodNotAllowedJSONEverywhere(t *testing.T) {
	h := testRouterWith(nil, &stubStats{ready: true, version: 1})
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/healthz"},
		{http.MethodPost, "/api/v1/meta"},
		{http.MethodDelete, "/api/v1/stories"},
		{http.MethodPut, "/api/v1/incidents/1/sar"},
		{http.MethodGet, "/api/v1/chat"},
		{http.MethodPost, "/media/stories/a/b.png"},
	} {
		rec := do(t, h, c.method, c.path, nil)
		if rec.Code != http.StatusMethodNotAllowed || decodeError(t, rec).Error.Code != CodeMethodNotAllowed ||
			!strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
			t.Errorf("%s %s: %d %q %s", c.method, c.path, rec.Code, rec.Header().Get("Content-Type"), rec.Body)
		}
	}
}

func TestHEADOnGETRoutes(t *testing.T) {
	h := testRouterWith(nil, &stubStats{ready: true, version: 3})
	for _, p := range []string{"/healthz", "/api/v1/meta", "/api/v1/incident-types", "/api/v1/stats/choropleth?types=flood"} {
		rec := do(t, h, http.MethodHead, p, nil)
		if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
			t.Errorf("HEAD %s: %d %v", p, rec.Code, rec.Header())
		}
	}
	rec := do(t, h, http.MethodHead, "/api/v1/meta", nil)
	if rec.Header().Get("ETag") == "" || rec.Header().Get("Cache-Control") != ccMeta {
		t.Errorf("HEAD must carry cache headers: %v", rec.Header())
	}
}

func TestTimeoutWrites504(t *testing.T) {
	// A handler that ignores ctx and returns after the deadline without writing.
	slow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		time.Sleep(5 * time.Millisecond)
	})
	h := RequestID(Timeout(20 * time.Millisecond)(slow))
	rec := do(t, h, http.MethodGet, "/", nil)
	if rec.Code != http.StatusGatewayTimeout || decodeError(t, rec).Error.Code != CodeUpstreamTimeout {
		t.Errorf("timeout: %d %s", rec.Code, rec.Body)
	}
	// A handler that answers in time is untouched.
	fast := RequestID(Timeout(time.Second)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]bool{"ok": true})
	})))
	if rec := do(t, fast, http.MethodGet, "/", nil); rec.Code != 200 {
		t.Errorf("fast: %d", rec.Code)
	}
}

func TestOddQueryInputNever500(t *testing.T) {
	h := testRouterWith(nil, &stubStats{ready: true, version: 1})
	long := strings.Repeat("a", MaxQueryBytes+10)
	cases := []string{
		"/api/v1/stats/choropleth?types=" + long,
		"/api/v1/stats/choropleth?types=%ff%fe", // invalid UTF-8
		"/api/v1/stats/choropleth?types=%zz",    // invalid escape
		"/api/v1/stats/choropleth?types=" + strings.Repeat("flood,", 500) + "meteor",
		"/api/v1/stats/choropleth?types=%E5%9C%B0%E9%9C%87", // unicode "地震"
		"/api/v1/stats/choropleth?types=flood&from=99999999999999999999",
		"/api/v1/stats/timeseries?types=flood&iso3=%C3%84%C3%84%C3%84", // ÄÄÄ
		"/api/v1/incidents/99999999999999999999999/sar",
		"/api/v1/incidents/1/sar?windowDays=1e9",
		"/api/v1/stats/choropleth?types=flood&level=%00",
	}
	for _, p := range cases {
		rec := do(t, h, http.MethodGet, p, nil)
		if rec.Code >= 500 {
			t.Errorf("%.80s → %d", p, rec.Code)
		}
		if rec.Code == 400 && decodeError(t, rec).Error.Code != CodeInvalidParam {
			t.Errorf("%.80s: wrong code", p)
		}
	}
	// Repeated params: the first value wins; a valid first value is accepted.
	rec := do(t, h, http.MethodGet, "/api/v1/stats/choropleth?types=flood&types=meteor&from=1950&from=x", nil)
	if rec.Code != 200 {
		t.Errorf("repeated params: %d %s", rec.Code, rec.Body)
	}
}

func TestCORSPreflightAndExpose(t *testing.T) {
	h := testRouterWith(nil, &stubStats{ready: true, version: 1})
	rec := do(t, h, http.MethodOptions, "/api/v1/stats/choropleth", map[string]string{
		"Origin":                         "http://localhost:5173",
		"Access-Control-Request-Method":  "GET",
		"Access-Control-Request-Headers": "If-None-Match, X-Request-ID",
	})
	hd := rec.Header()
	if rec.Code != 200 && rec.Code != 204 {
		t.Fatalf("preflight status %d", rec.Code)
	}
	if hd.Get("Access-Control-Allow-Origin") != "http://localhost:5173" || !strings.Contains(hd.Get("Access-Control-Allow-Methods"), "GET") ||
		hd.Get("Access-Control-Max-Age") != "300" {
		t.Errorf("preflight headers %v", hd)
	}
	allowH := strings.ToLower(hd.Get("Access-Control-Allow-Headers"))
	if !strings.Contains(allowH, "if-none-match") || !strings.Contains(allowH, "x-request-id") {
		t.Errorf("allow-headers %q", allowH)
	}
	// POST preflight (chat) is allowed too.
	rec = do(t, h, http.MethodOptions, "/api/v1/chat", map[string]string{"Origin": "http://localhost:5173",
		"Access-Control-Request-Method": "POST", "Access-Control-Request-Headers": "Content-Type"})
	if !strings.Contains(rec.Header().Get("Access-Control-Allow-Methods"), "POST") {
		t.Errorf("POST preflight %v", rec.Header())
	}
	// Disallowed method / origin.
	rec = do(t, h, http.MethodOptions, "/api/v1/meta", map[string]string{"Origin": "https://evil.example", "Access-Control-Request-Method": "GET"})
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("disallowed origin echoed on preflight")
	}
	// Cross-origin GET exposes ETag and X-Request-Id; If-None-Match works cross-origin.
	rec = do(t, h, http.MethodGet, "/api/v1/meta", map[string]string{"Origin": "http://localhost:5173"})
	exp := strings.ToLower(rec.Header().Get("Access-Control-Expose-Headers"))
	if !strings.Contains(exp, "etag") || !strings.Contains(exp, "x-request-id") {
		t.Errorf("expose headers %q", exp)
	}
	rec2 := do(t, h, http.MethodGet, "/api/v1/meta", map[string]string{"Origin": "http://localhost:5173", "If-None-Match": rec.Header().Get("ETag")})
	if rec2.Code != 304 || rec2.Header().Get("Access-Control-Allow-Origin") != "http://localhost:5173" {
		t.Errorf("cross-origin 304: %d %v", rec2.Code, rec2.Header())
	}
}
