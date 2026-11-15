package sources

import (
	"context"
	"errors"
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
	"github.com/dwts/backend/internal/platform"
)

// ibtracsFixture holds real rows of ibtracs.ALL.list.v04r01.csv (header,
// units row, then whole storms, in file order), fetched 2026-11-15:
//   - 1899178N28267 (11 fixes, SEASON 1899, 35 kt): below MIN_YEAR, skipped
//   - 2013306N07162 HAIYAN (75 fixes): WMO 125 kt (tokyo), USA 170 kt
//   - 2019063S18038 IDAI (101 fixes): WMO 105 kt (reunion), USA_SSHS max 4
//   - 2025339S14210 UNNAMED, SP, SEASON 2026, starts 2025-12-05 (11 fixes):
//     no WMO wind, USA 39 kt
//   - 2026070N10137 NURI (11 fixes): USA 29 kt, below 34 kt, skipped
//   - 2026117S30045 JULUKA (19 fixes): WMO 40 kt, no USA wind
//   - 2026117S30999: HAND-EDITED, fixes 5–7 of JULUKA with the SID changed
//     and TRACK_TYPE set to spur-merge (35/40 kt): a spur track, skipped
//   - 2026297N14177 OLANA (23 fixes): crosses 180° eastwards with LON
//     177.1 → 183.8 (BASIN WP → EP), USA 40 kt at LON 182.5
//
// 5 storms are emitted.
const ibtracsFixture = "../../../testdata/fixtures/ibtracs_sample.csv"

func parseIBTrACSFixture(t *testing.T, w ingest.Window) ([]domain.Incident, ibtracsStats) {
	t.Helper()
	f, err := os.Open(ibtracsFixture)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var got []domain.Incident
	st, err := parseIBTrACS(context.Background(), f, 1900, w, func(inc domain.Incident) error {
		got = append(got, inc)
		return nil
	})
	if err != nil {
		t.Fatalf("parseIBTrACS: %v", err)
	}
	if st.emitted != len(got) {
		t.Errorf("reported %d emitted, callback saw %d", st.emitted, len(got))
	}
	return got, st
}

func TestIBTrACSFixture(t *testing.T) {
	got, st := parseIBTrACSFixture(t, ingest.Window{})
	var ids []string
	for _, inc := range got {
		ids = append(ids, inc.SourceID)
	}
	want := "2013306N07162 2019063S18038 2025339S14210 2026117S30045 2026297N14177"
	if strings.Join(ids, " ") != want {
		t.Fatalf("emitted %v\nwant    %s", ids, want)
	}
	if st.storms != 8 || st.season != 1 || st.weak != 1 || st.spur != 1 || st.window != 0 || st.bad != 0 {
		t.Errorf("stats = %+v; want 8 storms, 1 season, 1 weak, 1 spur", st)
	}
	for i := range got {
		if got[i].Raw != nil {
			t.Errorf("%s: Raw must be nil (raw_hash is of the normalized summary, R-B.2)", got[i].SourceID)
		}
		if !ingest.Finalize(&got[i], 1900) {
			t.Errorf("Finalize rejected %s", got[i].SourceID)
		}
		if got[i].RawHash == "" {
			t.Errorf("%s: no raw hash", got[i].SourceID)
		}
	}

	// Haiyan: one agency for the whole storm (WMO, since it has WMO winds),
	// never the USA 170 kt.
	h := got[0]
	if h.Source != "ibtracs" || h.Type != domain.Storm || h.Title != "Typhoon HAIYAN (2013)" || h.Name != "HAIYAN" {
		t.Errorf("haiyan = %q %q %q %q", h.Source, h.Type, h.Title, h.Name)
	}
	checkTime(t, h, time.Date(2013, 11, 2, 6, 0, 0, 0, time.UTC), "second")
	if h.EndTime == nil || !h.EndTime.Equal(time.Date(2013, 11, 11, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("haiyan end = %v", h.EndTime)
	}
	if h.Lon != 129.1 || h.Lat != 10.2 {
		t.Errorf("haiyan point = %v %v, want the max-wind fix 129.1 10.2", h.Lon, h.Lat)
	}
	checkMag(t, h, f32(125), "kt")
	if h.Severity != 3 {
		t.Errorf("haiyan severity = %d", h.Severity)
	}
	checkProps(t, h, `{"basin":"WP","minPressureMb":895,"sshs":5,"windSource":"WMO_WIND"}`)
	if h.URL != "https://ncics.org/ibtracs/index.php?name=v04r01-2013306N07162" {
		t.Errorf("haiyan url = %q", h.URL)
	}
	if h.PlaceText != "Western North Pacific" {
		t.Errorf("haiyan place = %q", h.PlaceText)
	}
	if !strings.HasPrefix(h.TrackWKT, "LINESTRING(161.6 6.6,") || trackPoints(h.TrackWKT) != 38 {
		t.Errorf("haiyan track: %d points, %.40s…", trackPoints(h.TrackWKT), h.TrackWKT)
	}

	// Idai: WMO (reunion) 105 kt at its own fix; SSHS 4 → severity 3.
	i := got[1]
	if i.Title != "Cyclone IDAI (2019)" || i.Lon != 38.5 || i.Lat != -19.5 || i.Severity != 3 {
		t.Errorf("idai = %q %v %v sev %d", i.Title, i.Lon, i.Lat, i.Severity)
	}
	checkMag(t, i, f32(105), "kt")
	checkProps(t, i, `{"basin":"SI","minPressureMb":940,"sshs":4,"windSource":"WMO_WIND"}`)
	if trackPoints(i.TrackWKT) != 51 {
		t.Errorf("idai track points = %d, want 51", trackPoints(i.TrackWKT))
	}

	// Unnamed SP storm of SEASON 2026 that starts in Dec 2025: USA wind only.
	u := got[2]
	if u.Title != "Unnamed storm 2026 (SP)" || u.Name != "" || u.Year != 2025 {
		t.Errorf("unnamed = %q name %q year %d", u.Title, u.Name, u.Year)
	}
	if u.Lon != -148.8 || u.Lat != -15 || u.Severity != 1 {
		t.Errorf("unnamed point %v %v sev %d", u.Lon, u.Lat, u.Severity)
	}
	checkMag(t, u, f32(39), "kt")
	checkProps(t, u, `{"basin":"SP","minPressureMb":999,"sshs":0,"windSource":"USA_WIND"}`)

	// Juluka: WMO only, tropical-storm strength.
	j := got[3]
	if j.Title != "Tropical Storm JULUKA (2026)" {
		t.Errorf("juluka title = %q", j.Title)
	}
	checkMag(t, j, f32(40), "kt")
	checkProps(t, j, `{"basin":"SI","minPressureMb":984,"sshs":-5,"windSource":"WMO_WIND"}`)

	// Olana: longitudes > 180 are normalized, point and track.
	o := got[4]
	if o.Lon != -177.5 || o.Lat != 15 {
		t.Errorf("olana point = %v %v, want -177.5 15", o.Lon, o.Lat)
	}
	if o.Title != "Tropical Storm OLANA (2026)" {
		t.Errorf("olana title = %q", o.Title)
	}
	checkProps(t, o, `{"basin":"WP","minPressureMb":1000,"sshs":0,"windSource":"USA_WIND"}`)
	if trackPoints(o.TrackWKT) != 12 || !strings.Contains(o.TrackWKT, "-178.4 14.8") || strings.Contains(o.TrackWKT, " 181") {
		t.Errorf("olana track = %s", o.TrackWKT)
	}
}

func trackPoints(wkt string) int {
	if wkt == "" {
		return 0
	}
	return strings.Count(wkt, ",") + 1
}

func TestIBTrACSWindow(t *testing.T) {
	ids := func(w ingest.Window) string {
		got, _ := parseIBTrACSFixture(t, w)
		var s []string
		for _, inc := range got {
			s = append(s, inc.SourceID)
		}
		return strings.Join(s, " ")
	}
	// A storm counts when its [start, end] overlaps [From, To).
	if g := ids(ingest.Window{From: d(2013, 1, 1), To: d(2014, 1, 1)}); g != "2013306N07162" {
		t.Errorf("2013 = %q", g)
	}
	if g := ids(ingest.Window{From: d(2025, 1, 1), To: d(2026, 1, 1)}); g != "2025339S14210" {
		t.Errorf("2025 = %q", g)
	}
	// --since 30d on 2026-11-15
	if g := ids(ingest.Window{From: d(2026, 10, 16), To: d(2026, 11, 15)}); g != "2026297N14177" {
		t.Errorf("since = %q", g)
	}
	// A window that starts inside a storm still emits it.
	if g := ids(ingest.Window{From: time.Date(2013, 11, 11, 12, 0, 0, 0, time.UTC), To: d(2014, 1, 1)}); g != "2013306N07162" {
		t.Errorf("tail overlap = %q", g)
	}
	if g := ids(ingest.Window{From: time.Date(2013, 11, 11, 12, 0, 1, 0, time.UTC), To: d(2014, 1, 1)}); g != "" {
		t.Errorf("after end = %q", g)
	}
}

func TestIBTrACSTrack(t *testing.T) {
	fx := func(n int) []ibtracsFix {
		out := make([]ibtracsFix, n)
		for i := range out {
			out[i] = ibtracsFix{lon: float64(i), lat: 1}
		}
		return out
	}
	cases := []struct {
		n    int
		want string
	}{
		{1, ""},
		{2, "LINESTRING(0 1,1 1)"},
		{3, "LINESTRING(0 1,2 1)"},
		{4, "LINESTRING(0 1,2 1,3 1)"}, // every 2nd fix, plus the last
		{5, "LINESTRING(0 1,2 1,4 1)"},
	}
	for _, c := range cases {
		if got := ibtracsTrack(fx(c.n)); got != c.want {
			t.Errorf("track(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestIBTrACSHelpers(t *testing.T) {
	for _, c := range []struct {
		basin string
		kt    float64
		want  string
	}{
		{"NA", 150, "Hurricane"}, {"EP", 64, "Hurricane"}, {"WP", 64, "Typhoon"},
		{"NI", 70, "Cyclone"}, {"SI", 100, "Cyclone"}, {"SP", 90, "Cyclone"}, {"SA", 70, "Cyclone"},
		{"NA", 63, "Tropical Storm"}, {"WP", 34, "Tropical Storm"}, {"MM", 80, "Cyclone"},
	} {
		if got := ibtracsClass(c.basin, c.kt); got != c.want {
			t.Errorf("class(%s, %v) = %q, want %q", c.basin, c.kt, got, c.want)
		}
	}
	for in, want := range map[float64]float64{180: 180, 180.4: -179.6, 183.8: -176.2, 359.9: -0.1, -106: -106, 0: 0} {
		if got := normLon(in); got != want {
			t.Errorf("normLon(%v) = %v, want %v", in, got, want)
		}
	}
	for _, c := range []struct {
		max  int
		has  bool
		want domain.Severity
	}{{5, true, 3}, {3, true, 3}, {2, true, 2}, {1, true, 2}, {0, true, 1}, {-5, true, 1}, {0, false, 1}} {
		if got := ibtracsSeverity(c.max, c.has); got != c.want {
			t.Errorf("severity(%d,%v) = %d, want %d", c.max, c.has, got, c.want)
		}
	}
}

func TestIBTrACSParseErrors(t *testing.T) {
	head := "SID,SEASON,NUMBER,BASIN,SUBBASIN,NAME,ISO_TIME,NATURE,LAT,LON,WMO_WIND,WMO_PRES,WMO_AGENCY,TRACK_TYPE,USA_WIND,USA_PRES,USA_SSHS\n" +
		" ,Year, , , , , , ,degrees_north,degrees_east,kts,mb, , ,kts,mb,1\n"
	parse := func(s string) ([]domain.Incident, ibtracsStats, error) {
		var got []domain.Incident
		st, err := parseIBTrACS(context.Background(), strings.NewReader(s), 1900, ingest.Window{},
			func(inc domain.Incident) error { got = append(got, inc); return nil })
		return got, st, err
	}
	if _, _, err := parse(""); err == nil {
		t.Error("empty input: want an error")
	}
	if _, _, err := parse("SID,SEASON,NAME\n x,1,2\n"); err == nil || !strings.Contains(err.Error(), "ISO_TIME") {
		t.Errorf("missing columns: err = %v", err)
	}
	// A bad fix is dropped; a bad SEASON drops the storm; the rest goes on.
	got, st, err := parse(head +
		"A,2000,1,NA,MM,ONE,2000-08-01 00:00:00,TS,10,-50,40,1000, ,main,45,999,0\n" +
		"A,2000,1,NA,MM,ONE,garbage,TS,10,-50,90,1000, ,main,45,999,0\n" +
		"A,2000,1,NA,MM,ONE,2000-08-01 06:00:00,TS,x,-51,90,1000, ,main,45,999,0\n" +
		"A,2000,1,NA,MM,ONE,2000-08-01 12:00:00,TS,11,-52,50,990, ,main,45,999,1\n" +
		"B,yy,1,NA,MM,TWO,2000-09-01 00:00:00,TS,10,-50,80,1000, ,main,45,999,0\n" +
		"C,2000,1,NA,MM,NOT_NAMED,2000-10-01 00:00:00,TS,10,-50, , , ,main, , , \n")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SourceID != "A" || *got[0].Magnitude != 50 || got[0].Lon != -52 {
		t.Fatalf("got %+v", got)
	}
	if trackPoints(got[0].TrackWKT) != 2 {
		t.Errorf("track = %q", got[0].TrackWKT)
	}
	if st.bad != 1 || st.badFixes != 2 || st.weak != 1 {
		t.Errorf("stats = %+v; want bad 1, badFixes 2, weak 1", st)
	}

	// An emit error stops the parse and comes back as is.
	stop := errors.New("stop")
	if _, err := parseIBTrACS(context.Background(), strings.NewReader(head+
		"A,2000,1,NA,MM,ONE,2000-08-01 00:00:00,TS,10,-50,40,1000, ,main,45,999,0\n"), 1900, ingest.Window{},
		func(domain.Incident) error { return stop }); !errors.Is(err, stop) {
		t.Errorf("emit err = %v", err)
	}
	// A cancelled context stops it too.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := parseIBTrACS(ctx, strings.NewReader(head+
		"A,2000,1,NA,MM,ONE,2000-08-01 00:00:00,TS,10,-50,40,1000, ,main,45,999,0\n"), 1900, ingest.Window{},
		func(domain.Incident) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled err = %v", err)
	}
}

// ibtracsServer serves the fixture as both the ALL and the last3years file
// and records the request paths.
type ibtracsServer struct {
	body []byte
	mu   sync.Mutex
	reqs []string
}

func (s *ibtracsServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.reqs = append(s.reqs, r.URL.Path)
	s.mu.Unlock()
	switch r.URL.Path {
	case "/" + ibtracsAllFile, "/" + ibtracsRecentFile:
		w.Header().Set("Content-Type", "text/csv")
		_, _ = w.Write(s.body)
	default:
		http.NotFound(w, r)
	}
}

func (s *ibtracsServer) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.reqs...)
}

func TestIBTrACSFetchEndToEnd(t *testing.T) {
	body, err := os.ReadFile(ibtracsFixture)
	if err != nil {
		t.Fatal(err)
	}
	srv := &ibtracsServer{body: body}
	ts := httptest.NewServer(srv)
	defer ts.Close()

	f := fetch.New(ts.Client(), t.TempDir(), quietLog())
	s := NewIBTrACS(f, 1900, quietLog())
	s.BaseURL = ts.URL + "/"
	s.Now = func() time.Time { return time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC) }
	if s.Name() != "ibtracs" {
		t.Errorf("Name = %q", s.Name())
	}
	if f.Limiter != nil || s.F == f || s.F.Limiter == nil {
		t.Errorf("constructor must pace a copy of the Fetcher, not the caller's")
	}

	run := func(w ingest.Window) int {
		t.Helper()
		n := 0
		if err := s.Fetch(context.Background(), w, func(domain.Incident) error { n++; return nil }); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		return n
	}
	all, recent := "/"+ibtracsAllFile, "/"+ibtracsRecentFile
	want := func(step string, paths ...string) {
		t.Helper()
		if got := srv.requests(); strings.Join(got, " ") != strings.Join(paths, " ") {
			t.Errorf("%s: requests = %v, want %v", step, got, paths)
		}
	}

	// Full runs read the ALL file, downloaded once (Guide M8 "cached once").
	full := ingest.Window{From: d(1900, 1, 1), To: s.Now()}
	if n := run(full); n != 5 {
		t.Errorf("full run emitted %d, want 5", n)
	}
	if n := run(full); n != 5 {
		t.Errorf("second full run emitted %d, want 5", n)
	}
	want("two full runs", all)

	// --since 30d reads last3years, downloaded again on every run, never ALL.
	since := ingest.Window{From: d(2026, 10, 16), To: s.Now()}
	for range 2 {
		if n := run(since); n != 1 {
			t.Errorf("since run emitted %d, want 1 (OLANA)", n)
		}
	}
	want("two since runs", all, recent, recent)
	if s.F.Refresh {
		t.Error("Fetch left Refresh set on the adapter's Fetcher")
	}

	// The boundary: From in Now.Year()-2 is recent, Now.Year()-3 is not.
	if n := run(ingest.Window{From: d(2024, 1, 1), To: s.Now()}); n != 3 {
		t.Errorf("2024→now emitted %d, want 3", n)
	}
	if n := run(ingest.Window{From: d(2023, 12, 31), To: s.Now()}); n != 3 {
		t.Errorf("2023-12-31→now emitted %d, want 3", n)
	}
	// A closed past window reads the cached ALL file.
	if n := run(ingest.Window{From: d(2013, 1, 1), To: d(2014, 1, 1)}); n != 1 {
		t.Errorf("2013 run emitted %d, want 1", n)
	}
	want("boundary + 2013", all, recent, recent, recent)

	// The global --refresh (Refresh on the caller's Fetcher) downloads ALL again.
	fr := fetch.New(ts.Client(), f.RawDir, quietLog())
	fr.Refresh = true
	sr := NewIBTrACS(fr, 1900, quietLog())
	sr.BaseURL, sr.Now = ts.URL, s.Now
	if err := sr.Fetch(context.Background(), full, func(domain.Incident) error { return nil }); err != nil {
		t.Fatal(err)
	}
	want("--refresh", all, recent, recent, recent, all)

	// An empty window does nothing.
	if n := run(ingest.Window{From: d(2013, 1, 1), To: d(2013, 1, 1)}); n != 0 || len(srv.requests()) != 5 {
		t.Errorf("empty window: emitted %d, requests %v", n, srv.requests())
	}

	// A missing file is an error.
	s.BaseURL = ts.URL + "/nope"
	if err := s.Fetch(context.Background(), since, func(domain.Incident) error { return nil }); err == nil {
		t.Error("404: want an error")
	}
}

func TestNewIBTrACSTimeout(t *testing.T) {
	// The whole 333 MB body must fit in the client timeout: a short one is
	// raised on the adapter's copy only.
	hc := platform.NewHTTPClient(5 * time.Minute)
	f := fetch.New(hc, t.TempDir(), quietLog())
	s := NewIBTrACS(f, 1900, nil)
	if s.F.HC == hc || s.F.HC.Timeout != ibtracsTimeout || hc.Timeout != 5*time.Minute {
		t.Errorf("timeout: adapter %v, caller %v", s.F.HC.Timeout, hc.Timeout)
	}
	// No timeout at all is kept as is.
	f2 := fetch.New(&http.Client{}, t.TempDir(), quietLog())
	if s2 := NewIBTrACS(f2, 1900, nil); s2.F.HC.Timeout != 0 {
		t.Errorf("no-timeout client changed to %v", s2.F.HC.Timeout)
	}
	// A nil client gets one with the long timeout.
	f3 := fetch.New(nil, t.TempDir(), quietLog())
	if s3 := NewIBTrACS(f3, 1900, nil); s3.F.HC == nil || s3.F.HC.Timeout != ibtracsTimeout {
		t.Errorf("nil client: %v", s3.F.HC)
	}
}

func TestIBTrACSStrayQuote(t *testing.T) {
	// LazyQuotes: a bare quote inside a field does not abort the whole file.
	in := "SID,SEASON,NUMBER,BASIN,SUBBASIN,NAME,ISO_TIME,NATURE,LAT,LON,WMO_WIND,WMO_PRES,WMO_AGENCY,TRACK_TYPE,USA_WIND,USA_PRES,USA_SSHS\n" +
		" ,Year, , , , , , ,degrees_north,degrees_east,kts,mb, , ,kts,mb,1\n" +
		"A,2000,1,NA,MM,O\"NEIL,2000-08-01 00:00:00,TS,10,-50,40,1000, ,main,45,999,0\n"
	var got []domain.Incident
	if _, err := parseIBTrACS(context.Background(), strings.NewReader(in), 1900, ingest.Window{},
		func(inc domain.Incident) error { got = append(got, inc); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != `O"NEIL` {
		t.Errorf("got %+v", got)
	}
}
