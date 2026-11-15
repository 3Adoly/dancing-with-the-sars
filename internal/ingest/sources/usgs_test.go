package sources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
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

// The fixture is a real USGS response (2011-03-11 05:40–06:10 UTC, M≥4.5,
// 11 features starting with Tōhoku) plus one hand-added feature with a null
// mag (id handmade0001, third in the list) that the parser must skip.
const usgsFixture = "../../../testdata/fixtures/usgs_sample.geojson"

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func parseFixture(t *testing.T) ([]domain.Incident, int) {
	t.Helper()
	f, err := os.Open(usgsFixture)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var got []domain.Incident
	n, skipped, err := parseUSGS(f, func(inc domain.Incident) error {
		got = append(got, inc)
		return nil
	})
	if err != nil {
		t.Fatalf("parseUSGS: %v", err)
	}
	if n != len(got) {
		t.Errorf("parseUSGS reported %d emitted, callback saw %d", n, len(got))
	}
	return got, skipped
}

func TestUSGSParseFixture(t *testing.T) {
	got, skipped := parseFixture(t)
	if len(got) != 11 {
		t.Fatalf("emitted %d incidents, want 11", len(got))
	}
	if skipped != 1 {
		t.Errorf("skipped = %d, want 1 (the null-mag feature)", skipped)
	}
	for _, inc := range got {
		if inc.SourceID == "handmade0001" {
			t.Errorf("null-mag feature was emitted")
		}
	}

	inc := got[0]
	if inc.Source != "usgs" || inc.SourceID != "official20110311054624120_30" {
		t.Errorf("key = %s/%s", inc.Source, inc.SourceID)
	}
	if inc.Type != domain.Earthquake {
		t.Errorf("type = %q", inc.Type)
	}
	if want := "M 9.1 - 2011 Great Tohoku Earthquake, Japan"; inc.Title != want {
		t.Errorf("title = %q, want %q", inc.Title, want)
	}
	if want := time.Date(2011, 3, 11, 5, 46, 24, 120e6, time.UTC); !inc.StartTime.Equal(want) || inc.StartTime.Location() != time.UTC {
		t.Errorf("start = %v, want %v UTC", inc.StartTime, want)
	}
	if inc.TimePrecision != "second" {
		t.Errorf("precision = %q", inc.TimePrecision)
	}
	if inc.Lon != 142.373 || inc.Lat != 38.297 {
		t.Errorf("lon/lat = %v/%v", inc.Lon, inc.Lat)
	}
	if inc.Magnitude == nil || *inc.Magnitude != float32(9.1) || inc.MagnitudeUnit != "mww" {
		t.Errorf("magnitude = %v %q", inc.Magnitude, inc.MagnitudeUnit)
	}
	if inc.Severity != 3 {
		t.Errorf("severity = %d, want 3 (no alert, mag ≥ 7)", inc.Severity)
	}
	if want := "https://earthquake.usgs.gov/earthquakes/eventpage/official20110311054624120_30"; inc.URL != want {
		t.Errorf("url = %q", inc.URL)
	}
	if inc.PlaceText != "2011 Great Tohoku Earthquake, Japan" {
		t.Errorf("place = %q", inc.PlaceText)
	}
	wantProps := map[string]any{"depthKm": 29.0, "felt": 1460, "tsunami": 0, "sig": 2184}
	if fmt.Sprint(inc.Props) != fmt.Sprint(wantProps) {
		t.Errorf("props = %v, want %v (null alert omitted)", inc.Props, wantProps)
	}
	var raw struct{ ID string }
	if err := json.Unmarshal(inc.Raw, &raw); err != nil || raw.ID != inc.SourceID {
		t.Errorf("raw is not the feature JSON: %v %q", err, inc.Raw)
	}

	// The second feature has felt null: it is omitted from props.
	if _, ok := got[1].Props["felt"]; ok {
		t.Errorf("null felt kept in props: %v", got[1].Props)
	}
	if got[1].Severity != 2 { // M 6.3, no alert
		t.Errorf("second severity = %d, want 2", got[1].Severity)
	}

	// Every emitted incident survives the shared normalizer.
	for i := range got {
		if !ingest.Finalize(&got[i], 1900) {
			t.Errorf("Finalize rejected %s", got[i].SourceID)
		}
	}
	if got[0].Year != 2011 || got[0].RawHash == "" {
		t.Errorf("finalized year/hash = %d/%q", got[0].Year, got[0].RawHash)
	}
}

func TestUSGSParseSkipsIncompleteFeatures(t *testing.T) {
	body := `{"type":"FeatureCollection","metadata":{"count":5},"features":[
{"type":"Feature","properties":{"mag":5,"place":"","time":-1893456000000,"alert":"orange","magType":"mw","tsunami":1},"geometry":{"type":"Point","coordinates":[10,20]},"id":"old"},
{"type":"Feature","properties":{"mag":5,"place":"x","time":null},"geometry":{"type":"Point","coordinates":[10,20,5]},"id":"notime"},
{"type":"Feature","properties":{"mag":5,"place":"x","time":0},"geometry":null,"id":"nogeom"},
{"type":"Feature","properties":{"mag":5,"place":"x","time":0},"geometry":{"type":"Point","coordinates":[10]},"id":"shortcoords"},
{"type":"Feature","properties":{"mag":null,"place":"x","time":0},"geometry":{"type":"Point","coordinates":[1,2,3]},"id":"nomag"}
],"bbox":[1,2,3,4,5,6]}`
	var got []domain.Incident
	n, skipped, err := parseUSGS(strings.NewReader(body), func(inc domain.Incident) error {
		got = append(got, inc)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || skipped != 4 || len(got) != 1 {
		t.Fatalf("emitted %d (callback %d), skipped %d; want 1, 4", n, len(got), skipped)
	}
	inc := got[0]
	if want := time.Date(1910, 1, 1, 0, 0, 0, 0, time.UTC); !inc.StartTime.Equal(want) {
		t.Errorf("negative epoch ms: start = %v, want %v", inc.StartTime, want)
	}
	if inc.Title != "M 5.0 earthquake" {
		t.Errorf("empty-place title = %q", inc.Title)
	}
	if inc.Severity != 2 {
		t.Errorf("alert orange severity = %d, want 2", inc.Severity)
	}
	if _, ok := inc.Props["depthKm"]; ok {
		t.Errorf("depthKm present without a depth: %v", inc.Props)
	}
	if inc.Props["alert"] != "orange" || inc.Props["tsunami"] != 1 {
		t.Errorf("props = %v", inc.Props)
	}
}

func TestUSGSParseStopsOnEmitError(t *testing.T) {
	f, err := os.Open(usgsFixture)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	boom := errors.New("boom")
	_, _, err = parseUSGS(f, func(domain.Incident) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
}

func TestUSGSParseRejectsMalformedJSON(t *testing.T) {
	for _, body := range []string{``, `{"features":[{"id":`, `[1,2]`, `{"features":{}}`} {
		if _, _, err := parseUSGS(strings.NewReader(body), func(domain.Incident) error { return nil }); err == nil {
			t.Errorf("parseUSGS(%q) = nil error", body)
		}
	}
}

func TestUSGSSeverity(t *testing.T) {
	tests := []struct {
		alert string
		mag   float64
		want  domain.Severity
	}{
		{"red", 4.5, 3}, {"orange", 4.5, 2}, {"yellow", 9, 1}, {"green", 9, 1}, {"RED", 4.5, 3},
		{"", 7.0, 3}, {"", 6.99, 2}, {"", 6.0, 2}, {"", 5.99, 1}, {"", 4.5, 1}, {"pending", 7.2, 3},
	}
	for _, tc := range tests {
		if got := usgsSeverity(tc.alert, tc.mag); got != tc.want {
			t.Errorf("usgsSeverity(%q, %v) = %d, want %d", tc.alert, tc.mag, got, tc.want)
		}
	}
}

func d(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 0, 0, 0, 0, time.UTC) }

func TestUSGSWindows(t *testing.T) {
	ws := usgsWindows(d(1900, 1, 1), d(2026, 11, 15))
	// 6 decades + 13 years (1960–1972) + 53 years of months (1973–2025) + Jan..Nov 2026.
	if want := 6 + 13 + 53*12 + 11; len(ws) != want {
		t.Fatalf("%d windows, want %d", len(ws), want)
	}
	for i := 1; i < len(ws); i++ {
		if !ws[i].From.Equal(ws[i-1].To) {
			t.Fatalf("gap between windows %d and %d: %v / %v", i-1, i, ws[i-1], ws[i])
		}
	}
	checks := map[int]ingest.Window{
		0:           {From: d(1900, 1, 1), To: d(1910, 1, 1)},
		5:           {From: d(1950, 1, 1), To: d(1960, 1, 1)},
		6:           {From: d(1960, 1, 1), To: d(1961, 1, 1)},
		18:          {From: d(1972, 1, 1), To: d(1973, 1, 1)},
		19:          {From: d(1973, 1, 1), To: d(1973, 2, 1)},
		30:          {From: d(1973, 12, 1), To: d(1974, 1, 1)},
		len(ws) - 1: {From: d(2026, 11, 1), To: d(2026, 11, 15)},
	}
	for i, want := range checks {
		if !ws[i].From.Equal(want.From) || !ws[i].To.Equal(want.To) {
			t.Errorf("window %d = %v..%v, want %v..%v", i, ws[i].From, ws[i].To, want.From, want.To)
		}
	}

	// Unaligned bounds are clipped, not rounded.
	ws = usgsWindows(time.Date(1905, 6, 1, 12, 0, 0, 0, time.UTC), d(1960, 6, 1))
	if len(ws) != 7 || !ws[0].From.Equal(time.Date(1905, 6, 1, 12, 0, 0, 0, time.UTC)) || !ws[0].To.Equal(d(1910, 1, 1)) ||
		!ws[6].From.Equal(d(1960, 1, 1)) || !ws[6].To.Equal(d(1960, 6, 1)) {
		t.Errorf("unaligned windows = %v", ws)
	}
	ws = usgsWindows(d(2026, 10, 16), d(2026, 11, 15))
	if len(ws) != 2 || !ws[0].To.Equal(d(2026, 11, 1)) {
		t.Errorf("since-30d windows = %v", ws)
	}
	if ws := usgsWindows(d(2000, 1, 1), d(2000, 1, 1)); len(ws) != 0 {
		t.Errorf("empty range gave %v", ws)
	}
	// A non-UTC input is treated as the same instant in UTC.
	est := time.FixedZone("EST", -5*3600)
	ws = usgsWindows(time.Date(1999, 12, 31, 19, 0, 0, 0, est), d(2000, 2, 1))
	if len(ws) != 1 || !ws[0].From.Equal(d(2000, 1, 1)) || ws[0].From.Location() != time.UTC {
		t.Errorf("zoned windows = %v", ws)
	}
}

func TestSplitByCount(t *testing.T) {
	// 10 events spread over one day; at most 3 per window.
	var events []time.Time
	for i := range 10 {
		events = append(events, d(2000, 1, 1).Add(time.Duration(i)*2*time.Hour+time.Minute))
	}
	count := func(_ context.Context, w ingest.Window) (int, error) {
		n := 0
		for _, e := range events {
			if !e.Before(w.From) && e.Before(w.To) {
				n++
			}
		}
		return n, nil
	}
	var visited []ingest.Window
	total := 0
	err := splitByCount(context.Background(), ingest.Window{From: d(2000, 1, 1), To: d(2000, 1, 2)}, 3, count,
		func(w ingest.Window, n int) error {
			if n > 3 || n == 0 {
				t.Errorf("visited %v with count %d", w, n)
			}
			visited = append(visited, w)
			total += n
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if total != 10 {
		t.Errorf("visited windows hold %d events, want 10", total)
	}
	for i := 1; i < len(visited); i++ {
		if visited[i].From.Before(visited[i-1].To) {
			t.Errorf("windows out of order/overlapping: %v then %v", visited[i-1], visited[i])
		}
	}

	// A window that cannot be split below the limit is an error, not a loop.
	always := func(context.Context, ingest.Window) (int, error) { return 100, nil }
	err = splitByCount(context.Background(), ingest.Window{From: d(2000, 1, 1), To: d(2000, 1, 2)}, 3, always,
		func(ingest.Window, int) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "cannot split") {
		t.Errorf("unsplittable window err = %v", err)
	}

	// Count errors propagate.
	boom := errors.New("boom")
	err = splitByCount(context.Background(), ingest.Window{From: d(2000, 1, 1), To: d(2000, 1, 2)}, 3,
		func(context.Context, ingest.Window) (int, error) { return 0, boom }, func(ingest.Window, int) error { return nil })
	if !errors.Is(err, boom) {
		t.Errorf("count error = %v", err)
	}
}

// usgsServer fakes the FDSN count and query endpoints. Every window has
// count `perWindow` except windows starting in February, which have 0.
type usgsServer struct {
	mu      sync.Mutex
	counts  []string // RawQuery of each /count call
	queries []string // RawQuery of each /query call
}

func (s *usgsServer) handler(t *testing.T, fixture []byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		q := r.URL.Query()
		switch r.URL.Path {
		case "/count":
			s.counts = append(s.counts, r.URL.RawQuery)
			n := 11
			if strings.HasPrefix(q.Get("starttime"), "2011-02") {
				n = 0
			}
			fmt.Fprintf(w, `{"count":%d,"maxAllowed":20000}`, n)
		case "/query":
			s.queries = append(s.queries, r.URL.RawQuery)
			w.Write(fixture)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
}

func TestUSGSFetchEndToEnd(t *testing.T) {
	fixture, err := os.ReadFile(usgsFixture)
	if err != nil {
		t.Fatal(err)
	}
	srv := &usgsServer{}
	ts := httptest.NewServer(srv.handler(t, fixture))
	defer ts.Close()

	f := fetch.New(ts.Client(), t.TempDir(), quietLog())
	s := NewUSGS(f, 4.5, quietLog())
	s.BaseURL = ts.URL
	if s.Name() != "usgs" {
		t.Errorf("Name = %q", s.Name())
	}
	if f.Limiter != nil {
		t.Errorf("NewUSGS changed the caller's Fetcher")
	}
	if s.F == f || s.F.Limiter == nil {
		t.Errorf("NewUSGS must use a copy of the Fetcher with a limiter")
	}

	w := ingest.Window{From: d(2011, 2, 1), To: d(2011, 4, 1)} // Feb (count 0) + Mar (count 11)
	var got []domain.Incident
	collect := func(inc domain.Incident) error { got = append(got, inc); return nil }
	if err := s.Fetch(context.Background(), w, collect); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got) != 11 {
		t.Fatalf("emitted %d, want 11", len(got))
	}
	if len(srv.counts) != 2 || len(srv.queries) != 1 {
		t.Fatalf("calls: %d count, %d query; want 2, 1 (no query for an empty window)", len(srv.counts), len(srv.queries))
	}
	wantQ := "format=geojson&starttime=2011-03-01T00:00:00&endtime=2011-04-01T00:00:00&minmagnitude=4.5&orderby=time-asc&limit=20000"
	if srv.queries[0] != wantQ {
		t.Errorf("query = %q\nwant    %q", srv.queries[0], wantQ)
	}
	wantC := "format=geojson&starttime=2011-02-01T00:00:00&endtime=2011-03-01T00:00:00&minmagnitude=4.5"
	if srv.counts[0] != wantC {
		t.Errorf("count = %q\nwant    %q", srv.counts[0], wantC)
	}

	// A second run is served from the disk cache: no new network calls.
	got = nil
	if err := s.Fetch(context.Background(), w, collect); err != nil {
		t.Fatalf("second Fetch: %v", err)
	}
	if len(got) != 11 || len(srv.counts) != 2 || len(srv.queries) != 1 {
		t.Errorf("second run: emitted %d, calls %d/%d; want 11 from cache, 2/1", len(got), len(srv.counts), len(srv.queries))
	}
}

func TestUSGSFetchStopsOnCancel(t *testing.T) {
	fixture, err := os.ReadFile(usgsFixture)
	if err != nil {
		t.Fatal(err)
	}
	srv := &usgsServer{}
	ts := httptest.NewServer(srv.handler(t, fixture))
	defer ts.Close()
	s := NewUSGS(fetch.New(ts.Client(), t.TempDir(), quietLog()), 4.5, quietLog())
	s.BaseURL = ts.URL
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = s.Fetch(ctx, ingest.Window{From: d(2011, 3, 1), To: d(2011, 6, 1)}, func(domain.Incident) error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
