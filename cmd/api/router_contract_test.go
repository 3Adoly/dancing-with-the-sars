package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dwts/backend/internal/config"
	"github.com/dwts/backend/internal/external/asf"
	"github.com/dwts/backend/internal/httpapi"
)

// stubASF serves the recorded real Sentinel-1 / NISAR responses offline.
type stubASF struct{ byKey map[string][]asf.Scene }

func (s stubASF) Search(_ context.Context, q asf.Query) ([]asf.Scene, error) {
	k := q.Params["platform"]
	if k == "" {
		k = q.Params["dataset"]
	}
	var out []asf.Scene
	for _, sc := range s.byKey[k] {
		if !sc.StartTime.Before(q.Start) && !sc.StartTime.After(q.End) {
			out = append(out, sc)
		}
	}
	return out, nil
}

func loadASF(t *testing.T, name string) []asf.Scene {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "testdata", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc, err := asf.Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	return sc
}

// contractRouter builds the production wiring (buildDeps) in fixture mode with an offline ASF stub.
func contractRouter(t *testing.T) http.Handler {
	t.Helper()
	td := filepath.Join("..", "..", "testdata")
	env := map[string]string{
		"STATS_FIXTURE":     filepath.Join(td, "stats_fixture.json"),
		"INCIDENTS_FIXTURE": filepath.Join(td, "incidents_fixture.json"),
		"STORIES_DIR":       filepath.Join(td, "stories"),
		"CORS_ORIGINS":      "http://localhost:5173",
	}
	cfg, err := config.FromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	deps, err := buildDeps(ctx, cfg, log, nil, stubASF{byKey: map[string][]asf.Scene{
		"SENTINEL-1": loadASF(t, "asf_sample.json"), "NISAR": loadASF(t, "asf_nisar_sample.json"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	return httpapi.NewRouter(deps)
}

// TestRouterContract checks every route E1–E15 against Plan_Detailed §8.1/§8.2: status, Content-Type,
// Cache-Control, ETag (+304 on If-None-Match), X-Request-Id and the error JSON shape.
func TestRouterContract(t *testing.T) {
	h := contractRouter(t)
	const jsonCT = "application/json; charset=utf-8"
	cases := []struct {
		name, method, path string
		status             int
		ct, cc             string
		etag               bool
		errCode            string
		key                string // a JSON field that must exist on 200
	}{
		{"E1 healthz", "GET", "/healthz", 200, jsonCT, "no-store", false, "", "cubeVersion"},
		{"E2 meta", "GET", "/api/v1/meta", 200, jsonCT, "public, max-age=300", true, "", "datasetVersion"},
		{"E3 incident-types", "GET", "/api/v1/incident-types", 200, jsonCT, "public, max-age=3600", true, "", "types"},
		{"E4 choropleth", "GET", "/api/v1/stats/choropleth?types=earthquake,flood&from=1950&to=2000", 200, jsonCT, "public, max-age=300, stale-while-revalidate=3600", true, "", "counts"},
		{"E5 timeseries", "GET", "/api/v1/stats/timeseries?types=storm&from=1980&to=1983&iso3=PHL", 200, jsonCT, "public, max-age=300", true, "", "points"},
		{"E6 grid (A8)", "GET", "/api/v1/stats/grid?types=earthquake&bbox=120,20,150,50", 501, jsonCT, "no-store", false, "not_implemented", ""},
		{"E7 incidents (fixture: needs DB)", "GET", "/api/v1/incidents?types=volcano", 501, jsonCT, "no-store", false, "not_implemented", ""},
		{"E8 incident", "GET", "/api/v1/incidents/500812", 200, jsonCT, "public, max-age=3600", true, "", "sarAvailability"},
		{"E8 unknown", "GET", "/api/v1/incidents/42", 404, jsonCT, "no-store", false, "not_found", ""},
		{"E9 short q", "GET", "/api/v1/search?q=k", 400, jsonCT, "no-store", false, "invalid_param", ""},
		{"E7 bad limit", "GET", "/api/v1/incidents?limit=9999", 400, jsonCT, "no-store", false, "invalid_param", ""},
		{"E9 search (fixture: needs DB)", "GET", "/api/v1/search?q=katrina", 501, jsonCT, "no-store", false, "not_implemented", ""},
		{"E10 sar S1", "GET", "/api/v1/incidents/500812/sar", 200, jsonCT, "public, max-age=3600", false, "", "bestPair"},
		{"E10 sar NISAR", "GET", "/api/v1/incidents/700001/sar", 200, jsonCT, "public, max-age=3600", false, "", "storyId"},
		{"E10 sar pre-SAR", "GET", "/api/v1/incidents/118233/sar", 200, jsonCT, "public, max-age=3600", false, "", "coverage"},
		{"E11 coverage-timeline", "GET", "/api/v1/sar/coverage-timeline", 200, jsonCT, "public, max-age=86400", false, "", "byYear"},
		{"E12 stories", "GET", "/api/v1/stories", 200, jsonCT, "public, max-age=300", true, "", "stories"},
		{"E13 story", "GET", "/api/v1/stories/aomori-2025-nisar", 200, jsonCT, "public, max-age=300", true, "", "layers"},
		{"E14 media png", "GET", "/media/stories/aomori-2025-nisar/change_logratio.png", 200, "image/png", "public, max-age=86400, immutable", false, "", ""},
		{"E14 media jpg", "GET", "/media/stories/aomori-2025-nisar/thumb.jpg", 200, "image/jpeg", "public, max-age=86400, immutable", false, "", ""},
		{"E15 chat disabled", "POST", "/api/v1/chat", 503, jsonCT, "no-store", false, "chat_disabled", ""},
		// errors
		{"E4 missing types", "GET", "/api/v1/stats/choropleth", 400, jsonCT, "no-store", false, "invalid_param", ""},
		{"E5 bad iso3", "GET", "/api/v1/stats/timeseries?types=storm&iso3=PHILIPPINES", 400, jsonCT, "no-store", false, "invalid_param", ""},
		{"E10 bad id", "GET", "/api/v1/incidents/abc/sar", 400, jsonCT, "no-store", false, "invalid_param", ""},
		{"E10 unknown incident", "GET", "/api/v1/incidents/42/sar", 404, jsonCT, "no-store", false, "not_found", ""},
		{"E10 bad window", "GET", "/api/v1/incidents/500812/sar?windowDays=500", 400, jsonCT, "no-store", false, "invalid_param", ""},
		{"E13 unknown story", "GET", "/api/v1/stories/nope", 404, jsonCT, "no-store", false, "not_found", ""},
		{"E14 traversal", "GET", "/media/stories/aomori-2025-nisar/..%2f..%2f..%2fgo.mod", 404, jsonCT, "no-store", false, "not_found", ""},
		{"E14 bad ext", "GET", "/media/stories/aomori-2025-nisar/manifest.txt", 404, jsonCT, "no-store", false, "not_found", ""},
		{"unknown route", "GET", "/api/v1/nope", 404, jsonCT, "no-store", false, "not_found", ""},
		{"wrong method", "POST", "/api/v1/meta", 405, jsonCT, "no-store", false, "method_not_allowed", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := serve(h, c.method, c.path, nil)
			hd := rec.Header()
			if rec.Code != c.status {
				t.Fatalf("status %d want %d: %s", rec.Code, c.status, rec.Body)
			}
			if hd.Get("Content-Type") != c.ct {
				t.Errorf("Content-Type %q want %q", hd.Get("Content-Type"), c.ct)
			}
			if hd.Get("Cache-Control") != c.cc {
				t.Errorf("Cache-Control %q want %q", hd.Get("Cache-Control"), c.cc)
			}
			if hd.Get("X-Request-Id") == "" {
				t.Error("missing X-Request-Id")
			}
			if (hd.Get("ETag") != "") != c.etag {
				t.Errorf("ETag %q, want present=%v", hd.Get("ETag"), c.etag)
			}
			if c.errCode != "" {
				var e httpapi.ErrorBody
				if json.Unmarshal(rec.Body.Bytes(), &e) != nil || e.Error.Code != c.errCode || e.Error.Message == "" || e.RequestID != hd.Get("X-Request-Id") {
					t.Errorf("error body %s", rec.Body)
				}
			}
			if c.key != "" {
				var m map[string]json.RawMessage
				if json.Unmarshal(rec.Body.Bytes(), &m) != nil || len(m[c.key]) == 0 || string(m[c.key]) == "null" {
					t.Errorf("field %q missing in %.200s", c.key, rec.Body)
				}
			}
			if c.etag {
				rec2 := serve(h, c.method, c.path, map[string]string{"If-None-Match": hd.Get("ETag")})
				if rec2.Code != http.StatusNotModified || rec2.Body.Len() != 0 || rec2.Header().Get("Cache-Control") != c.cc {
					t.Errorf("304: %d len=%d cc=%q", rec2.Code, rec2.Body.Len(), rec2.Header().Get("Cache-Control"))
				}
			}
			if c.method == http.MethodGet && c.status == 200 { // HEAD mirrors GET headers, no body
				hr := serve(h, http.MethodHead, c.path, nil)
				if hr.Code != 200 || hr.Body.Len() != 0 || hr.Header().Get("Content-Type") != c.ct {
					t.Errorf("HEAD: %d len=%d ct=%q", hr.Code, hr.Body.Len(), hr.Header().Get("Content-Type"))
				}
			}
		})
	}
}

func TestRouterGzipThreshold(t *testing.T) {
	h := contractRouter(t)
	big := serve(h, "GET", "/api/v1/incident-types", map[string]string{"Accept-Encoding": "gzip"}) // ~1.5 KB
	small := serve(h, "GET", "/healthz", map[string]string{"Accept-Encoding": "gzip"})             // < 1 KB
	plain := serve(h, "GET", "/api/v1/incident-types", nil)
	img := serve(h, "GET", "/media/stories/aomori-2025-nisar/before_hh.png", map[string]string{"Accept-Encoding": "gzip"})
	if big.Header().Get("Content-Encoding") != "gzip" || small.Header().Get("Content-Encoding") != "" ||
		plain.Header().Get("Content-Encoding") != "" || img.Header().Get("Content-Encoding") != "" {
		t.Errorf("gzip: big=%q small=%q plain=%q img=%q", big.Header().Get("Content-Encoding"),
			small.Header().Get("Content-Encoding"), plain.Header().Get("Content-Encoding"), img.Header().Get("Content-Encoding"))
	}
	if plain.Body.Len() < 1024 {
		t.Errorf("incident-types should be ≥ 1 KB, got %d", plain.Body.Len())
	}
	if !strings.Contains(strings.Join(plain.Header().Values("Vary"), ","), "Accept-Encoding") {
		t.Error("missing Vary: Accept-Encoding")
	}
}

func serve(h http.Handler, method, path string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
