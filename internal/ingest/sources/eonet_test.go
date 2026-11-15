package sources

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dwts/backend/internal/domain"
	"github.com/dwts/backend/internal/ingest"
	"github.com/dwts/backend/internal/ingest/fetch"
)

// eonetFixture is a real EONET v3 /events response, trimmed to six events
// taken from the 2023, 2025 and 2026 yearly responses (fetched 2026-11-15)
// and re-serialized with indent=1:
//   - EONET_6412 Hurricane Idalia (35 Point fixes in kts) first,
//   - EONET_6563 Tiger Island wildfire (IRWIN, acres, a description),
//   - EONET_6471 Heard volcano (null magnitude, midnight date),
//   - EONET_4089 Iceberg C36 (open: closed null; a null then an NM^2 fix),
//   - EONET_25820 Flood in Germany (one Polygon geometry),
//   - EONET_16759 Wildfire in Sudan (GDACS, hectare).
//
// Two rows are HAND-EDITED copies of the volcano: EONET_999001 with an empty
// geometry array (must be skipped) and EONET_999002 with the category
// changed to "dustHaze" (must map to type other). 7 usable rows.
const eonetFixture = "../../../testdata/fixtures/eonet.json"

func parseEONETFixture(t *testing.T) ([]domain.Incident, eonetStats) {
	t.Helper()
	f, err := os.Open(eonetFixture)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var got []domain.Incident
	st, err := parseEONET(f, func(inc domain.Incident) error {
		got = append(got, inc)
		return nil
	})
	if err != nil {
		t.Fatalf("parseEONET: %v", err)
	}
	if st.emitted != len(got) {
		t.Errorf("reported %d emitted, callback saw %d", st.emitted, len(got))
	}
	for i := range got {
		if !ingest.Finalize(&got[i], 1900) {
			t.Errorf("Finalize rejected %s", got[i].SourceID)
		}
	}
	return got, st
}

func findEONET(t *testing.T, got []domain.Incident, id string) domain.Incident {
	t.Helper()
	for _, inc := range got {
		if inc.SourceID == id {
			return inc
		}
	}
	t.Fatalf("source id %s not emitted", id)
	return domain.Incident{}
}

func TestEONETFixture(t *testing.T) {
	got, st := parseEONETFixture(t)
	if len(got) != 7 || st.skipped != 1 {
		t.Fatalf("emitted %d, skipped %d; want 7, 1", len(got), st.skipped)
	}
	for _, inc := range got {
		if inc.SourceID == "EONET_999001" {
			t.Errorf("event without geometry was emitted")
		}
	}

	// Hurricane Idalia: first Point, a track of all 35 fixes, max kts.
	inc := got[0]
	if inc.Source != "eonet" || inc.SourceID != "EONET_6412" || inc.Type != domain.Storm {
		t.Errorf("key = %s/%s %s", inc.Source, inc.SourceID, inc.Type)
	}
	if inc.Title != "Hurricane Idalia" || inc.Description != "" {
		t.Errorf("title/description = %q/%q", inc.Title, inc.Description)
	}
	checkTime(t, inc, time.Date(2023, 8, 27, 15, 15, 0, 0, time.UTC), "second")
	if inc.EndTime == nil || !inc.EndTime.Equal(d(2023, 9, 6)) {
		t.Errorf("end = %v, want 2023-09-06 (closed)", inc.EndTime)
	}
	if inc.Lon != -85.8 || inc.Lat != 19.9 {
		t.Errorf("lon/lat = %v/%v", inc.Lon, inc.Lat)
	}
	checkMag(t, inc, f32(115), "kts")
	if inc.Severity != 3 {
		t.Errorf("severity = %d, want 3 (115 kt)", inc.Severity)
	}
	if want := "https://www.gdacs.org/report.aspx?eventid=1001000&episodeid=7&eventtype=TC"; inc.URL != want {
		t.Errorf("url = %q", inc.URL)
	}
	if !strings.HasPrefix(inc.TrackWKT, "LINESTRING(-85.8 19.9,") || !strings.HasSuffix(inc.TrackWKT, ",-71.5 33.1)") ||
		strings.Count(inc.TrackWKT, ",") != 34 {
		t.Errorf("track = %q, want all 35 fixes from -85.8 19.9 to -71.5 33.1", inc.TrackWKT)
	}
	checkProps(t, inc, `{"category":"severeStorms","geometries":35,`+
		`"link":"https://eonet.gsfc.nasa.gov/api/v3/events/EONET_6412","sources":["GDACS","NOAA_NHC"]}`)
	var raw struct{ ID string }
	if err := json.Unmarshal(inc.Raw, &raw); err != nil || raw.ID != "EONET_6412" {
		t.Errorf("raw is not the event JSON: %v", err)
	}
	if inc.Year != 2023 || inc.RawHash == "" {
		t.Errorf("finalized year/hash = %d/%q", inc.Year, inc.RawHash)
	}

	// IRWIN wildfire: acres, a description, a single point → no track.
	fire := findEONET(t, got, "EONET_6563")
	if fire.Type != domain.Wildfire || fire.TrackWKT != "" || fire.Severity != 1 {
		t.Errorf("fire = %s track %q severity %d", fire.Type, fire.TrackWKT, fire.Severity)
	}
	checkMag(t, fire, f32(31290), "acres")
	checkTime(t, fire, time.Date(2023, 8, 22, 15, 51, 0, 0, time.UTC), "second")
	if !strings.HasPrefix(fire.Description, "E of Merryville, LA") {
		t.Errorf("description = %q", fire.Description)
	}

	// Volcano: null magnitude, a midnight date → precision day.
	vol := findEONET(t, got, "EONET_6471")
	if vol.Type != domain.Volcano || vol.Severity != 1 {
		t.Errorf("volcano = %s severity %d", vol.Type, vol.Severity)
	}
	checkMag(t, vol, nil, "")
	checkTime(t, vol, d(2023, 12, 29), "day")
	if vol.URL != "https://volcano.si.edu/volcano.cfm?vn=234010" {
		t.Errorf("volcano url = %q", vol.URL)
	}

	// Iceberg: still open (no end), seaLakeIce → other, the unit of the
	// only non-null magnitude.
	ice := findEONET(t, got, "EONET_4089")
	if ice.Type != domain.Other || ice.EndTime != nil {
		t.Errorf("iceberg = %s end %v", ice.Type, ice.EndTime)
	}
	checkMag(t, ice, f32(368), "NM^2")
	checkTime(t, ice, d(2019, 2, 11), "day")
	if ice.TrackWKT != "LINESTRING(147.83 -68.13,147.04 -67.76)" {
		t.Errorf("iceberg track = %q", ice.TrackWKT)
	}

	// Flood polygon: the point is the mean of the ring's distinct vertices
	// (the closing vertex repeats the first and is left out).
	fl := findEONET(t, got, "EONET_25820")
	if fl.Type != domain.Flood || fl.TrackWKT != "" {
		t.Errorf("flood = %s track %q", fl.Type, fl.TrackWKT)
	}
	wantLon := (7.4086637 + 7.5000001 + 7.549833 + 7.4484651 + 7.408728) / 5
	wantLat := (54.0068062 + 54.0197601 + 54.0268 + 54.1378463 + 54.1357002) / 5
	if math.Abs(fl.Lon-wantLon) > 1e-9 || math.Abs(fl.Lat-wantLat) > 1e-9 {
		t.Errorf("flood centroid = %v/%v, want %v/%v", fl.Lon, fl.Lat, wantLon, wantLat)
	}
	checkTime(t, fl, time.Date(2026, 10, 25, 20, 0, 0, 0, time.UTC), "second")

	sudan := findEONET(t, got, "EONET_16759")
	checkMag(t, sudan, f32(5492), "hectare")
	if sudan.Type != domain.Wildfire {
		t.Errorf("sudan type = %s", sudan.Type)
	}

	if o := findEONET(t, got, "EONET_999002"); o.Type != domain.Other {
		t.Errorf("dustHaze type = %s, want other", o.Type)
	}
}

func TestEONETType(t *testing.T) {
	for id, want := range map[string]domain.IncidentType{
		"wildfires": domain.Wildfire, "severeStorms": domain.Storm, "volcanoes": domain.Volcano,
		"floods": domain.Flood, "landslides": domain.Landslide, "earthquakes": domain.Earthquake,
		"drought": domain.Drought, "seaLakeIce": domain.Other, "snow": domain.Other, "": domain.Other,
	} {
		if got := eonetType(id); got != want {
			t.Errorf("eonetType(%q) = %s, want %s", id, got, want)
		}
	}
}

func TestEONETSeverity(t *testing.T) {
	fp := func(v float64) *float64 { return &v }
	tests := []struct {
		typ  domain.IncidentType
		mag  *float64
		unit string
		want domain.Severity
	}{
		{domain.Storm, fp(96), "kts", 3},
		{domain.Storm, fp(95), "kts", 2},
		{domain.Storm, fp(64), "kts", 2},
		{domain.Storm, fp(63), "kts", 1},
		{domain.Storm, nil, "", 1},
		{domain.Wildfire, fp(500000), "acres", 1},
	}
	for _, tc := range tests {
		if got := eonetSeverity(tc.typ, tc.mag, tc.unit); got != tc.want {
			t.Errorf("eonetSeverity(%s, %v, %s) = %d, want %d", tc.typ, tc.mag, tc.unit, got, tc.want)
		}
	}
}

func TestEONETParseEdgeCases(t *testing.T) {
	// No categories, a bad date, a Polygon before a Point (the Point wins),
	// non-Point geometries do not make a track, no sources → the API link.
	body := `{"title":"x","events":[
{"id":"A","title":"no categories","categories":[],"sources":[],"link":"L","geometry":[{"date":"2020-01-02T03:04:05Z","type":"Point","coordinates":[1,2]}]},
{"id":"B","title":"bad date","categories":[{"id":"floods"}],"geometry":[{"date":"yesterday","type":"Point","coordinates":[1,2]}]},
{"id":"C","title":"poly then point","categories":[{"id":"floods"}],"closed":null,"geometry":[
 {"date":"2020-01-01T00:00:00Z","type":"Polygon","coordinates":[[[0,0],[2,0],[2,2],[0,2],[0,0]]]},
 {"date":"2020-01-03T00:00:00Z","type":"Point","coordinates":[10,20]}]},
{"id":"D","title":"poly only","categories":[{"id":"floods"}],"geometry":[
 {"date":"2020-01-01T00:00:00Z","type":"Polygon","coordinates":[[[0,0],[2,0],[2,2],[0,2]]]},
 {"date":"2020-01-02T00:00:00Z","type":"Polygon","coordinates":[[[50,50],[51,50],[51,51]]]}]},
{"id":"E","title":"short point","categories":[{"id":"floods"}],"geometry":[{"date":"2020-01-01T00:00:00Z","type":"Point","coordinates":[1]}]},
{"title":"no id","categories":[{"id":"floods"}],"geometry":[{"date":"2020-01-01T00:00:00Z","type":"Point","coordinates":[1,2]}]}
]}`
	var got []domain.Incident
	st, err := parseEONET(strings.NewReader(body), func(inc domain.Incident) error {
		got = append(got, inc)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if st.events != 6 || st.emitted != 3 || st.skipped != 3 || len(got) != 3 {
		t.Fatalf("stats = %+v, got %d", st, len(got))
	}
	if got[0].Type != domain.Other || got[0].URL != "L" {
		t.Errorf("A = %s %q", got[0].Type, got[0].URL)
	}
	if c := got[1]; c.Lon != 10 || c.Lat != 20 || c.TrackWKT != "" || !c.StartTime.Equal(d(2020, 1, 1)) {
		t.Errorf("C = %v/%v track %q start %v", c.Lon, c.Lat, c.TrackWKT, c.StartTime)
	}
	if dd := got[2]; dd.Lon != 1 || dd.Lat != 1 {
		t.Errorf("D centroid = %v/%v, want 1/1 (an unclosed ring keeps every vertex)", dd.Lon, dd.Lat)
	}
}

func TestEONETParseErrors(t *testing.T) {
	for _, body := range []string{``, `[1]`, `{"events":{}}`, `{"title":"x"}`, `{"events":[1]}`, `{"events":[{"id":`} {
		if _, err := parseEONET(strings.NewReader(body), func(domain.Incident) error { return nil }); err == nil {
			t.Errorf("parseEONET(%q) = nil error", body)
		}
	}
	f, err := os.Open(eonetFixture)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	boom := errors.New("boom")
	if _, err := parseEONET(f, func(domain.Incident) error { return boom }); !errors.Is(err, boom) {
		t.Errorf("emit error = %v, want boom", err)
	}
}

// eonetServer serves the fixture for every request and records each
// request's path and query.
type eonetServer struct {
	mu   sync.Mutex
	body []byte
	reqs []string
}

func (s *eonetServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reqs = append(s.reqs, r.URL.Path+"?"+r.URL.RawQuery)
	w.Write(s.body)
}

func (s *eonetServer) take() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.reqs
	s.reqs = nil
	return r
}

func TestEONETFetchEndToEnd(t *testing.T) {
	body, err := os.ReadFile(eonetFixture)
	if err != nil {
		t.Fatal(err)
	}
	srv := &eonetServer{body: body}
	ts := httptest.NewServer(srv)
	defer ts.Close()

	hc := ts.Client()
	hc.Timeout = 5 * time.Minute // cmd/ingest's default
	f := fetch.New(hc, t.TempDir(), quietLog())
	s := NewEONET(f, quietLog())
	s.BaseURL = ts.URL + "/api/v3/events"
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }
	if s.Name() != "eonet" {
		t.Errorf("Name = %q", s.Name())
	}
	if f.Limiter != nil || s.F == f || s.F.Limiter == nil {
		t.Errorf("constructor must pace a copy of the Fetcher, not the caller's")
	}
	if s.F.HC == nil || s.F.HC.Timeout != eonetTimeout {
		t.Errorf("adapter client timeout = %v, want %v", s.F.HC.Timeout, eonetTimeout)
	}
	if f.HC.Timeout != 5*time.Minute {
		t.Errorf("constructor changed the caller's client")
	}

	var n int
	count := func(domain.Incident) error { n++; return nil }

	// --from 2023: yearly windows 2023, 2024, 2025 and 2026. A window that
	// reaches now asks for its whole last year (end=2026-12-31, the Guide's
	// URL), so the cache key does not change every day.
	w := ingest.Window{From: d(2023, 1, 1), To: now}
	if err := s.Fetch(context.Background(), w, count); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	want := []string{
		"/api/v3/events?status=all&start=2023-01-01&end=2023-12-31",
		"/api/v3/events?status=all&start=2024-01-01&end=2024-12-31",
		"/api/v3/events?status=all&start=2025-01-01&end=2025-12-31",
		"/api/v3/events?status=all&start=2026-01-01&end=2026-12-31",
	}
	if got := srv.take(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if n != 4*7 {
		t.Errorf("emitted %d, want %d (7 per window)", n, 4*7)
	}

	// Second run: 2023 and 2024 come from the cache; last year and the
	// current year (whose events may still change) are downloaded again.
	if err := s.Fetch(context.Background(), w, count); err != nil {
		t.Fatal(err)
	}
	if got := srv.take(); strings.Join(got, "\n") != strings.Join(want[2:], "\n") {
		t.Errorf("second run requests = %v, want %v", got, want[2:])
	}
	if s.F.Refresh {
		t.Errorf("Fetch left Refresh set on the adapter's Fetcher")
	}

	// --since 30d crossing no year boundary: one window, its start clipped.
	if err := s.Fetch(context.Background(), ingest.Window{From: now.Add(-30 * 24 * time.Hour), To: now}, count); err != nil {
		t.Fatal(err)
	}
	if got := srv.take(); len(got) != 1 || got[0] != "/api/v3/events?status=all&start=2026-10-16&end=2026-12-31" {
		t.Errorf("--since requests = %v", got)
	}

	// --from 2010 --to 2010: [2010-01-01, 2011-01-01) is one closed year.
	if err := s.Fetch(context.Background(), ingest.Window{From: d(2010, 1, 1), To: d(2011, 1, 1)}, count); err != nil {
		t.Fatal(err)
	}
	if got := srv.take(); len(got) != 1 || got[0] != "/api/v3/events?status=all&start=2010-01-01&end=2010-12-31" {
		t.Errorf("--from/--to 2010 requests = %v", got)
	}
	// cmd/ingest sets To = time.Now() before the run, so the adapter's own
	// clock is a little later: still a window that reaches now.
	if err := s.Fetch(context.Background(), ingest.Window{From: d(2026, 10, 1), To: now.Add(-3 * time.Minute)}, count); err != nil {
		t.Fatal(err)
	}
	if got := srv.take(); len(got) != 1 || got[0] != "/api/v3/events?status=all&start=2026-10-01&end=2026-12-31" {
		t.Errorf("To slightly before now: requests = %v", got)
	}
	// A window ending before today inside a year (a test-only shape) is
	// clipped to its last day.
	if err := s.Fetch(context.Background(), ingest.Window{From: d(2015, 3, 1), To: d(2015, 6, 1)}, count); err != nil {
		t.Fatal(err)
	}
	if got := srv.take(); len(got) != 1 || got[0] != "/api/v3/events?status=all&start=2015-03-01&end=2015-05-31" {
		t.Errorf("closed partial window requests = %v", got)
	}
	// Empty window: no requests.
	if err := s.Fetch(context.Background(), ingest.Window{From: now, To: now}, count); err != nil {
		t.Fatal(err)
	}
	if got := srv.take(); len(got) != 0 {
		t.Errorf("empty window requests = %v", got)
	}
}

func TestEONETCoordinateRepairs(t *testing.T) {
	// Real patterns from the 2000→2026 run: [lat, lon] order (EONET_3014,
	// a BC fire, and 46 GDACS flood polygons of 2026), an impossible first
	// fix (EONET_3660 Super Typhoon Maria: [151.1, 93.0]) and IRWIN points
	// that are garbage either way (EONET_20386: [189, 7]).
	body := `{"events":[
{"id":"F","title":"swapped point","categories":[{"id":"wildfires"}],"geometry":[{"date":"2017-07-07T00:00:00Z","type":"Point","coordinates":[52.8108167,-124.02195]}]},
{"id":"G","title":"bad first fix","categories":[{"id":"severeStorms"}],"geometry":[
 {"date":"2018-07-02T00:00:00Z","type":"Point","coordinates":[151.1,93.0]},
 {"date":"2018-07-02T06:00:00Z","type":"Point","coordinates":[150.4,9.6]},
 {"date":"2018-07-02T12:00:00Z","type":"Point","coordinates":[149.7,10.1]}]},
{"id":"H","title":"swapped polygon","categories":[{"id":"floods"}],"geometry":[{"date":"2026-10-04T00:00:00Z","type":"Polygon","coordinates":[[[16,98],[17,98],[17,99],[16,98]]]}]},
{"id":"I","title":"garbage point","categories":[{"id":"wildfires"}],"geometry":[{"date":"2026-06-04T00:00:00Z","type":"Point","coordinates":[189,7]}]}
]}`
	var got []domain.Incident
	st, err := parseEONET(strings.NewReader(body), func(inc domain.Incident) error {
		got = append(got, inc)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if st.emitted != 3 || st.skipped != 1 {
		t.Fatalf("stats = %+v, want 3 emitted, 1 skipped", st)
	}
	f := got[0]
	if f.Lon != -124.02195 || f.Lat != 52.8108167 || f.Props["swappedLatLon"] != true {
		t.Errorf("F = %v/%v props %v", f.Lon, f.Lat, f.Props)
	}
	g := got[1]
	if g.Lon != 150.4 || g.Lat != 9.6 || g.TrackWKT != "LINESTRING(150.4 9.6,149.7 10.1)" {
		t.Errorf("G = %v/%v track %q", g.Lon, g.Lat, g.TrackWKT)
	}
	if !g.StartTime.Equal(d(2018, 7, 2)) || g.Props["swappedLatLon"] != nil {
		t.Errorf("G start %v props %v (start stays the first geometry date)", g.StartTime, g.Props)
	}
	h := got[2]
	if math.Abs(h.Lon-(98+98+99)/3.0) > 1e-9 || math.Abs(h.Lat-(16+17+17)/3.0) > 1e-9 || h.Props["swappedLatLon"] != true {
		t.Errorf("H = %v/%v props %v", h.Lon, h.Lat, h.Props)
	}
	for i := range got {
		if !ingest.Finalize(&got[i], 1900) {
			t.Errorf("Finalize rejected %s", got[i].SourceID)
		}
	}
}

func TestEONETGDACSClockwisePolygons(t *testing.T) {
	// GDACS writes its flood polygons counter-clockwise (RFC 7946). From
	// late June to early October 2026 EONET served them as [lat, lon],
	// which turns the ring clockwise: 171 of the 244 GDACS polygons, most of
	// them in range both ways (Finland [60.5, 24.7] would land in Iran).
	// A clockwise GDACS shape is read swapped; a CCW one, a non-GDACS one
	// and an axis-aligned box (the 2015–2018 hand-drawn EO boxes are all
	// clockwise and correct) are kept as given.
	cw := `[[[60,24],[61,25],[62,24],[61,23],[60,24]]]`  // (x,y) clockwise, a diamond
	ccw := `[[[24,60],[25,61],[24,62],[23,61],[24,60]]]` // bottom, right, top, left
	box := `[[[30,10],[30,12],[32,12],[32,10],[30,10]]]` // clockwise box
	gdacs := `[{"id":"GDACS","url":"https://www.gdacs.org/report.aspx?eventtype=FL&eventid=1104008"}]`
	other := `[{"id":"EO","url":"https://earthobservatory.nasa.gov/"}]`
	ev := func(id, src, ring string) string {
		return `{"id":"` + id + `","title":"t","categories":[{"id":"floods"}],"sources":` + src +
			`,"geometry":[{"date":"2026-07-04T20:00:00Z","type":"Polygon","coordinates":` + ring + `}]}`
	}
	body := `{"events":[` + strings.Join([]string{
		ev("cw", gdacs, cw), ev("ccw", gdacs, ccw), ev("eo", other, cw), ev("box", gdacs, box),
	}, ",") + `]}`
	var got []domain.Incident
	if _, err := parseEONET(strings.NewReader(body), func(inc domain.Incident) error {
		got = append(got, inc)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("emitted %d, want 4", len(got))
	}
	want := []struct {
		lon, lat float64
		swapped  bool
	}{{24, 61, true}, {24, 61, false}, {61, 24, false}, {31, 11, false}}
	for i, w := range want {
		g := got[i]
		if math.Abs(g.Lon-w.lon) > 1e-9 || math.Abs(g.Lat-w.lat) > 1e-9 || (g.Props["swappedLatLon"] == true) != w.swapped {
			t.Errorf("%s = %v/%v swapped %v, want %v/%v swapped %v", g.SourceID, g.Lon, g.Lat, g.Props["swappedLatLon"], w.lon, w.lat, w.swapped)
		}
	}
}

func TestEONETUnescapesHTMLEntities(t *testing.T) {
	// 108 real titles carry HTML entities (EONET_918, EONET_13375).
	body := `{"events":[{"id":"EONET_918","title":"Hunga Tonga-Hunga Ha&#039;apai Volcano, Tonga",
"description":"Bosnia &amp; Herzegovina","categories":[{"id":"volcanoes"}],
"geometry":[{"date":"2014-12-19T00:00:00Z","type":"Point","coordinates":[-175.382,-20.536]}]}]}`
	var got []domain.Incident
	if _, err := parseEONET(strings.NewReader(body), func(inc domain.Incident) error {
		got = append(got, inc)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "Hunga Tonga-Hunga Ha'apai Volcano, Tonga" || got[0].Description != "Bosnia & Herzegovina" {
		t.Errorf("got %+v", got)
	}
}
