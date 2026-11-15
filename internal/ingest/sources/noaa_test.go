package sources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dwts/backend/internal/domain"
	"github.com/dwts/backend/internal/ingest"
	"github.com/dwts/backend/internal/ingest/fetch"
)

// The NOAA fixtures are real hazard-service responses, trimmed and reordered
// so the headline event comes first:
//   - noaa_eq.json: 20 earthquakes of 2011 (Tōhoku, id 9799, first) and four
//     real 1900s rows with no month (2701), no day (2771), no hour (2567) and
//     no magnitude (2647), plus one HAND-EDITED row (id 999001, a copy of 2913
//     with latitude/longitude removed) that must be skipped: 24 usable rows.
//   - noaa_ts.json: the 21 tsunamis of 2011 (Tōhoku, id 5413, first; 6155 has
//     no lat/lon) and three real early rows: 1452 (no month), 1297 (no day and
//     no lat/lon) and 1437 (no day): 22 usable rows.
//   - noaa_vo.json: the 7 eruptions of 1991 (Pinatubo, id 4428, first) and 13
//     real rows from page 1 of minYear=1900 (1354 has no month, 87 no day;
//     21 and 32 have no VEI): 20 usable rows.
const (
	noaaEQFixture = "../../../testdata/fixtures/noaa_eq.json"
	noaaTSFixture = "../../../testdata/fixtures/noaa_ts.json"
	noaaVOFixture = "../../../testdata/fixtures/noaa_vo.json"
)

func parseNOAAFixture(t *testing.T, k noaaKind, path string) ([]domain.Incident, int, int) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var got []domain.Incident
	pg, err := parseNOAAPage(f, k, 0, 0, func(inc domain.Incident) error {
		got = append(got, inc)
		return nil
	})
	if err != nil {
		t.Fatalf("parseNOAAPage: %v", err)
	}
	if pg.emitted != len(got) {
		t.Errorf("reported %d emitted, callback saw %d", pg.emitted, len(got))
	}
	if pg.items != pg.emitted+pg.skipped {
		t.Errorf("items %d != emitted %d + skipped %d", pg.items, pg.emitted, pg.skipped)
	}
	for i := range got {
		if !ingest.Finalize(&got[i], 1900) {
			t.Errorf("Finalize rejected %s/%s", got[i].Source, got[i].SourceID)
		}
	}
	return got, pg.skipped, pg.totalPages
}

func findNOAA(t *testing.T, got []domain.Incident, id string) domain.Incident {
	t.Helper()
	for _, inc := range got {
		if inc.SourceID == id {
			return inc
		}
	}
	t.Fatalf("source id %s not emitted", id)
	return domain.Incident{}
}

func f32(v float32) *float32 { return &v }

func checkMag(t *testing.T, inc domain.Incident, want *float32, unit string) {
	t.Helper()
	switch {
	case want == nil && inc.Magnitude != nil:
		t.Errorf("%s magnitude = %v, want nil", inc.SourceID, *inc.Magnitude)
	case want != nil && (inc.Magnitude == nil || *inc.Magnitude != *want):
		t.Errorf("%s magnitude = %v, want %v", inc.SourceID, inc.Magnitude, *want)
	}
	if inc.MagnitudeUnit != unit {
		t.Errorf("%s magnitude unit = %q, want %q", inc.SourceID, inc.MagnitudeUnit, unit)
	}
}

func checkDeaths(t *testing.T, inc domain.Incident, want int32) {
	t.Helper()
	if want < 0 {
		if inc.Deaths != nil {
			t.Errorf("%s deaths = %d, want nil", inc.SourceID, *inc.Deaths)
		}
		return
	}
	if inc.Deaths == nil || *inc.Deaths != want {
		t.Errorf("%s deaths = %v, want %d", inc.SourceID, inc.Deaths, want)
	}
}

// checkProps compares props as the JSON the writer stores, so an int and a
// float with the same printed value are not confused with a string.
func checkProps(t *testing.T, inc domain.Incident, want string) {
	t.Helper()
	got, err := json.Marshal(inc.Props)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("%s props = %s\nwant          %s", inc.SourceID, got, want)
	}
}

func checkTime(t *testing.T, inc domain.Incident, want time.Time, prec domain.Precision) {
	t.Helper()
	if !inc.StartTime.Equal(want) || inc.StartTime.Location() != time.UTC {
		t.Errorf("%s start = %v, want %v UTC", inc.SourceID, inc.StartTime, want)
	}
	if inc.TimePrecision != prec {
		t.Errorf("%s precision = %q, want %q", inc.SourceID, inc.TimePrecision, prec)
	}
}

func TestNOAAEarthquakeFixture(t *testing.T) {
	got, skipped, total := parseNOAAFixture(t, noaaEQ, noaaEQFixture)
	if len(got) != 24 || skipped != 1 {
		t.Fatalf("emitted %d, skipped %d; want 24, 1", len(got), skipped)
	}
	if total != 1 {
		t.Errorf("totalPages = %d, want 1", total)
	}
	for _, inc := range got {
		if inc.SourceID == "999001" {
			t.Errorf("row without lat/lon was emitted")
		}
	}

	inc := got[0]
	if inc.Source != "noaa_eq" || inc.SourceID != "9799" || inc.Type != domain.Earthquake {
		t.Errorf("key = %s/%s %s", inc.Source, inc.SourceID, inc.Type)
	}
	if want := "JAPAN: HONSHU earthquake (2011)"; inc.Title != want {
		t.Errorf("title = %q, want %q", inc.Title, want)
	}
	checkTime(t, inc, time.Date(2011, 3, 11, 5, 46, 24, 100e6, time.UTC), "second")
	if inc.Lon != 142.373 || inc.Lat != 38.297 {
		t.Errorf("lon/lat = %v/%v", inc.Lon, inc.Lat)
	}
	checkMag(t, inc, f32(9.1), "")
	checkDeaths(t, inc, 1474) // `deaths`, not deathsTotal (18423)
	if inc.Severity != 3 {
		t.Errorf("severity = %d, want 3", inc.Severity)
	}
	if want := "https://www.ngdc.noaa.gov/hazel/view/hazards/earthquake/event-more-info/9799"; inc.URL != want {
		t.Errorf("url = %q", inc.URL)
	}
	if inc.PlaceText != "JAPAN: HONSHU, JAPAN" {
		t.Errorf("place = %q", inc.PlaceText)
	}
	wantProps := `{"country":"JAPAN","damageAmountOrder":4,"damageMillionsDollars":4401.709,` +
		`"deathsAmountOrder":4,"depthKm":29,"injuries":6157,"tsunamiEventId":5413}`
	checkProps(t, inc, wantProps)
	// P6 copies props.damageAmountOrder onto the USGS row and needs a JSON
	// number: an int in Go, not a string.
	if v, ok := inc.Props["damageAmountOrder"].(int); !ok || v != 4 {
		t.Errorf("damageAmountOrder = %#v, want int 4", inc.Props["damageAmountOrder"])
	}
	var raw struct{ ID int64 }
	if err := json.Unmarshal(inc.Raw, &raw); err != nil || raw.ID != 9799 {
		t.Errorf("raw is not the item JSON: %v %q", err, inc.Raw)
	}
	if inc.Year != 2011 || inc.RawHash == "" {
		t.Errorf("finalized year/hash = %d/%q", inc.Year, inc.RawHash)
	}

	// Partial dates.
	checkTime(t, findNOAA(t, got, "2701"), time.Date(1905, 1, 1, 0, 0, 0, 0, time.UTC), "year")
	checkTime(t, findNOAA(t, got, "2771"), time.Date(1906, 5, 1, 0, 0, 0, 0, time.UTC), "month")
	checkTime(t, findNOAA(t, got, "2567"), time.Date(1900, 1, 14, 0, 0, 0, 0, time.UTC), "day")
	// No magnitude, no deaths, no amount orders: severity 1.
	noMag := findNOAA(t, got, "2647")
	checkMag(t, noMag, nil, "")
	checkDeaths(t, noMag, -1)
	if noMag.Severity != 1 || noMag.Title != "TURKEY earthquake (1903)" {
		t.Errorf("2647 = %q severity %d", noMag.Title, noMag.Severity)
	}
	// Severity from deathsAmountOrder 3 / damageAmountOrder 2.
	if s := findNOAA(t, got, "9801").Severity; s != 3 {
		t.Errorf("9801 severity = %d, want 3", s)
	}
	// The M7.3 foreshock has no deaths at all.
	checkDeaths(t, findNOAA(t, got, "9797"), -1)
}

func TestNOAATsunamiFixture(t *testing.T) {
	got, skipped, _ := parseNOAAFixture(t, noaaTS, noaaTSFixture)
	if len(got) != 22 || skipped != 2 {
		t.Fatalf("emitted %d, skipped %d; want 22, 2", len(got), skipped)
	}
	inc := got[0]
	if inc.Source != "noaa_ts" || inc.SourceID != "5413" || inc.Type != domain.Tsunami {
		t.Errorf("key = %s/%s %s", inc.Source, inc.SourceID, inc.Type)
	}
	if want := "HONSHU ISLAND tsunami (2011)"; inc.Title != want {
		t.Errorf("title = %q, want %q", inc.Title, want)
	}
	checkTime(t, inc, time.Date(2011, 3, 11, 5, 46, 24, 100e6, time.UTC), "second")
	checkMag(t, inc, f32(39.26), "m")
	checkDeaths(t, inc, 18423)
	if inc.Severity != 3 {
		t.Errorf("severity = %d", inc.Severity)
	}
	if want := "https://www.ngdc.noaa.gov/hazel/view/hazards/tsunami/event-more-info/5413"; inc.URL != want {
		t.Errorf("url = %q", inc.URL)
	}
	wantProps := `{"causeCode":1,"country":"JAPAN","damageAmountOrder":4,"deathsAmountOrder":4,` +
		`"earthquakeEventId":9799,"eqMagnitude":9.1,"eventValidity":4,"numRunups":6429}`
	checkProps(t, inc, wantProps)

	// 5407 (Christchurch): only deathsTotal / deathsAmountOrderTotal.
	nz := findNOAA(t, got, "5407")
	checkDeaths(t, nz, 185)
	if nz.Severity != 3 {
		t.Errorf("5407 severity = %d, want 3 (deathsAmountOrderTotal 3)", nz.Severity)
	}
	// 5434: no maxWaterHeight.
	checkMag(t, findNOAA(t, got, "5434"), nil, "m")
	checkTime(t, findNOAA(t, got, "5433"), time.Date(2011, 6, 27, 0, 0, 0, 0, time.UTC), "day")
	checkTime(t, findNOAA(t, got, "1452"), time.Date(1918, 1, 1, 0, 0, 0, 0, time.UTC), "year")
	checkTime(t, findNOAA(t, got, "1437"), time.Date(1917, 1, 1, 0, 0, 0, 0, time.UTC), "month")
}

func TestNOAAVolcanoFixture(t *testing.T) {
	got, skipped, _ := parseNOAAFixture(t, noaaVO, noaaVOFixture)
	if len(got) != 20 || skipped != 0 {
		t.Fatalf("emitted %d, skipped %d; want 20, 0", len(got), skipped)
	}
	inc := got[0]
	if inc.Source != "noaa_vo" || inc.SourceID != "4428" || inc.Type != domain.Volcano {
		t.Errorf("key = %s/%s %s", inc.Source, inc.SourceID, inc.Type)
	}
	if inc.Title != "Pinatubo eruption (1991)" || inc.Name != "Pinatubo" {
		t.Errorf("title/name = %q/%q", inc.Title, inc.Name)
	}
	checkTime(t, inc, time.Date(1991, 6, 15, 0, 0, 0, 0, time.UTC), "day")
	if inc.Lon != 120.35 || inc.Lat != 15.13 {
		t.Errorf("lon/lat = %v/%v", inc.Lon, inc.Lat)
	}
	checkMag(t, inc, f32(6), "VEI")
	checkDeaths(t, inc, 350)
	if inc.Severity != 3 {
		t.Errorf("severity = %d", inc.Severity)
	}
	if want := "https://www.ngdc.noaa.gov/hazel/view/hazards/volcano/event-more-info/4428"; inc.URL != want {
		t.Errorf("url = %q", inc.URL)
	}
	if inc.PlaceText != "Luzon-Philippines, Philippines" {
		t.Errorf("place = %q", inc.PlaceText)
	}
	wantProps := `{"country":"Philippines","damageAmountOrder":3,"deathsAmountOrder":3,` +
		`"elevationM":1486,"morphology":"Stratovolcano","vei":6}`
	checkProps(t, inc, wantProps)

	// Unzendake: VEI 1 and deathsAmountOrder 1 → 1. Nyiragongo 1977: VEI 1
	// but damageAmountOrder 3 → 3 (severity is the max of both rules).
	unzen := findNOAA(t, got, "933")
	checkMag(t, unzen, f32(1), "VEI")
	checkDeaths(t, unzen, 43)
	if unzen.Severity != 1 {
		t.Errorf("Unzendake severity = %d, want 1", unzen.Severity)
	}
	if s := findNOAA(t, got, "7").Severity; s != 3 {
		t.Errorf("Nyiragongo severity = %d, want 3", s)
	}
	// Hudson VEI 5 → 3; Etna VEI 2 with damageAmountOrder 2 → 2.
	if s := findNOAA(t, got, "4442").Severity; s != 3 {
		t.Errorf("Hudson severity = %d, want 3", s)
	}
	if s := findNOAA(t, got, "5488").Severity; s != 2 {
		t.Errorf("Etna severity = %d, want 2", s)
	}
	checkTime(t, findNOAA(t, got, "1354"), time.Date(1914, 1, 1, 0, 0, 0, 0, time.UTC), "year")
	checkTime(t, findNOAA(t, got, "87"), time.Date(1902, 12, 1, 0, 0, 0, 0, time.UTC), "month")
	checkMag(t, findNOAA(t, got, "21"), nil, "VEI")
}

func TestNOAASeverity(t *testing.T) {
	ip := func(v int) *int { return &v }
	fp := func(v float64) *float64 { return &v }
	tests := []struct {
		name string
		k    noaaKind
		r    noaaRecord
		want domain.Severity
	}{
		{"none", noaaEQ, noaaRecord{}, 1},
		{"damage 2", noaaEQ, noaaRecord{DamageAmountOrder: ip(2)}, 2},
		{"damage 3", noaaEQ, noaaRecord{DamageAmountOrder: ip(3)}, 3},
		{"deaths 4", noaaEQ, noaaRecord{DeathsAmountOrder: ip(4), DamageAmountOrder: ip(1)}, 3},
		{"total only", noaaTS, noaaRecord{DamageAmountOrderTotal: ip(2)}, 2},
		{"order 1", noaaTS, noaaRecord{DeathsAmountOrder: ip(1)}, 1},
		{"vei 4", noaaVO, noaaRecord{VEI: fp(4)}, 3},
		{"vei 3", noaaVO, noaaRecord{VEI: fp(3)}, 2},
		{"vei 2", noaaVO, noaaRecord{VEI: fp(2)}, 1},
		{"vei 1 deaths 3", noaaVO, noaaRecord{VEI: fp(1), DeathsAmountOrder: ip(3)}, 3},
		{"vei ignored for eq", noaaEQ, noaaRecord{VEI: fp(6)}, 1},
	}
	for _, tc := range tests {
		if got := noaaSeverity(tc.k, &tc.r); got != tc.want {
			t.Errorf("%s: severity = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestNOAAParseEdgeCases(t *testing.T) {
	// items before totalPages (the API does this on pages past the end), a
	// title fallback to country, a deathsTotal fallback, a bad month and a
	// row outside the year range.
	body := `{"items":[
{"id":1,"year":1950,"month":13,"day":5,"country":"PERU","latitude":-10,"longitude":-75,"deathsTotal":7},
{"id":2,"year":1950,"month":2,"day":30,"hour":25,"latitude":1,"longitude":2},
{"id":3,"year":1949,"month":1,"day":1,"locationName":"X","latitude":1,"longitude":2},
{"year":1950,"locationName":"no id","latitude":1,"longitude":2},
{"id":5,"locationName":"no year","latitude":1,"longitude":2}
],"page":1,"totalPages":3,"itemsPerPage":200,"totalItems":5}`
	var got []domain.Incident
	pg, err := parseNOAAPage(strings.NewReader(body), noaaEQ, 1950, 1950, func(inc domain.Incident) error {
		got = append(got, inc)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if pg.totalPages != 3 || pg.items != 5 || pg.emitted != 2 || pg.skipped != 3 || len(got) != 2 {
		t.Fatalf("page = %+v, got %d", pg, len(got))
	}
	if got[0].Title != "PERU earthquake (1950)" || got[0].TimePrecision != "year" {
		t.Errorf("row 1 = %q %q", got[0].Title, got[0].TimePrecision)
	}
	checkDeaths(t, got[0], 7)
	if got[1].Title != "Earthquake (1950)" {
		t.Errorf("row 2 title = %q", got[1].Title)
	}
	checkTime(t, got[1], time.Date(1950, 2, 1, 0, 0, 0, 0, time.UTC), "month") // day 30 does not exist in Feb
}

func TestNOAAParseErrors(t *testing.T) {
	for _, body := range []string{``, `{"items":[{"id":`, `[1,2]`, `{"items":{}}`, `{"page":1,"totalPages":1}`, `{"items":[1]}`} {
		if _, err := parseNOAAPage(strings.NewReader(body), noaaEQ, 0, 0, func(domain.Incident) error { return nil }); err == nil {
			t.Errorf("parseNOAAPage(%q) = nil error", body)
		}
	}
	f, err := os.Open(noaaEQFixture)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	boom := errors.New("boom")
	if _, err := parseNOAAPage(f, noaaEQ, 0, 0, func(domain.Incident) error { return boom }); !errors.Is(err, boom) {
		t.Errorf("emit error = %v, want boom", err)
	}
}

// noaaServer serves a fixture's items split into pages of perPage, and
// records each request's path and query.
type noaaServer struct {
	mu      sync.Mutex
	items   []json.RawMessage
	perPage int
	reqs    []string
}

func (s *noaaServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reqs = append(s.reqs, r.URL.Path+"?"+r.URL.RawQuery)
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	total := (len(s.items) + s.perPage - 1) / s.perPage
	lo, hi := (page-1)*s.perPage, page*s.perPage
	lo, hi = min(max(lo, 0), len(s.items)), min(max(hi, 0), len(s.items))
	items := append([]json.RawMessage{}, s.items[lo:hi]...) // [] (not null) when empty, like the API
	b, _ := json.Marshal(items)
	fmt.Fprintf(w, `{"page":%d,"totalPages":%d,"itemsPerPage":%d,"totalItems":%d,"items":%s}`, page, total, s.perPage, len(s.items), b)
}

func TestNOAAFetchEndToEnd(t *testing.T) {
	cases := []struct {
		name, fixture, path string
		mk                  func(*fetch.Fetcher) *NOAA
		want                int
	}{
		{"noaa_eq", noaaEQFixture, "/earthquakes", func(f *fetch.Fetcher) *NOAA { return NewNOAAEarthquakes(f, quietLog()) }, 24},
		{"noaa_ts", noaaTSFixture, "/tsunamis/events", func(f *fetch.Fetcher) *NOAA { return NewNOAATsunamis(f, quietLog()) }, 22},
		{"noaa_vo", noaaVOFixture, "/volcanoes", func(f *fetch.Fetcher) *NOAA { return NewNOAAVolcanoes(f, quietLog()) }, 20},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := os.ReadFile(tc.fixture)
			if err != nil {
				t.Fatal(err)
			}
			var doc struct{ Items []json.RawMessage }
			if err := json.Unmarshal(b, &doc); err != nil {
				t.Fatal(err)
			}
			srv := &noaaServer{items: doc.Items, perPage: 8}
			ts := httptest.NewServer(srv)
			defer ts.Close()

			f := fetch.New(ts.Client(), t.TempDir(), quietLog())
			s := tc.mk(f)
			s.BaseURL = ts.URL
			s.Now = func() time.Time { return time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC) }
			if s.Name() != tc.name {
				t.Errorf("Name = %q", s.Name())
			}
			if f.Limiter != nil || s.F == f || s.F.Limiter == nil {
				t.Errorf("constructor must pace a copy of the Fetcher, not the caller's")
			}

			// The whole catalog (window up to now): no maxYear, so the
			// cache key is the Guide's URL.
			w := ingest.Window{From: d(1900, 1, 1), To: time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)}
			var got []domain.Incident
			collect := func(inc domain.Incident) error { got = append(got, inc); return nil }
			if err := s.Fetch(context.Background(), w, collect); err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			if len(got) != tc.want {
				t.Errorf("emitted %d, want %d", len(got), tc.want)
			}
			pages := (len(doc.Items) + 7) / 8
			if len(srv.reqs) != pages {
				t.Fatalf("requests = %v, want %d pages", srv.reqs, pages)
			}
			for i, r := range srv.reqs {
				if want := fmt.Sprintf("%s?minYear=1900&page=%d", tc.path, i+1); r != want {
					t.Errorf("request %d = %q, want %q", i, r, want)
				}
			}

			// The window reaches the current year, so the same URLs can hold
			// new events: a second run must download them again, not replay
			// the cache.
			got = nil
			if err := s.Fetch(context.Background(), w, collect); err != nil {
				t.Fatal(err)
			}
			if len(got) != tc.want || len(srv.reqs) != 2*pages {
				t.Errorf("second run: emitted %d, requests %d; want %d, %d (refetched)", len(got), len(srv.reqs), tc.want, 2*pages)
			}
			if s.F.Refresh {
				t.Errorf("Fetch left Refresh set on the adapter's Fetcher")
			}
		})
	}
}

func TestNOAAFetchWindowYears(t *testing.T) {
	b, err := os.ReadFile(noaaEQFixture)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct{ Items []json.RawMessage }
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	srv := &noaaServer{items: doc.Items, perPage: 200}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	s := NewNOAAEarthquakes(fetch.New(ts.Client(), t.TempDir(), quietLog()), quietLog())
	s.BaseURL = ts.URL
	s.Now = func() time.Time { return time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC) }

	// --from 2011 --to 2011: [2011-01-01, 2012-01-01) → minYear=maxYear=2011.
	// The fake server ignores the years, so the adapter's own year filter
	// must drop the 1900s rows.
	var got []domain.Incident
	err = s.Fetch(context.Background(), ingest.Window{From: d(2011, 1, 1), To: d(2012, 1, 1)},
		func(inc domain.Incident) error { got = append(got, inc); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if want := "/earthquakes?minYear=2011&maxYear=2011&page=1"; len(srv.reqs) != 1 || srv.reqs[0] != want {
		t.Errorf("requests = %v, want [%s]", srv.reqs, want)
	}
	if len(got) != 20 {
		t.Errorf("emitted %d, want the 20 rows of 2011", len(got))
	}
	// A closed past window cannot change: the second run is served from the
	// disk cache.
	got = nil
	err = s.Fetch(context.Background(), ingest.Window{From: d(2011, 1, 1), To: d(2012, 1, 1)},
		func(inc domain.Incident) error { got = append(got, inc); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 20 || len(srv.reqs) != 1 {
		t.Errorf("second closed-window run: emitted %d, requests %v; want 20 from cache", len(got), srv.reqs)
	}

	// --since 30d: [2026-10-16, 2026-11-15) → minYear=2026, no maxYear, and
	// a second run downloads again.
	since := ingest.Window{From: d(2026, 10, 16), To: d(2026, 11, 15)}
	for range 2 {
		if err := s.Fetch(context.Background(), since, func(domain.Incident) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"/earthquakes?minYear=2011&maxYear=2011&page=1", "/earthquakes?minYear=2026&page=1", "/earthquakes?minYear=2026&page=1"}
	if fmt.Sprint(srv.reqs) != fmt.Sprint(want) {
		t.Errorf("requests = %v\nwant       %v", srv.reqs, want)
	}

	// An empty catalog (totalPages 0) is one request and no error.
	empty := &noaaServer{perPage: 200}
	ts2 := httptest.NewServer(empty)
	defer ts2.Close()
	s2 := NewNOAAVolcanoes(fetch.New(ts2.Client(), t.TempDir(), quietLog()), quietLog())
	s2.BaseURL = ts2.URL
	s2.Now = s.Now
	if err := s2.Fetch(context.Background(), ingest.Window{From: d(1900, 1, 1), To: d(2026, 11, 15)},
		func(domain.Incident) error { t.Error("emitted from an empty catalog"); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(empty.reqs) != 1 {
		t.Errorf("empty catalog requests = %v", empty.reqs)
	}

	// Cancellation stops the run.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s3 := NewNOAATsunamis(fetch.New(ts.Client(), t.TempDir(), quietLog()), quietLog())
	s3.BaseURL = ts.URL
	s3.Now = s.Now
	if err := s3.Fetch(ctx, ingest.Window{From: d(1900, 1, 1), To: d(2026, 11, 15)}, func(domain.Incident) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled err = %v", err)
	}
}
