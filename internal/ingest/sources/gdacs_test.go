package sources

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dwts/backend/internal/domain"
	"github.com/dwts/backend/internal/ingest"
	"github.com/dwts/backend/internal/ingest/fetch"
)

// Fixture: three real features from the Feb 2023 response (one page with
// alertlevel=Green;Orange;Red), kept unchanged.
func TestGDACSFixture(t *testing.T) {
	f, err := os.Open("../../../testdata/fixtures/gdacs.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var got []domain.Incident
	n, err := parseGDACSPage(f, func(i domain.Incident) error { got = append(got, i); return nil })
	if err != nil || n != 3 || len(got) != 3 {
		t.Fatalf("n=%d len=%d err=%v", n, len(got), err)
	}
	d := got[0]
	if d.Source != "gdacs" || d.SourceID != "DR-1023877" || d.Type != domain.Drought || d.Severity != 2 {
		t.Errorf("drought = %+v", d)
	}
	if !d.StartTime.Equal(time.Date(2022, 9, 21, 0, 0, 0, 0, time.UTC)) || d.EndTime == nil ||
		!d.EndTime.Equal(time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)) || d.TimePrecision != "day" {
		t.Errorf("drought times = %v %v %s", d.StartTime, d.EndTime, d.TimePrecision)
	}
	if d.Lon != -66.675 || d.Lat != 7.108 || len(d.Raw) == 0 {
		t.Errorf("drought point/raw = %v %v %d", d.Lon, d.Lat, len(d.Raw))
	}
	wantHints := []string{"ABW", "BRA", "COL", "CRI", "GUF", "GUY", "PAN", "SUR", "VEN"}
	if !slices.Equal(d.CountryHints, wantHints) {
		t.Errorf("hints = %v", d.CountryHints)
	}
	if !strings.HasPrefix(d.URL, "https://www.gdacs.org/report.aspx?eventid=1023877") || !strings.HasPrefix(d.Title, "Drought in Aruba, Brazil") {
		t.Errorf("url/title = %q %q", d.URL, d.Title)
	}
	if got[1].Type != domain.Wildfire || got[1].Severity != 1 || got[2].Type != domain.Flood || got[2].SourceID != "FL-1101832" {
		t.Errorf("wf/fl = %+v / %+v", got[1], got[2])
	}
	if h := gdacsHints("SSD", []gdacsCountry{{ISO3: "XKX"}, {ISO3: "pse"}, {ISO3: "SSD"}}); !slices.Equal(h, []string{"SDS", "KOS", "PSX"}) {
		t.Errorf("aliases = %v", h)
	}
	for _, body := range []string{"", "  \n"} {
		if n, err := parseGDACSPage(strings.NewReader(body), nil); n != 0 || err != nil {
			t.Errorf("empty body %q: n=%d err=%v", body, n, err)
		}
	}
}

func TestGDACSFetchEndToEnd(t *testing.T) {
	fixture, err := os.ReadFile("../../../testdata/fixtures/gdacs.json")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var urls []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		urls = append(urls, r.URL.RequestURI())
		mu.Unlock()
		q := r.URL.Query()
		switch {
		case q.Get("fromdate") == "2023-01-01" && q.Get("pagenumber") == "1":
			w.Write(fixture)
		case q.Get("fromdate") == "2023-01-01":
			w.Write([]byte(`{"type":"FeatureCollection","features":[]}`))
		default: // February: 204 No Content
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer ts.Close()

	dir := t.TempDir()
	newS := func() *GDACS {
		s := NewGDACS(fetch.New(ts.Client(), dir, nil), nil)
		s.F.Limiter = nil
		s.BaseURL = ts.URL
		s.Now = func() time.Time { return time.Date(2023, 2, 10, 0, 0, 0, 0, time.UTC) }
		return s
	}
	w := ingest.Window{From: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2023, 2, 10, 0, 0, 0, 0, time.UTC)}
	var n int
	if err := newS().Fetch(context.Background(), w, func(domain.Incident) error { n++; return nil }); err != nil {
		t.Fatal(err)
	}
	p := "/SEARCH?eventlist=FL;VO;DR;WF&alertlevel=Green;Orange;Red&pagesize=100"
	want := []string{
		p + "&fromdate=2023-01-01&todate=2023-01-31&pagenumber=1",
		p + "&fromdate=2023-01-01&todate=2023-01-31&pagenumber=2",
		p + "&fromdate=2023-02-01&todate=2023-02-28&pagenumber=1",
	}
	if n != 3 || !slices.Equal(urls, want) {
		t.Fatalf("n=%d urls=\n%s", n, strings.Join(urls, "\n"))
	}
	// Second run: January is cached, the current month (February) refetched.
	urls = nil
	if err := newS().Fetch(context.Background(), w, func(domain.Incident) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(urls, want[2:]) {
		t.Fatalf("second run urls = %v", urls)
	}
}
