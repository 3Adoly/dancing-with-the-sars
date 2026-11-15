package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dwts/backend/internal/domain"
)

// stubStats serves the api/examples payloads and records the last queries.
type stubStats struct {
	ready   bool
	version int64
	lastCh  domain.ChoroplethQuery
	lastTS  domain.TimeseriesQuery
	calls   int
}

func (s *stubStats) Ready() bool            { return s.ready }
func (s *stubStats) Version() int64         { return s.version }
func (s *stubStats) YearBounds() (int, int) { return 1900, 2026 }
func (s *stubStats) IncidentTypes() []domain.TypeInfo {
	var r IncidentTypesResponse
	mustExample("incident-types.json", &r)
	return r.Types
}
func (s *stubStats) Meta(context.Context) (domain.Meta, error) {
	s.calls++
	var m domain.Meta
	mustExample("meta.json", &m)
	return m, nil
}
func (s *stubStats) Choropleth(_ context.Context, q domain.ChoroplethQuery) (domain.Choropleth, error) {
	s.calls++
	s.lastCh = q
	var c domain.Choropleth
	mustExample("choropleth.json", &c)
	return c, nil
}
func (s *stubStats) Timeseries(_ context.Context, q domain.TimeseriesQuery) (domain.Timeseries, error) {
	s.calls++
	s.lastTS = q
	var t domain.Timeseries
	mustExample("timeseries.json", &t)
	return t, nil
}

func mustExample(name string, v any) {
	b, err := os.ReadFile(filepath.Join("..", "..", "api", "examples", name))
	if err != nil {
		panic(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		panic(err)
	}
}

// topKeys returns the sorted top-level keys of a JSON object.
func topKeys(t *testing.T, b []byte) []string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("not a JSON object: %v (%s)", err, b)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func TestStatsEndpointsShapeMatchesExamples(t *testing.T) {
	h := testRouterWith(nil, &stubStats{ready: true, version: 42})
	cases := []struct{ path, example, cc string }{
		{"/api/v1/meta", "meta.json", ccMeta},
		{"/api/v1/incident-types", "incident-types.json", ccTypes},
		{"/api/v1/stats/choropleth?types=earthquake,flood&from=1950&to=2000", "choropleth.json", ccChoropleth},
		{"/api/v1/stats/timeseries?types=storm&from=1980&to=1983&iso3=phl", "timeseries.json", ccTimeseries},
	}
	for _, c := range cases {
		rec := do(t, h, http.MethodGet, c.path, nil)
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", c.path, rec.Code, rec.Body)
		}
		ex, _ := os.ReadFile(filepath.Join("..", "..", "api", "examples", c.example))
		if got, want := topKeys(t, rec.Body.Bytes()), topKeys(t, ex); !reflect.DeepEqual(got, want) {
			t.Errorf("%s keys %v want %v", c.path, got, want)
		}
		if rec.Header().Get("Cache-Control") != c.cc || !strings.HasPrefix(rec.Header().Get("ETag"), `W/"v42-`) {
			t.Errorf("%s: cache headers %q %q", c.path, rec.Header().Get("Cache-Control"), rec.Header().Get("ETag"))
		}
	}
}

func TestStatsParamsPassedThrough(t *testing.T) {
	st := &stubStats{ready: true, version: 1}
	h := testRouterWith(nil, st)
	do(t, h, http.MethodGet, "/api/v1/stats/choropleth?types=flood,earthquake&from=1800&to=1999", nil)
	if !reflect.DeepEqual(st.lastCh, domain.ChoroplethQuery{Types: []domain.IncidentType{"flood", "earthquake"}, Years: domain.YearRange{From: 1900, To: 1999}}) {
		t.Errorf("choropleth query %+v", st.lastCh)
	}
	do(t, h, http.MethodGet, "/api/v1/stats/timeseries?types=storm&iso3=phl", nil)
	if st.lastTS.ISO3 != "PHL" || st.lastTS.Years != (domain.YearRange{From: 1900, To: 2026}) {
		t.Errorf("timeseries query %+v", st.lastTS)
	}
}

func TestStats304BeforeCompute(t *testing.T) {
	st := &stubStats{ready: true, version: 42}
	h := testRouterWith(nil, st)
	for _, p := range []string{"/api/v1/meta", "/api/v1/stats/choropleth?types=flood", "/api/v1/stats/timeseries?types=flood"} {
		first := do(t, h, http.MethodGet, p, nil)
		calls := st.calls
		// Same query, parameters reordered → same ETag → 304 without calling the service.
		p2 := p
		if strings.Contains(p, "?") {
			p2 = p + "&from=1900"
			first = do(t, h, http.MethodGet, p2, nil)
			calls = st.calls
		}
		rec := do(t, h, http.MethodGet, p2, map[string]string{"If-None-Match": first.Header().Get("ETag")})
		if rec.Code != http.StatusNotModified || st.calls != calls {
			t.Errorf("%s: code %d, service calls %d→%d", p2, rec.Code, calls, st.calls)
		}
	}
	// A new dataset version invalidates the ETag.
	etag := do(t, h, http.MethodGet, "/api/v1/meta", nil).Header().Get("ETag")
	st.version = 43
	if rec := do(t, h, http.MethodGet, "/api/v1/meta", map[string]string{"If-None-Match": etag}); rec.Code != 200 {
		t.Errorf("version bump should miss the cache, got %d", rec.Code)
	}
}

func TestStatsBadParams(t *testing.T) {
	h := testRouterWith(nil, &stubStats{ready: true, version: 1})
	cases := []struct{ path, param string }{
		{"/api/v1/stats/choropleth", "types"},
		{"/api/v1/stats/choropleth?types=meteor", "types"},
		{"/api/v1/stats/choropleth?types=flood,fire_activity", "types"},
		{"/api/v1/stats/choropleth?types=flood&from=2000&to=1990", "from"},
		{"/api/v1/stats/choropleth?types=flood&level=admin1", "level"},
		{"/api/v1/stats/timeseries?types=flood&iso3=JAPAN", "iso3"},
		{"/api/v1/stats/timeseries?types=flood&to=abc", "to"},
	}
	for _, c := range cases {
		rec := do(t, h, http.MethodGet, c.path, nil)
		body := decodeError(t, rec)
		if rec.Code != 400 || body.Error.Code != CodeInvalidParam || body.Error.Details["param"] != c.param {
			t.Errorf("%s: %d %+v", c.path, rec.Code, body)
		}
	}
}

func TestStatsNotReady(t *testing.T) {
	for name, st := range map[string]StatsAPI{"nil service": nil, "cube not loaded": &stubStats{}} {
		h := testRouterWith(nil, st)
		for _, p := range []string{"/api/v1/meta", "/api/v1/stats/choropleth?types=flood", "/api/v1/stats/timeseries?types=flood"} {
			rec := do(t, h, http.MethodGet, p, nil)
			if rec.Code != 503 || decodeError(t, rec).Error.Code != CodeNotReady {
				t.Errorf("%s %s: %d %s", name, p, rec.Code, rec.Body)
			}
		}
		// E3 works without data.
		rec := do(t, h, http.MethodGet, "/api/v1/incident-types", nil)
		var r IncidentTypesResponse
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &r) != nil || len(r.Types) != len(domain.AllTypes) {
			t.Errorf("%s incident-types: %d %s", name, rec.Code, rec.Body)
		}
	}
}
