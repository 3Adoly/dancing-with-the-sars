package httpapi

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dwts/backend/internal/domain"
)

type stubIncidents struct {
	lastF domain.IncidentFilter
	lastQ domain.SearchQuery
}

func (s *stubIncidents) Get(_ context.Context, id int64) (domain.Incident, error) {
	if id != 118233 {
		return domain.Incident{}, domain.ErrNotFound
	}
	var inc domain.Incident
	mustExample("incident.json", &inc)
	return inc, nil
}
func (s *stubIncidents) List(_ context.Context, f domain.IncidentFilter) ([]domain.IncidentSummary, string, error) {
	s.lastF = f
	var r IncidentsResponse
	mustExample("incidents.json", &r)
	return r.Items, r.NextCursor, nil
}
func (s *stubIncidents) Search(_ context.Context, q domain.SearchQuery) ([]domain.IncidentSummary, error) {
	s.lastQ = q
	var r SearchResponse
	mustExample("search.json", &r)
	return r.Items, nil
}

func TestIncidentEndpoints(t *testing.T) {
	st := &stubIncidents{}
	h := NewRouter(Deps{Incidents: st, Stats: &stubStats{ready: true, version: 9}})
	cases := []struct{ path, example, cc string }{
		{"/api/v1/incidents?types=volcano&from=1990&to=1995&limit=2", "incidents.json", ccIncidents},
		{"/api/v1/incidents/118233", "incident.json", ccIncident},
		{"/api/v1/search?q=katrina", "search.json", ccSearch},
	}
	for _, c := range cases {
		rec := do(t, h, http.MethodGet, c.path, nil)
		if rec.Code != 200 || rec.Header().Get("Cache-Control") != c.cc || rec.Header().Get("ETag") == "" {
			t.Fatalf("%s: %d %v", c.path, rec.Code, rec.Header())
		}
		ex, _ := os.ReadFile(filepath.Join("..", "..", "api", "examples", c.example))
		if got, want := topKeys(t, rec.Body.Bytes()), topKeys(t, ex); !reflect.DeepEqual(got, want) {
			t.Errorf("%s keys %v want %v", c.path, got, want)
		}
		if rec2 := do(t, h, http.MethodGet, c.path, map[string]string{"If-None-Match": rec.Header().Get("ETag")}); rec2.Code != 304 {
			t.Errorf("%s 304: %d", c.path, rec2.Code)
		}
	}
	if st.lastF.Limit != 2 || st.lastF.Years != (domain.YearRange{From: 1990, To: 1995}) || st.lastF.Sort != "severity" {
		t.Errorf("filter %+v", st.lastF)
	}
	do(t, h, http.MethodGet, "/api/v1/incidents?bbox=120,20,150,50&iso3=jpn&minMag=6.5&minSeverity=2&sort=time", nil)
	if st.lastF.BBox == nil || st.lastF.ISO3 != "JPN" || *st.lastF.MinMagnitude != 6.5 || st.lastF.MinSeverity != 2 || st.lastF.Sort != "time" {
		t.Errorf("filter2 %+v", st.lastF)
	}
	do(t, h, http.MethodGet, "/api/v1/search?q=katrna&types=storm&from=2000&to=2010&limit=5", nil)
	if st.lastQ.Q != "katrna" || st.lastQ.Years == nil || st.lastQ.Limit != 5 || len(st.lastQ.Types) != 1 {
		t.Errorf("search %+v", st.lastQ)
	}
	bad := []struct{ path, param string }{
		{"/api/v1/incidents?limit=0", "limit"}, {"/api/v1/incidents?minSeverity=4", "minSeverity"},
		{"/api/v1/incidents?minMag=x", "minMag"}, {"/api/v1/incidents?cursor=zzz", "cursor"},
		{"/api/v1/incidents?iso3=JAPAN", "iso3"}, {"/api/v1/incidents?bbox=1,2", "bbox"},
		{"/api/v1/search", "q"}, {"/api/v1/search?q=ab&limit=51", "limit"}, {"/api/v1/incidents/x", "id"},
	}
	for _, b := range bad {
		rec := do(t, h, http.MethodGet, b.path, nil)
		if e := decodeError(t, rec); rec.Code != 400 || e.Error.Details["param"] != b.param || rec.Header().Get("ETag") != "" {
			t.Errorf("%s: %d %+v", b.path, rec.Code, e)
		}
	}
	if rec := do(t, h, http.MethodGet, "/api/v1/incidents/7", nil); rec.Code != 404 {
		t.Errorf("unknown: %d", rec.Code)
	}
	// No incident service → 503 (after param validation).
	h2 := NewRouter(Deps{})
	if rec := do(t, h2, http.MethodGet, "/api/v1/incidents", nil); rec.Code != 503 {
		t.Errorf("nil incidents: %d", rec.Code)
	}
}
