package sources

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/dwts/backend/internal/domain"
	"github.com/dwts/backend/internal/ingest"
	"github.com/dwts/backend/internal/ingest/fetch"
)

// dfoFixture holds 20 real features of the DFO archive FeatureServer
// (fetched 2026-11-15, all 6 pages, then picked by hand). The response
// envelope is the real one with its long "fields" array removed; the
// features are unchanged. They cover: 1 (the first, Algeria 1985), 51
// ("Viet Nam"), 132 ("Chinan": a stray letter), 206 ("South Korea i"), 215
// ("USSR": no country today, DEAD 0), the two features with ID 278 (FID 275
// and 354), 490 ("Britain, Ireland"), 1414 (OTHERCOUNT " Uzbekistan"), 1480
// (MAINCAUSE "0"), 1494 ("0Peru"), 1569 (Democratic Republic of Congo +
// Congo), 2369 ("Canada and USA"), 3487 ("Cote d'Ivoire", a GLIDE number),
// 3696 (the 2010 Pakistan floods), 3829 ("Ivory Coast"), 4081
// ("Burma/Myanmar" + Thailand, SEVERITY 1.5), the two features with ID 4842
// (Zambia FID 4840, Mozambique FID 4841) and 5132 (LONG/LAT 0,0, AREA 0).
const dfoFixture = "../../../testdata/fixtures/dfo.json"

// dfoTestNames stands in for the map cmd/ingest loads from countries.name
// (Natural Earth NAME spellings, as stored).
var dfoTestNames = map[string]string{
	"Algeria": "DZA", "China": "CHN", "South Korea": "KOR", "Indonesia": "IDN", "United Kingdom": "GBR",
	"Ireland": "IRL", "Kyrgyzstan": "KGZ", "Uzbekistan": "UZB", "Sri Lanka": "LKA", "Peru": "PER",
	"Dem. Rep. Congo": "COD", "Congo": "COG", "Canada": "CAN", "United States of America": "USA",
	"Côte d'Ivoire": "CIV", "Pakistan": "PAK", "Myanmar": "MMR", "Thailand": "THA", "Zambia": "ZMB",
	"Mozambique": "MOZ", "Vietnam": "VNM", "Niger": "NER", "Nigeria": "NGA", "Bosnia and Herz.": "BIH",
	"Serbia": "SRB", "Montenegro": "MNE", "S. Sudan": "SDS",
}

func parseDFOFixture(t *testing.T, names map[string]string, w ingest.Window) ([]domain.Incident, *dfoState, dfoPage) {
	t.Helper()
	f, err := os.Open(dfoFixture)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	st := newDFOState(names)
	var got []domain.Incident
	pg, err := parseDFOPage(context.Background(), f, st, w, func(inc domain.Incident) error {
		got = append(got, inc)
		return nil
	})
	if err != nil {
		t.Fatalf("parseDFOPage: %v", err)
	}
	if st.emitted != len(got) {
		t.Errorf("reported %d emitted, callback saw %d", st.emitted, len(got))
	}
	return got, st, pg
}

func findDFO(t *testing.T, got []domain.Incident, id string) domain.Incident {
	t.Helper()
	for _, inc := range got {
		if inc.SourceID == id {
			return inc
		}
	}
	t.Fatalf("event %s not emitted", id)
	return domain.Incident{}
}

func TestDFOFixture(t *testing.T) {
	got, st, pg := parseDFOFixture(t, dfoTestNames, ingest.Window{})
	var ids []string
	for _, inc := range got {
		ids = append(ids, inc.SourceID)
	}
	want := "1 51 132 206 215 278 278-354 490 1414 1480 1494 1569 2369 3487 3696 3829 4081 4842 4842-4841"
	if strings.Join(ids, " ") != want {
		t.Fatalf("emitted %v\nwant    %s", ids, want)
	}
	if pg.features != 20 || pg.exceeded {
		t.Errorf("page = %+v, want 20 features and no exceededTransferLimit", pg)
	}
	if st.rows != 20 || st.noLocation != 1 || st.dupIDs != 2 || st.hinted != 18 || st.window != 0 || st.noID != 0 || st.badTime != 0 {
		t.Errorf("stats = %+v", *st)
	}
	if len(st.unmatched) != 1 || st.unmatched["USSR"] != 1 {
		t.Errorf("unmatched = %v, want USSR×1", st.unmatched)
	}
	for i := range got {
		inc := &got[i]
		if inc.Source != "dfo" || inc.Type != domain.Flood || inc.TimePrecision != "day" || len(inc.Raw) == 0 {
			t.Errorf("%s: source %q type %q precision %q raw %d bytes", inc.SourceID, inc.Source, inc.Type, inc.TimePrecision, len(inc.Raw))
		}
		if !ingest.Finalize(inc, 1900) {
			t.Errorf("Finalize rejected %s", inc.SourceID)
		}
	}

	// The 2010 Pakistan floods: the well-known one.
	p := findDFO(t, got, "3696")
	if p.Title != "Monsoonal Rain flood, Pakistan (2010)" || p.Year != 2010 {
		t.Errorf("pakistan title %q year %d", p.Title, p.Year)
	}
	checkTime(t, p, d(2010, 7, 27), "day")
	if p.EndTime == nil || !p.EndTime.Equal(d(2010, 11, 15)) {
		t.Errorf("pakistan end = %v", p.EndTime)
	}
	if p.Lon != 73.257885 || p.Lat != 33.363388 || p.Severity != 3 {
		t.Errorf("pakistan point %v %v severity %d", p.Lon, p.Lat, p.Severity)
	}
	area := float32(129691.62951641)
	checkMag(t, p, &area, "km2")
	checkDeaths(t, p, 1750)
	if !slices.Equal(p.CountryHints, []string{"PAK"}) || p.PlaceText != "Pakistan" || p.URL != "" {
		t.Errorf("pakistan hints %v place %q url %q", p.CountryHints, p.PlaceText, p.URL)
	}
	checkProps(t, p, `{"displaced":10000000,"glide":"FL-2010-000141-PAK","validation":"News"}`)
	if !strings.Contains(p.SearchText, "monsoonal rain flood, pakistan") || !strings.HasSuffix(p.SearchText, "flood 2010 3696") {
		t.Errorf("pakistan search text = %q", p.SearchText)
	}

	// The first row: GLIDE "0" is no GLIDE number.
	a := findDFO(t, got, "1")
	if a.Title != "Heavy rain flood, Algeria (1985)" || a.Severity != 1 || !slices.Equal(a.CountryHints, []string{"DZA"}) {
		t.Errorf("algeria title %q severity %d hints %v", a.Title, a.Severity, a.CountryHints)
	}
	checkTime(t, a, d(1985, 1, 1), "day")
	checkDeaths(t, a, 26)
	checkProps(t, a, `{"displaced":3000,"validation":"News"}`)

	// DEAD 0 is unknown, not zero (carried ruling); DISPLACED 0 is left out.
	u := findDFO(t, got, "215")
	checkDeaths(t, u, -1)
	if u.CountryHints != nil || u.Title != "Torrential rain flood, USSR (1988)" {
		t.Errorf("ussr hints %v title %q", u.CountryHints, u.Title)
	}
	checkProps(t, u, `{"validation":"News"}`)

	// Two features share ID 278 and two share 4842: the second keeps its own
	// row as "<ID>-<FID>".
	if x, y := findDFO(t, got, "278"), findDFO(t, got, "278-354"); x.Lon != -3.964477 || y.Lon != 101.363924 {
		t.Errorf("278 lon %v, 278-354 lon %v", x.Lon, y.Lon)
	}
	if z, m := findDFO(t, got, "4842"), findDFO(t, got, "4842-4841"); !slices.Equal(z.CountryHints, []string{"ZMB"}) || !slices.Equal(m.CountryHints, []string{"MOZ"}) {
		t.Errorf("4842 hints %v, 4842-4841 hints %v", z.CountryHints, m.CountryHints)
	}
	checkDeaths(t, findDFO(t, got, "4842"), -1)

	// SEVERITY 1.5 → 2; COUNTRY with a slash, OTHERCOUNT appended.
	b := findDFO(t, got, "4081")
	if b.Severity != 2 || !slices.Equal(b.CountryHints, []string{"MMR", "THA"}) || b.PlaceText != "Burma/Myanmar, Thailand" {
		t.Errorf("burma severity %d hints %v place %q", b.Severity, b.CountryHints, b.PlaceText)
	}

	// Name repairs: stray letters, leading junk, aliases, accents, lists.
	for id, tc := range map[string]struct {
		hints []string
		title string
	}{
		"51":   {[]string{"VNM"}, "Heavy rain flood, Viet Nam (1985)"},
		"132":  {[]string{"CHN"}, "Heavy rain flood, China (1987)"},
		"206":  {[]string{"KOR"}, "Torrential rain flood, South Korea (1988)"},
		"490":  {[]string{"GBR", "IRL"}, "Heavy rain flood, Britain, Ireland (1991)"},
		"1414": {[]string{"KGZ", "UZB"}, "Dam/Levy, break or release flood, Kyrgyzstan (1998)"},
		"1480": {[]string{"LKA"}, "Flood, Sri Lanka (1998)"},
		"1494": {[]string{"PER"}, "Heavy rain flood, Peru (1999)"},
		"1569": {[]string{"COD", "COG"}, "Brief torrential rain flood, Democratic Republic of Congo (1999)"},
		"2369": {[]string{"CAN", "USA"}, "Heavy rain flood, Canada and USA (2003)"},
		"3487": {[]string{"CIV"}, "Heavy Rain flood, Cote d'Ivoire (2009)"},
		"3829": {[]string{"CIV"}, "Heavy Rain flood, Ivory Coast (2011)"},
	} {
		inc := findDFO(t, got, id)
		if !slices.Equal(inc.CountryHints, tc.hints) || inc.Title != tc.title {
			t.Errorf("%s: hints %v title %q, want %v %q", id, inc.CountryHints, inc.Title, tc.hints, tc.title)
		}
	}
	checkProps(t, findDFO(t, got, "3487"), `{"glide":"MS-2009-000115-CIV","validation":"News"}`)
}

// Without a name map only the aliases give hints; nothing fails.
func TestDFONoNameMap(t *testing.T) {
	got, st, _ := parseDFOFixture(t, nil, ingest.Window{})
	if len(got) != 19 {
		t.Fatalf("emitted %d, want 19", len(got))
	}
	if h := findDFO(t, got, "3829").CountryHints; !slices.Equal(h, []string{"CIV"}) {
		t.Errorf("Ivory Coast without a map: hints %v, want the alias CIV", h)
	}
	if h := findDFO(t, got, "3696").CountryHints; h != nil {
		t.Errorf("Pakistan without a map: hints %v, want none", h)
	}
	if st.unmatched["Pakistan"] != 1 {
		t.Errorf("unmatched = %v", st.unmatched)
	}
}

func TestDFOResolveCountry(t *testing.T) {
	c := newDFOCountries(dfoTestNames)
	for _, tc := range []struct {
		raw, display string
		want         []string
	}{
		{"Pakistan", "Pakistan", []string{"PAK"}},
		{"  PAKISTAN ", "PAKISTAN", []string{"PAK"}},
		{"Cote D'Ivoire", "Cote D'Ivoire", []string{"CIV"}},
		{"Côte d’Ivoire", "Côte d’Ivoire", []string{"CIV"}},
		{"USA", "USA", []string{"USA"}},
		{"USA.", "USA", []string{"USA"}},
		{"USAv", "USA", []string{"USA"}},
		{"Alaskar", "Alaska", []string{"USA"}},
		{"Brazilra", "Brazilra", nil}, // Brazil is not in the test map
		{"Chinan", "China", []string{"CHN"}},
		{"China R", "China", []string{"CHN"}},
		{"Indonesia e", "Indonesia", []string{"IDN"}},
		{"Indian", "Indian", nil},
		{"Nigeria", "Nigeria", []string{"NGA"}}, // exact beats trimming
		{"Nigern", "Niger", []string{"NER"}},
		{"Nigeriaa", "Nigeria", []string{"NGA"}},
		{"0Peru", "Peru", []string{"PER"}},
		{"Uruguay,", "Uruguay", nil},
		{"Democratic  Republic of the Congo", "Democratic Republic of the Congo", []string{"COD"}},
		{"DR Congoi", "DR Congo", []string{"COD"}},
		{"Zaire", "Zaire", []string{"COD"}},
		{"Republic of Congo", "Republic of Congo", []string{"COG"}},
		{"Burma", "Burma", []string{"MMR"}},
		{"Burma/Myanmar", "Burma/Myanmar", []string{"MMR"}},
		{"Bosnia-Herzegovina", "Bosnia-Herzegovina", []string{"BIH"}},
		{"Bosnia and Herzegovina", "Bosnia and Herzegovina", []string{"BIH"}},
		{"Serbia and Montenegro", "Serbia and Montenegro", []string{"SRB", "MNE"}},
		{"Serbia-Montenegro", "Serbia-Montenegro", []string{"SRB", "MNE"}},
		{"Sudan and Eritrea", "Sudan and Eritrea", nil}, // neither is in the test map
		{"South Sudan", "South Sudan", []string{"SDS"}},
		{"Ivory Coastn", "Ivory Coast", []string{"CIV"}},
		{"UK", "UK", []string{"GBR"}},
		{"England", "England", []string{"GBR"}},
		{"USSR", "USSR", nil},
		{"Africa", "Africa", nil},
		{"", "", nil},
		{"0", "", nil},
		{" ", "", nil},
	} {
		got, display := c.resolve(tc.raw)
		if !slices.Equal(got, tc.want) || display != tc.display {
			t.Errorf("resolve(%q) = %v %q, want %v %q", tc.raw, got, display, tc.want, tc.display)
		}
	}
}

func TestDFOSeverity(t *testing.T) {
	for _, tc := range []struct {
		v    *float64
		want domain.Severity
	}{{f64(1), 1}, {f64(1.5), 2}, {f64(2), 3}, {f64(0), 0}, {f64(3), 0}, {nil, 0}} {
		if got := dfoSeverity(tc.v); got != tc.want {
			t.Errorf("dfoSeverity(%v) = %d, want %d", tc.v, got, tc.want)
		}
	}
}

func f64(v float64) *float64 { return &v }

func TestDFOWindow(t *testing.T) {
	got, st, _ := parseDFOFixture(t, dfoTestNames, ingest.Window{From: d(2010, 1, 1), To: d(2011, 1, 1)})
	if len(got) != 1 || got[0].SourceID != "3696" || st.window != 19 {
		t.Errorf("2010 window: %d emitted, stats %+v", len(got), *st)
	}
	// From is inclusive, To exclusive (3696 begins 2010-07-27).
	if got, _, _ := parseDFOFixture(t, dfoTestNames, ingest.Window{From: d(2010, 7, 27), To: d(2010, 7, 28)}); len(got) != 1 {
		t.Errorf("edge window emitted %d", len(got))
	}
	if got, _, _ := parseDFOFixture(t, dfoTestNames, ingest.Window{From: d(2010, 7, 1), To: d(2010, 7, 27)}); len(got) != 0 {
		t.Errorf("window ending at 3696 emitted %d", len(got))
	}
}

func TestDFOParseErrors(t *testing.T) {
	emitNone := func(domain.Incident) error { return nil }
	parse := func(body string) (dfoPage, *dfoState, error) {
		st := newDFOState(nil)
		pg, err := parseDFOPage(context.Background(), strings.NewReader(body), st, ingest.Window{}, emitNone)
		return pg, st, err
	}
	if _, _, err := parse(`{"error":{"code":400,"message":"Invalid or missing input parameters.","details":[]}}`); err == nil || !strings.Contains(err.Error(), "Invalid or missing input parameters") {
		t.Errorf("ArcGIS error body: err = %v", err)
	}
	if _, _, err := parse(`{"objectIdFieldName":"FID"}`); err == nil || !strings.Contains(err.Error(), "no features") {
		t.Errorf("no features: err = %v", err)
	}
	if _, _, err := parse(``); err == nil {
		t.Error("empty body: want an error")
	}
	if pg, _, err := parse(`{"exceededTransferLimit":true,"features":[]}`); err != nil || pg.features != 0 || !pg.exceeded {
		t.Errorf("empty page: %+v %v", pg, err)
	}

	// Rows with no ID, no BEGAN or bad coordinates are skipped, not fatal.
	pg, st, err := parse(`{"features":[
		{"attributes":{"FID":1,"COUNTRY":"Peru","LONG":1,"LAT":1,"BEGAN":473385600000}},
		{"attributes":{"FID":2,"ID":2,"COUNTRY":"Peru","LONG":1,"LAT":1}},
		{"attributes":{"FID":3,"ID":3,"COUNTRY":"Peru","LONG":200,"LAT":1,"BEGAN":473385600000}},
		{"attributes":{"FID":4,"ID":4,"COUNTRY":"Peru","LAT":1,"BEGAN":473385600000}},
		{"attributes":{"FID":5,"ID":5,"COUNTRY":"Peru","LONG":1,"LAT":1,"BEGAN":473385600000,"ENDED":null,"DEAD":null,"SEVERITY":null,"MAINCAUSE":null}},
		{"geometry":null}]}`)
	if err != nil || pg.features != 6 || st.emitted != 1 || st.noID != 2 || st.badTime != 1 || st.noLocation != 2 {
		t.Errorf("bad rows: err %v page %+v stats %+v", err, pg, *st)
	}

	f, err := os.Open(dfoFixture)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	boom := errors.New("boom")
	calls := 0
	_, err = parseDFOPage(context.Background(), f, newDFOState(nil), ingest.Window{}, func(domain.Incident) error { calls++; return boom })
	if !errors.Is(err, boom) || calls != 1 {
		t.Errorf("emit error: err %v after %d calls", err, calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := parseDFOPage(ctx, f, newDFOState(nil), ingest.Window{}, emitNone); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: err = %v", err)
	}
}

// dfoServer mimics the ArcGIS FeatureServer query endpoint: it serves the
// fixture's features in the given pages, keyed by resultOffset/1000, with
// exceededTransferLimit true on every page (as ArcGIS does until the end)
// and an empty page past the end.
type dfoServer struct {
	pages [][]json.RawMessage
	mu    sync.Mutex
	reqs  []string
}

const dfoTestPath = "/AXaYBvnJsB5Q7sDF/ArcGIS/rest/services/Global_Archive_of_large_flood_events/FeatureServer/0/query"

func (s *dfoServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.reqs = append(s.reqs, r.URL.RequestURI())
	s.mu.Unlock()
	if r.URL.Path != dfoTestPath {
		http.NotFound(w, r)
		return
	}
	off, _ := strconv.Atoi(r.URL.Query().Get("resultOffset"))
	page := []json.RawMessage{}
	if i := off / 1000; i < len(s.pages) {
		page = s.pages[i]
	}
	body, _ := json.Marshal(map[string]any{"objectIdFieldName": "FID", "exceededTransferLimit": true, "features": page})
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

func (s *dfoServer) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.reqs)
}

func dfoQuery(off int) string {
	return dfoTestPath + "?where=1%3D1&outFields=*&returnGeometry=false&orderByFields=ID&resultOffset=" + strconv.Itoa(off) + "&resultRecordCount=1000&f=json"
}

func TestDFOFetchEndToEnd(t *testing.T) {
	body, err := os.ReadFile(dfoFixture)
	if err != nil {
		t.Fatal(err)
	}
	var fx struct {
		Features []json.RawMessage `json:"features"`
	}
	if err := json.Unmarshal(body, &fx); err != nil {
		t.Fatal(err)
	}
	srv := &dfoServer{pages: [][]json.RawMessage{fx.Features[:6], fx.Features[6:]}}
	ts := httptest.NewServer(srv)
	defer ts.Close()

	f := fetch.New(ts.Client(), t.TempDir(), quietLog())
	s := NewDFO(f, dfoTestNames, quietLog())
	s.URL = ts.URL + dfoTestPath
	if s.Name() != "dfo" {
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
	want := func(step string, reqs ...string) {
		t.Helper()
		if r := srv.requests(); strings.Join(r, "\n") != strings.Join(reqs, "\n") {
			t.Errorf("%s: requests =\n%s\nwant\n%s", step, strings.Join(r, "\n"), strings.Join(reqs, "\n"))
		}
	}

	full := ingest.Window{From: d(1900, 1, 1), To: d(2026, 11, 15)}
	if n := run(full); n != 19 {
		t.Errorf("full run emitted %d, want 19", n)
	}
	// The two features with ID 278 sit on different pages: the seen-set
	// spans pages, so the second still becomes 278-354.
	if p := findDFO(t, got, "3696"); !slices.Equal(p.CountryHints, []string{"PAK"}) {
		t.Errorf("pakistan hints = %v", p.CountryHints)
	}
	if x := findDFO(t, got, "278-354"); x.Lon != 101.363924 {
		t.Errorf("278-354 lon = %v", x.Lon)
	}
	want("full run", dfoQuery(0), dfoQuery(1000), dfoQuery(2000))

	// The archive is frozen (it ends in 2021): a second run with any window
	// is served from the disk cache.
	if n := run(ingest.Window{From: d(2010, 1, 1), To: d(2011, 1, 1)}); n != 1 {
		t.Errorf("2010 run emitted %d, want 1", n)
	}
	want("cached run", dfoQuery(0), dfoQuery(1000), dfoQuery(2000))

	// The global --refresh downloads every page again.
	fr := fetch.New(ts.Client(), f.RawDir, quietLog())
	fr.Refresh = true
	sr := NewDFO(fr, nil, quietLog())
	sr.URL = s.URL
	if err := sr.Fetch(context.Background(), full, func(domain.Incident) error { return nil }); err != nil {
		t.Fatal(err)
	}
	want("--refresh", dfoQuery(0), dfoQuery(1000), dfoQuery(2000), dfoQuery(0), dfoQuery(1000), dfoQuery(2000))

	// An empty window does nothing.
	if n := run(ingest.Window{From: d(2013, 1, 1), To: d(2013, 1, 1)}); n != 0 || len(srv.requests()) != 6 {
		t.Errorf("empty window: emitted %d, requests %d", n, len(srv.requests()))
	}

	// A page without exceededTransferLimit is the last one: no further call.
	srv2 := &dfoServer{}
	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv2.mu.Lock()
		srv2.reqs = append(srv2.reqs, r.URL.RequestURI())
		srv2.mu.Unlock()
		_, _ = w.Write(body)
	}))
	defer ts2.Close()
	s2 := NewDFO(fetch.New(ts2.Client(), t.TempDir(), quietLog()), dfoTestNames, quietLog())
	s2.URL = ts2.URL + dfoTestPath
	var n2 int
	if err := s2.Fetch(context.Background(), full, func(domain.Incident) error { n2++; return nil }); err != nil {
		t.Fatal(err)
	}
	if r := srv2.requests(); n2 != 19 || len(r) != 1 || r[0] != dfoQuery(0) {
		t.Errorf("last page: emitted %d, requests %v", n2, r)
	}

	// A missing endpoint is an error.
	s.URL = ts.URL + "/nope"
	if err := s.Fetch(context.Background(), full, func(domain.Incident) error { return nil }); err == nil {
		t.Error("404: want an error")
	}
}
