package sources

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/dwts/backend/internal/domain"
	"github.com/dwts/backend/internal/ingest"
	"github.com/dwts/backend/internal/ingest/fetch"
)

// gvpFixture: 6 real GVP features (fetched 2026-11-15): Pacaya 10750-10752
// (year-only, year-only, full date), Pinatubo 1991, Campi Flegrei -8480 (BCE)
// and an uncertain Long Island 1961 with no VEI. Unchanged except the
// envelope, which keeps only type, features and totalFeatures.
const gvpFixture = "../../../testdata/fixtures/gvp.json"

func parseGVPFixture(t *testing.T, w ingest.Window) []domain.Incident {
	t.Helper()
	f, err := os.Open(gvpFixture)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var got []domain.Incident
	if _, err := parseGVP(context.Background(), f, w, func(i domain.Incident) error { got = append(got, i); return nil }); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestGVPFixture(t *testing.T) {
	w := ingest.Window{From: time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	got := parseGVPFixture(t, w)
	if len(got) != 2 {
		t.Fatalf("1900+ window: got %d, want 2 (Pinatubo, Long Island)", len(got))
	}
	all := parseGVPFixture(t, ingest.Window{From: time.Date(-9999, 1, 1, 0, 0, 0, 0, time.UTC), To: w.To})
	if len(all) != 6 {
		t.Fatalf("all: got %d, want 6", len(all))
	}
	by := map[string]domain.Incident{}
	for _, i := range all {
		by[i.SourceID] = i
	}
	p := by["16867"]
	if p.Source != "gvp" || p.Type != domain.Volcano || p.Name != "Pinatubo" || p.Title != "Pinatubo eruption (1991)" ||
		p.TimePrecision != "day" || p.StartTime != time.Date(1991, 4, 2, 0, 0, 0, 0, time.UTC) ||
		p.Magnitude == nil || *p.Magnitude != 6 || p.MagnitudeUnit != "VEI" || p.Severity != 3 || p.Lon != 120.35 || p.Lat != 15.13 || p.Raw == nil {
		t.Errorf("Pinatubo = %+v", p)
	}
	if by["10750"].TimePrecision != "year" || by["10752"].TimePrecision != "day" {
		t.Errorf("precision: %q %q", by["10750"].TimePrecision, by["10752"].TimePrecision)
	}
	if l := by["14961"]; l.Magnitude != nil || l.MagnitudeUnit != "" || l.Severity != 1 || l.Props["activityType"] != "Uncertain Eruption" {
		t.Errorf("Long Island = %+v", l)
	}
}

type gvpServer struct {
	mu   sync.Mutex
	reqs []string
	body []byte
}

func (s *gvpServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.reqs = append(s.reqs, r.URL.RequestURI())
	s.mu.Unlock()
	w.Write(s.body)
}

func TestGVPFetchEndToEnd(t *testing.T) {
	body, err := os.ReadFile(gvpFixture)
	if err != nil {
		t.Fatal(err)
	}
	srv := &gvpServer{body: body}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	f := fetch.New(ts.Client(), t.TempDir(), quietLog())
	s := NewGVP(f, quietLog())
	s.URL = ts.URL + "/geoserver/GVP-VOTW/ows?service=WFS&version=2.0.0&request=GetFeature&typeName=GVP-VOTW:Smithsonian_VOTW_Holocene_Eruptions&outputFormat=application/json"
	if s.Name() != "gvp" || s.F == f {
		t.Fatal("name or limiter copy")
	}
	w := ingest.Window{From: time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	for i := 0; i < 2; i++ { // second run is served from the cache
		n := 0
		if err := s.Fetch(context.Background(), w, func(domain.Incident) error { n++; return nil }); err != nil || n != 2 {
			t.Fatalf("run %d: n=%d err=%v", i, n, err)
		}
	}
	want := "/geoserver/GVP-VOTW/ows?service=WFS&version=2.0.0&request=GetFeature&typeName=GVP-VOTW:Smithsonian_VOTW_Holocene_Eruptions&outputFormat=application/json"
	if len(srv.reqs) != 1 || srv.reqs[0] != want {
		t.Errorf("requests = %v, want one %s", srv.reqs, want)
	}
}
