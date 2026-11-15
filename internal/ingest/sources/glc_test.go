package sources

import (
	"context"
	"errors"
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

// glcFixture holds real rows of Global_Landslide_Catalog_Export_rows.csv
// (header, then rows re-written with Python's csv module, fetched
// 2026-11-15): 684 (CN), 5971 Oso (US), 10790 Mocoa (no country_code,
// catastrophic), 2988 Nova Friburgo (very_large, no source_link), 7618
// Taipei (TW), 3032 Jølster (NO), 3226 Windhoek (NA, 11:00 PM), 9991 (no
// country, size, trigger, setting or fatality_count), 4713 (a description
// with blank lines), 6436 (1988, the oldest row). Two rows are HAND-EDITED:
//   - 900001: row 9810 with event_id changed and country_code "ZZ" (an ISO2
//     the countries table does not know): emitted without a country hint
//   - 900002: row 9958 with event_id changed and event_date "2017-07-03"
//     (not the GLC layout): skipped
const glcFixture = "../../../testdata/fixtures/glc_sample.csv"

// glcTestISO2 stands in for the map cmd/ingest loads from countries.iso2. It
// has no TW on purpose: Natural Earth gives Taiwan ISO_A2 "CN-TW".
var glcTestISO2 = map[string]string{
	"CN": "CHN", "US": "USA", "BR": "BRA", "NO": "NOR", "NA": "NAM", "ID": "IDN", "PH": "PHL", "NP": "NPL",
}

func parseGLCFixture(t *testing.T, iso2 map[string]string, w ingest.Window) ([]domain.Incident, glcStats) {
	t.Helper()
	f, err := os.Open(glcFixture)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var got []domain.Incident
	st, err := parseGLC(context.Background(), f, iso2, w, func(inc domain.Incident) error {
		got = append(got, inc)
		return nil
	})
	if err != nil {
		t.Fatalf("parseGLC: %v", err)
	}
	if st.emitted != len(got) {
		t.Errorf("reported %d emitted, callback saw %d", st.emitted, len(got))
	}
	return got, st
}

func findGLC(t *testing.T, got []domain.Incident, id string) domain.Incident {
	t.Helper()
	for _, inc := range got {
		if inc.SourceID == id {
			return inc
		}
	}
	t.Fatalf("event %s not emitted", id)
	return domain.Incident{}
}

func TestGLCFixture(t *testing.T) {
	got, st := parseGLCFixture(t, glcTestISO2, ingest.Window{})
	var ids []string
	for _, inc := range got {
		ids = append(ids, inc.SourceID)
	}
	want := "684 5971 10790 2988 7618 3032 3226 9991 4713 6436 900001"
	if strings.Join(ids, " ") != want {
		t.Fatalf("emitted %v\nwant    %s", ids, want)
	}
	if st != (glcStats{rows: 12, emitted: 11, badDate: 1, hinted: 8, noCode: 2, unknownCode: 1}) {
		t.Errorf("stats = %+v", st)
	}
	for i := range got {
		inc := &got[i]
		if inc.Source != "glc" || inc.Type != domain.Landslide || inc.TimePrecision != "day" || inc.Raw != nil {
			t.Errorf("%s: source %q type %q precision %q raw %v", inc.SourceID, inc.Source, inc.Type, inc.TimePrecision, inc.Raw)
		}
		if !ingest.Finalize(inc, 1900) {
			t.Errorf("Finalize rejected %s", inc.SourceID)
		}
	}

	// Oso, Washington: the well-known one.
	o := findGLC(t, got, "5971")
	if o.Title != "Oso Mudslide/ Hazel Landslide" || o.Name != "" {
		t.Errorf("oso title %q name %q", o.Title, o.Name)
	}
	checkTime(t, o, time.Date(2014, 3, 22, 10, 45, 0, 0, time.UTC), "day")
	if o.Lon != -121.8495 || o.Lat != 48.2847 || o.Severity != 2 {
		t.Errorf("oso point %v %v severity %d", o.Lon, o.Lat, o.Severity)
	}
	checkDeaths(t, o, 43)
	checkMag(t, o, nil, "")
	if !slices.Equal(o.CountryHints, []string{"USA"}) {
		t.Errorf("oso hints = %v", o.CountryHints)
	}
	if o.URL != "http://www.sltrib.com/home/2321393-155/bell-tolls-names-read-to-honor" {
		t.Errorf("oso url = %q", o.URL)
	}
	if !strings.HasPrefix(o.Description, "on Sunday, March 23, 2014, the day after a giant landslide") {
		t.Errorf("oso description = %.60q", o.Description)
	}
	if o.PlaceText != "Whitman Bench, Washington, United States" {
		t.Errorf("oso place = %q", o.PlaceText)
	}
	checkProps(t, o, `{"category":"mudslide","locationAccuracy":"exact","setting":"natural_slope","trigger":"downpour"}`)
	if !strings.Contains(o.SearchText, "oso mudslide") || !strings.Contains(o.SearchText, "washington") || !strings.Contains(o.SearchText, "landslide 2014 5971") {
		t.Errorf("oso search text = %q", o.SearchText)
	}

	// Mocoa: catastrophic → 3, no country_code → no hint.
	m := findGLC(t, got, "10790")
	if m.Severity != 3 || m.CountryHints != nil {
		t.Errorf("mocoa severity %d hints %v", m.Severity, m.CountryHints)
	}
	checkDeaths(t, m, 329)
	checkTime(t, m, time.Date(2017, 3, 25, 17, 32, 0, 0, time.UTC), "day")

	// very_large → 3; no source_link → no URL; midnight.
	nf := findGLC(t, got, "2988")
	if nf.Severity != 3 || nf.URL != "" || !slices.Equal(nf.CountryHints, []string{"BRA"}) {
		t.Errorf("nova friburgo severity %d url %q hints %v", nf.Severity, nf.URL, nf.CountryHints)
	}
	checkDeaths(t, nf, 0)
	checkTime(t, nf, time.Date(2011, 1, 12, 0, 0, 0, 0, time.UTC), "day")

	// TW is not in the countries map (Natural Earth says CN-TW): the alias gives TWN.
	if tw := findGLC(t, got, "7618"); !slices.Equal(tw.CountryHints, []string{"TWN"}) || tw.Severity != 2 {
		t.Errorf("taipei hints %v severity %d", tw.CountryHints, tw.Severity)
	}
	// NO resolves through the map (iso2 filled from ISO_A2_EH).
	if no := findGLC(t, got, "3032"); !slices.Equal(no.CountryHints, []string{"NOR"}) || !strings.Contains(no.Title, "Jølster") {
		t.Errorf("jølster hints %v title %q", no.CountryHints, no.Title)
	}
	// NA is Namibia, not a missing value; 11:00 PM keeps its calendar day.
	na := findGLC(t, got, "3226")
	if !slices.Equal(na.CountryHints, []string{"NAM"}) || na.Severity != 1 {
		t.Errorf("windhoek hints %v severity %d", na.CountryHints, na.Severity)
	}
	checkTime(t, na, time.Date(2011, 3, 15, 23, 0, 0, 0, time.UTC), "day")
	if na.Year != 2011 {
		t.Errorf("windhoek year = %d", na.Year)
	}

	// Mostly blank row: size "" → 1, no deaths, only the props that are set.
	b := findGLC(t, got, "9991")
	if b.Severity != 1 || b.CountryHints != nil || b.PlaceText != "Pasang Lhamu Highway, Nepal" {
		t.Errorf("9991 severity %d hints %v place %q", b.Severity, b.CountryHints, b.PlaceText)
	}
	checkDeaths(t, b, -1)
	checkProps(t, b, `{"category":"landslide","locationAccuracy":"50km"}`)

	// A multi-line quoted description survives the CSV reader.
	if d := findGLC(t, got, "4713"); !strings.Contains(d.Description, "\n") || !strings.HasSuffix(d.Description, "Ita, 10.") {
		t.Errorf("4713 description = %q", d.Description)
	}
	if old := findGLC(t, got, "6436"); old.Year != 1988 {
		t.Errorf("6436 year = %d", old.Year)
	}
	// An ISO2 the map does not know is no hint, not an error.
	if z := findGLC(t, got, "900001"); z.CountryHints != nil {
		t.Errorf("ZZ hints = %v, want none", z.CountryHints)
	}
}

func TestGLCSeverity(t *testing.T) {
	for size, want := range map[string]domain.Severity{
		"catastrophic": 3, "very_large": 3, "large": 2, "medium": 2, "small": 1, "unknown": 1, "": 1,
		" Large ": 2, "VERY_LARGE": 3,
	} {
		if got := glcSeverity(size); got != want {
			t.Errorf("glcSeverity(%q) = %d, want %d", size, got, want)
		}
	}
}

func TestGLCCountryHint(t *testing.T) {
	for _, tc := range []struct {
		code string
		iso2 map[string]string
		want []string
	}{
		{"US", glcTestISO2, []string{"USA"}},
		{" us ", glcTestISO2, []string{"USA"}},
		{"TW", glcTestISO2, []string{"TWN"}},
		{"TW", nil, []string{"TWN"}}, // the alias works without a map
		{"US", nil, nil},
		{"", glcTestISO2, nil},
		{"ZZ", glcTestISO2, nil},
	} {
		if got := glcHint(tc.code, tc.iso2); !slices.Equal(got, tc.want) {
			t.Errorf("glcHint(%q, %d codes) = %v, want %v", tc.code, len(tc.iso2), got, tc.want)
		}
	}
}

func TestGLCWindow(t *testing.T) {
	got, st := parseGLCFixture(t, glcTestISO2, ingest.Window{From: d(2014, 1, 1), To: d(2015, 1, 1)})
	if len(got) != 1 || got[0].SourceID != "5971" || st.window != 10 || st.badDate != 1 {
		t.Errorf("2014 window: %d emitted, stats %+v", len(got), st)
	}
	// From is inclusive, To exclusive.
	got, _ = parseGLCFixture(t, glcTestISO2, ingest.Window{From: time.Date(2011, 3, 15, 23, 0, 0, 0, time.UTC), To: d(2011, 3, 16)})
	if len(got) != 1 || got[0].SourceID != "3226" {
		t.Errorf("edge window emitted %d", len(got))
	}
	got, _ = parseGLCFixture(t, glcTestISO2, ingest.Window{From: d(2011, 1, 1), To: time.Date(2011, 3, 15, 23, 0, 0, 0, time.UTC)})
	if len(got) != 2 { // 2988, 3032
		t.Errorf("window ending at 3226 emitted %d, want 2", len(got))
	}
}

func TestGLCParseErrors(t *testing.T) {
	emitNone := func(domain.Incident) error { return nil }
	if _, err := parseGLC(context.Background(), strings.NewReader("event_id,event_date\n1,01/01/2010 12:00:00 AM\n"), nil, ingest.Window{}, emitNone); err == nil || !strings.Contains(err.Error(), "event_title") {
		t.Errorf("missing columns: err = %v", err)
	}
	if _, err := parseGLC(context.Background(), strings.NewReader(""), nil, ingest.Window{}, emitNone); err == nil {
		t.Error("empty body: want an error")
	}

	// Bad coordinates or an empty id skip the row, they do not stop the parse.
	head := "event_id,event_date,event_title,longitude,latitude\n"
	st, err := parseGLC(context.Background(), strings.NewReader(head+
		"1,01/01/2010 12:00:00 AM,A,,\n"+
		"2,01/01/2010 12:00:00 AM,B,200,10\n"+
		",01/01/2010 12:00:00 AM,C,10,10\n"+
		"4,01/01/2010 12:00:00 AM,D,10,10\n"), nil, ingest.Window{}, emitNone)
	if err != nil || st.emitted != 1 || st.badCoords != 2 || st.noID != 1 {
		t.Errorf("bad rows: err %v stats %+v", err, st)
	}

	f, err := os.Open(glcFixture)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	boom := errors.New("boom")
	calls := 0
	_, err = parseGLC(context.Background(), f, nil, ingest.Window{}, func(domain.Incident) error { calls++; return boom })
	if !errors.Is(err, boom) || calls != 1 {
		t.Errorf("emit error: err %v after %d calls", err, calls)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := parseGLC(ctx, f, nil, ingest.Window{}, emitNone); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: err = %v", err)
	}
}

// glcServer mimics data.nasa.gov: the catalog path answers 302 to a
// pre-signed S3 URL that serves the CSV.
type glcServer struct {
	body []byte
	mu   sync.Mutex
	reqs []string
}

const (
	glcTestPath = "/docs/legacy/Global_Landslide_Catalog_Export/Global_Landslide_Catalog_Export_rows.csv"
	glcTestS3   = "/s3/Global_Landslide_Catalog_Export_rows.csv"
)

func (s *glcServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.reqs = append(s.reqs, r.URL.Path)
	s.mu.Unlock()
	switch r.URL.Path {
	case glcTestPath:
		http.Redirect(w, r, glcTestS3+"?X-Amz-Signature=abc", http.StatusFound)
	case glcTestS3:
		w.Header().Set("Content-Type", "text/csv")
		_, _ = w.Write(s.body)
	default:
		http.NotFound(w, r)
	}
}

func (s *glcServer) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.reqs)
}

func TestGLCFetchEndToEnd(t *testing.T) {
	body, err := os.ReadFile(glcFixture)
	if err != nil {
		t.Fatal(err)
	}
	srv := &glcServer{body: body}
	ts := httptest.NewServer(srv)
	defer ts.Close()

	f := fetch.New(ts.Client(), t.TempDir(), quietLog())
	s := NewGLC(f, glcTestISO2, quietLog())
	s.URL = ts.URL + glcTestPath
	if s.Name() != "glc" {
		t.Errorf("Name = %q", s.Name())
	}
	if f.Limiter != nil || s.F == f || s.F.Limiter == nil {
		t.Error("constructor must pace a copy of the Fetcher, not the caller's")
	}

	var got []domain.Incident
	run := func(w ingest.Window) int {
		t.Helper()
		got = got[:0]
		if err := s.Fetch(context.Background(), w, func(inc domain.Incident) error { got = append(got, inc); return nil }); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		return len(got)
	}
	want := func(step string, paths ...string) {
		t.Helper()
		if r := srv.requests(); strings.Join(r, " ") != strings.Join(paths, " ") {
			t.Errorf("%s: requests = %v, want %v", step, r, paths)
		}
	}

	full := ingest.Window{From: d(1900, 1, 1), To: d(2026, 11, 15)}
	if n := run(full); n != 11 {
		t.Errorf("full run emitted %d, want 11", n)
	}
	if oso := findGLC(t, got, "5971"); !slices.Equal(oso.CountryHints, []string{"USA"}) {
		t.Errorf("oso hints = %v", oso.CountryHints)
	}
	// The 302 is followed; the file is cached under the catalog URL, so a
	// second run (any window) is served from the disk cache.
	if n := run(ingest.Window{From: d(2014, 1, 1), To: d(2015, 1, 1)}); n != 1 {
		t.Errorf("2014 run emitted %d, want 1", n)
	}
	want("two runs", glcTestPath, glcTestS3)

	// The global --refresh downloads it again.
	fr := fetch.New(ts.Client(), f.RawDir, quietLog())
	fr.Refresh = true
	sr := NewGLC(fr, nil, quietLog())
	sr.URL = s.URL
	if err := sr.Fetch(context.Background(), full, func(domain.Incident) error { return nil }); err != nil {
		t.Fatal(err)
	}
	want("--refresh", glcTestPath, glcTestS3, glcTestPath, glcTestS3)

	// An empty window does nothing.
	if n := run(ingest.Window{From: d(2013, 1, 1), To: d(2013, 1, 1)}); n != 0 || len(srv.requests()) != 4 {
		t.Errorf("empty window: emitted %d, requests %v", n, srv.requests())
	}

	// A missing file is an error.
	s.URL = ts.URL + "/nope"
	if err := s.Fetch(context.Background(), full, func(domain.Incident) error { return nil }); err == nil {
		t.Error("404: want an error")
	}
}
