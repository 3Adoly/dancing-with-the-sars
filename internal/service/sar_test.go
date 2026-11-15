package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dwts/backend/internal/config"
	"github.com/dwts/backend/internal/domain"
	"github.com/dwts/backend/internal/external/asf"
)

// --- stubs ---

type incStub map[int64]domain.Incident

func (m incStub) GetIncident(_ context.Context, id int64) (domain.Incident, error) {
	if inc, ok := m[id]; ok {
		return inc, nil
	}
	return domain.Incident{}, fmt.Errorf("%d: %w", id, domain.ErrNotFound)
}
func (m incStub) ListIncidents(context.Context, domain.IncidentFilter) ([]domain.IncidentSummary, string, error) {
	return nil, "", nil
}
func (m incStub) Search(context.Context, domain.SearchQuery) ([]domain.IncidentSummary, error) {
	return nil, nil
}
func (m incStub) Grid(context.Context, domain.GridQuery) ([]domain.GridCell, error) { return nil, nil }
func (m incStub) FindBySource(context.Context, string, string) (int64, error)       { return 0, nil }

// asfStub serves the recorded real ASF fixtures per mission and counts calls.
type asfStub struct {
	calls   atomic.Int32
	byParam map[string][]asf.Scene // key = platform or dataset value
	err     error
	delay   time.Duration
	gate    chan struct{} // if set, Search blocks until closed
}

func (a *asfStub) Search(ctx context.Context, q asf.Query) ([]asf.Scene, error) {
	a.calls.Add(1)
	if a.gate != nil {
		<-a.gate
	}
	if a.delay > 0 {
		select {
		case <-time.After(a.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if a.err != nil {
		return nil, a.err
	}
	k := q.Params["platform"]
	if k == "" {
		k = q.Params["dataset"]
	}
	// Only return scenes inside the requested window, like ASF does.
	var out []asf.Scene
	for _, s := range a.byParam[k] {
		if !s.StartTime.Before(q.Start) && !s.StartTime.After(q.End) {
			out = append(out, s)
		}
	}
	return out, nil
}

type memCache struct {
	mu sync.Mutex
	m  map[int64]domain.SARResult
}

func (c *memCache) GetSAR(_ context.Context, id int64) (domain.SARResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r, ok := c.m[id]; ok {
		return r, nil
	}
	return domain.SARResult{}, domain.ErrNotFound
}
func (c *memCache) PutSAR(_ context.Context, r domain.SARResult) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[r.IncidentID] = r
	return nil
}

func loadScenes(t testing.TB, name string) []asf.Scene {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "testdata", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s, err := asf.Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

var (
	tTurkiye         = time.Date(2023, 2, 6, 1, 17, 34, 0, time.UTC)
	tAomori          = time.Date(2025, 12, 8, 14, 15, 9, 0, time.UTC)
	fixtureIncidents = incStub{
		500812: {ID: 500812, Type: domain.Earthquake, StartTime: tTurkiye, Lon: 37.0143, Lat: 37.2256},
		700001: {ID: 700001, Type: domain.Earthquake, StartTime: tAomori, Lon: 142.1836, Lat: 40.9984},
		118233: {ID: 118233, Type: domain.Volcano, StartTime: time.Date(1991, 6, 15, 0, 0, 0, 0, time.UTC), Lon: 120.35, Lat: 15.13},
		400101: {ID: 400101, Type: domain.Earthquake, StartTime: time.Date(2013, 10, 15, 0, 12, 32, 0, time.UTC), Lon: 124.1167, Lat: 9.8796},
		612004: {ID: 612004, Type: domain.Flood, StartTime: time.Date(2022, 8, 25, 0, 0, 0, 0, time.UTC), Lon: 68.25, Lat: 27},
		777777: {ID: 777777, Type: domain.Earthquake, StartTime: time.Date(1978, 9, 16, 15, 35, 0, 0, time.UTC), Lon: 57.4, Lat: 33.2}, // Tabas, Seasat window
	}
)

func newSAR(t testing.TB, a SARSearcher, cache *memCache) *SARService {
	t.Helper()
	var c *memCache
	if cache != nil {
		c = cache
	}
	cfg := SARConfig{WindowDays: 45, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	var s *SARService
	if c != nil {
		s = NewSARService(fixtureIncidents, c, a, config.SARMissions(), nil, cfg)
	} else {
		s = NewSARService(fixtureIncidents, nil, a, config.SARMissions(), nil, cfg)
	}
	s.now = func() time.Time { return time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC) }
	return s
}

func realASF(t testing.TB) *asfStub {
	return &asfStub{byParam: map[string][]asf.Scene{
		"SENTINEL-1": loadScenes(t, "asf_sample.json"),
		"NISAR":      loadScenes(t, "asf_nisar_sample.json"),
	}}
}

// --- tests ---

func TestEraRuleNoASFCall(t *testing.T) {
	a := realASF(t)
	s := newSAR(t, a, nil)
	cases := []struct {
		id       int64
		coverage string
	}{
		{118233, domain.SARCoveragePreSAREra}, // Pinatubo 1991
		{400101, domain.SARCoverageNone},      // Bohol 2013: ALOS ended 2011, S1 from 2014
	}
	for _, c := range cases {
		r, err := s.ForIncident(context.Background(), c.id, false)
		if err != nil || r.Coverage != c.coverage || r.Reason != "no_searchable_mission_in_window" ||
			len(r.MissionsInWindow) != 0 || r.Before == nil || r.After == nil || r.ASFSearchURL == "" {
			t.Errorf("%d: %+v %v", c.id, r, err)
		}
	}
	if a.calls.Load() != 0 {
		t.Fatalf("era rule must not call ASF, got %d calls", a.calls.Load())
	}
	if got := s.Availability(time.Date(2013, 10, 15, 0, 0, 0, 0, time.UTC)); got != "gap" {
		t.Errorf("availability 2013 = %s", got)
	}
	if got := s.Availability(time.Date(1991, 6, 15, 0, 0, 0, 0, time.UTC)); got != "pre_sar_era" {
		t.Errorf("availability 1991 = %s", got)
	}
	if got := s.Availability(tTurkiye); got != "searchable" {
		t.Errorf("availability 2023 = %s", got)
	}
}

func TestMissionsInWindow(t *testing.T) {
	s := newSAR(t, realASF(t), nil)
	ids := func(a, b time.Time) string {
		var out []string
		for _, m := range s.missionsInWindow(a, b) {
			out = append(out, m.ID)
		}
		return strings.Join(out, ",")
	}
	d := func(y, m, dd int) time.Time { return time.Date(y, time.Month(m), dd, 0, 0, 0, 0, time.UTC) }
	cases := map[string][2]time.Time{
		"seasat":          {d(1978, 8, 1), d(1978, 11, 1)},
		"":                {d(1991, 5, 1), d(1991, 7, 30)},
		"alos":            {d(2011, 3, 1), d(2011, 5, 1)}, // ALOS ends 2011-04-21 (inclusive)
		"sentinel1":       {d(2023, 1, 1), d(2023, 3, 1)},
		"sentinel1,nisar": {d(2025, 10, 1), d(2025, 12, 1)},
	}
	for want, w := range cases {
		if got := ids(w[0], w[1]); got != want {
			t.Errorf("%v: got %q want %q", w, got, want)
		}
	}
}

func TestSentinel1SplitAndBestPair(t *testing.T) {
	a := realASF(t)
	s := newSAR(t, a, nil)
	r, err := s.ForIncident(context.Background(), 500812, false)
	if err != nil {
		t.Fatal(err)
	}
	if r.Coverage != domain.SARCoverageOK || len(r.MissionsInWindow) != 1 || r.MissionsInWindow[0] != "sentinel1" {
		t.Fatalf("result %+v", r)
	}
	if len(r.Before) != 10 || len(r.After) != 8 { // 12 before (kept 10) + 8 after in the real response
		t.Errorf("before=%d after=%d", len(r.Before), len(r.After))
	}
	for i := range r.Before {
		if !r.Before[i].StartTime.Before(tTurkiye) || (i > 0 && r.Before[i].StartTime.After(r.Before[i-1].StartTime)) {
			t.Fatal("before must be < t and newest first")
		}
	}
	for i := range r.After {
		if r.After[i].StartTime.Before(tTurkiye) || (i > 0 && r.After[i].StartTime.Before(r.After[i-1].StartTime)) {
			t.Fatal("after must be >= t and oldest first")
		}
	}
	if r.BestPair == nil {
		t.Fatal("expected a best pair")
	}
	var b, af domain.SARScene
	for _, x := range r.Before {
		if x.GranuleID == r.BestPair.Before {
			b = x
		}
	}
	for _, x := range r.After {
		if x.GranuleID == r.BestPair.After {
			af = x
		}
	}
	if b.Path != af.Path || b.Frame != af.Frame || b.FlightDirection != af.FlightDirection || b.Mission != "sentinel1" {
		t.Errorf("best pair not on the same track: %+v / %+v", b, af)
	}
	t.Logf("bestPair %s → %s (path %d frame %d)", b.StartTime.Format(time.DateOnly), af.StartTime.Format(time.DateOnly), b.Path, b.Frame)
	if !strings.Contains(r.ASFSearchURL, "search.asf.alaska.edu") || r.FetchedAt.IsZero() {
		t.Errorf("url/fetchedAt: %+v", r)
	}
}

func TestNISAREraQueriesBothMissions(t *testing.T) {
	a := realASF(t)
	s := newSAR(t, a, nil)
	r, err := s.ForIncident(context.Background(), 700001, false)
	if err != nil {
		t.Fatal(err)
	}
	if a.calls.Load() != 2 || strings.Join(r.MissionsInWindow, ",") != "sentinel1,nisar" {
		t.Errorf("calls=%d missions=%v", a.calls.Load(), r.MissionsInWindow)
	}
	nisar := 0
	for _, x := range append(append([]domain.SARScene{}, r.Before...), r.After...) {
		if x.Mission == "nisar" {
			nisar++
			if x.Polarization == "" || x.BrowseURL == "" {
				t.Errorf("NISAR scene missing pol/browse: %+v", x)
			}
		}
	}
	if nisar == 0 || r.Coverage != domain.SARCoverageOK {
		t.Errorf("nisar scenes=%d coverage=%s", nisar, r.Coverage)
	}
}

func TestBuildSARResultCoverageAndDedup(t *testing.T) {
	inc := domain.Incident{ID: 1, StartTime: tTurkiye}
	sc := func(id string, dt time.Duration) domain.SARScene {
		return domain.SARScene{GranuleID: id, Mission: "sentinel1", Path: 1, Frame: 2, FlightDirection: "ASCENDING", StartTime: tTurkiye.Add(dt)}
	}
	if r := BuildSARResult(inc, 45, nil, nil, 10); r.Coverage != domain.SARCoverageNone || r.Reason != "no_scenes_found" || r.BestPair != nil {
		t.Errorf("none: %+v", r)
	}
	if r := BuildSARResult(inc, 45, nil, []domain.SARScene{sc("a", -time.Hour), sc("a", -time.Hour)}, 10); r.Coverage != domain.SARCoverageBeforeOnly || len(r.Before) != 1 {
		t.Errorf("before_only/dedup: %+v", r)
	}
	if r := BuildSARResult(inc, 45, nil, []domain.SARScene{sc("b", 0)}, 10); r.Coverage != domain.SARCoverageAfterOnly {
		t.Errorf("scene at t counts as after: %+v", r)
	}
	// bestPair prefers browse on both sides over a smaller gap.
	b1, b2, a1 := sc("b1", -time.Hour), sc("b2", -48*time.Hour), sc("a1", time.Hour)
	b2.BrowseURL, a1.BrowseURL = "x", "y"
	other := sc("a2", 30*time.Minute)
	other.Path = 9 // different track: never paired
	r := BuildSARResult(inc, 45, nil, []domain.SARScene{b1, b2, a1, other}, 10)
	if r.BestPair == nil || r.BestPair.Before != "b2" || r.BestPair.After != "a1" {
		t.Errorf("bestPair %+v", r.BestPair)
	}
}

// The challenge is about NISAR: a NISAR pair must win even when a Sentinel-1 pair is closer
// in time and has browse images while the NISAR pair does not.
func TestBestPairPrefersNISAR(t *testing.T) {
	inc := domain.Incident{ID: 1, StartTime: tTurkiye}
	sc := func(id, mission string, path int, dt time.Duration, browse string) domain.SARScene {
		return domain.SARScene{GranuleID: id, Mission: mission, Path: path, Frame: 2, FlightDirection: "ASCENDING",
			StartTime: tTurkiye.Add(dt), BrowseURL: browse}
	}
	scenes := []domain.SARScene{
		sc("s1-before", "sentinel1", 1, -time.Hour, "x"), sc("s1-after", "sentinel1", 1, time.Hour, "y"),
		sc("ni-before", "nisar", 7, -10*24*time.Hour, ""), sc("ni-after", "nisar", 7, 10*24*time.Hour, ""),
	}
	r := BuildSARResult(inc, 45, nil, scenes, 10)
	if r.BestPair == nil || r.BestPair.Before != "ni-before" || r.BestPair.After != "ni-after" {
		t.Errorf("bestPair should be the NISAR pair, got %+v", r.BestPair)
	}
	// Without a NISAR pair, the old ranking (browse, then gap) still applies.
	r = BuildSARResult(inc, 45, nil, scenes[:2], 10)
	if r.BestPair == nil || r.BestPair.Before != "s1-before" {
		t.Errorf("fallback bestPair %+v", r.BestPair)
	}
}

func TestFloodUsesBoxWKT(t *testing.T) {
	if w := searchWKT(fixtureIncidents[612004]); !strings.HasPrefix(w, "POLYGON((68.1250 26.8750,68.3750 26.8750") {
		t.Errorf("flood wkt %s", w)
	}
	if w := searchWKT(fixtureIncidents[500812]); w != "POINT(37.0143 37.2256)" {
		t.Errorf("quake wkt %s", w)
	}
}

func TestCacheLayersAndStaleFallback(t *testing.T) {
	ctx := context.Background()
	a := realASF(t)
	cache := &memCache{m: map[int64]domain.SARResult{}}
	s := newSAR(t, a, cache)

	r1, _ := s.ForIncident(ctx, 500812, false)
	if a.calls.Load() != 1 || cache.m[500812].IncidentID != 500812 {
		t.Fatalf("first call must hit ASF and persist; calls=%d", a.calls.Load())
	}
	r2, _ := s.ForIncident(ctx, 500812, false) // memory hit
	if a.calls.Load() != 1 || r2.FetchedAt != r1.FetchedAt {
		t.Fatalf("memory cache miss; calls=%d", a.calls.Load())
	}

	// New service instance (restart): memory empty, DB row fresh → no ASF call.
	s2 := newSAR(t, a, cache)
	if _, err := s2.ForIncident(ctx, 500812, false); err != nil || a.calls.Load() != 1 {
		t.Fatalf("fresh DB row should be served; calls=%d err=%v", a.calls.Load(), err)
	}

	// Stale DB row → served immediately with stale=true, refreshed in the background.
	old := cache.m[500812]
	old.FetchedAt = old.FetchedAt.Add(-10 * 24 * time.Hour)
	cache.m[500812] = old
	s3 := newSAR(t, a, cache)
	r3, err := s3.ForIncident(ctx, 500812, false)
	if err != nil || !r3.Stale {
		t.Fatalf("stale row must be served stale: %+v %v", r3.Stale, err)
	}
	s3.bg.Wait()
	if a.calls.Load() != 2 || cache.m[500812].FetchedAt.Equal(old.FetchedAt) {
		t.Fatalf("background refresh should have run; calls=%d", a.calls.Load())
	}

	// ASF down + refresh=true → cached value with stale=true, reason upstream_error.
	a.err = &asf.HTTPError{Status: 503}
	r4, err := s3.ForIncident(ctx, 500812, true)
	if err != nil || !r4.Stale || r4.Reason != "upstream_error" || r4.Coverage != domain.SARCoverageOK {
		t.Fatalf("fallback: %+v %v", r4, err)
	}
}

func TestASFErrorsWithoutCache(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		err  error
		want error
	}{
		{fmt.Errorf("x: %w", asf.ErrUpstream), domain.ErrUpstream},
		{fmt.Errorf("x: %w", asf.ErrRateLimited), domain.ErrUpstream},
		{fmt.Errorf("x: %w", asf.ErrTimeout), domain.ErrUpstreamTimeout},
	}
	for _, c := range cases {
		s := newSAR(t, &asfStub{err: c.err}, nil)
		if _, err := s.ForIncident(ctx, 500812, false); !errors.Is(err, c.want) {
			t.Errorf("%v → %v want %v", c.err, err, c.want)
		}
	}
	// One mission failing while another succeeds is not an error (NISAR era: S1 + NISAR).
	part := &partialStub{ok: realASF(t)}
	s := newSAR(t, part, nil)
	r, err := s.ForIncident(ctx, 700001, false)
	// S1 succeeded (no 2025 scenes in the stub) and NISAR failed → a normal result, not an error.
	if err != nil || r.Coverage != domain.SARCoverageNone || len(r.MissionsInWindow) != 2 {
		t.Errorf("partial failure: %+v %v", r, err)
	}
}

type partialStub struct{ ok *asfStub }

func (p *partialStub) Search(ctx context.Context, q asf.Query) ([]asf.Scene, error) {
	if q.Params["dataset"] == "NISAR" {
		return nil, asf.ErrUpstream
	}
	return p.ok.Search(ctx, q)
}

func TestSingleflightDedup(t *testing.T) {
	a := realASF(t)
	a.gate = make(chan struct{})
	s := newSAR(t, a, nil)
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.ForIncident(context.Background(), 500812, true)
			errs <- err
		}()
	}
	time.Sleep(50 * time.Millisecond) // let all goroutines join the in-flight call
	close(a.gate)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := a.calls.Load(); n != 1 {
		t.Errorf("singleflight: %d ASF calls, want 1", n)
	}
}

func TestWindowValidationAndNotFound(t *testing.T) {
	s := newSAR(t, realASF(t), nil)
	for _, w := range []int{0, 181} {
		if _, err := s.ForIncidentWindow(context.Background(), 500812, w, false); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("window %d: %v", w, err)
		}
	}
	if _, err := s.ForIncident(context.Background(), 999, false); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown incident: %v", err)
	}
	none := NewSARService(nil, nil, nil, config.SARMissions(), nil, SARConfig{})
	if _, err := none.ForIncident(context.Background(), 1, false); !errors.Is(err, domain.ErrNotReady) {
		t.Errorf("no incident source: %v", err)
	}
	// A shorter window is cached separately and not persisted.
	r, err := s.ForIncidentWindow(context.Background(), 500812, 10, false)
	if err != nil || r.WindowDays != 10 || len(r.Before)+len(r.After) >= 18 {
		t.Errorf("10-day window: %d scenes, %v", len(r.Before)+len(r.After), err)
	}
}

func TestSeasatWindow(t *testing.T) {
	a := &asfStub{}
	s := newSAR(t, a, nil)
	r, err := s.ForIncident(context.Background(), 777777, false)
	if err != nil || strings.Join(r.MissionsInWindow, ",") != "seasat" || a.calls.Load() != 1 || r.Coverage != domain.SARCoverageNone {
		t.Errorf("seasat-era event should query SEASAT: %+v %v calls=%d", r, err, a.calls.Load())
	}
}

func TestCoverageTimeline(t *testing.T) {
	s := newSAR(t, realASF(t), nil)
	tl := s.CoverageTimeline()
	if len(tl.Missions) != len(config.SARMissions()) || tl.ByYear[0].Year != 1978 || tl.ByYear[len(tl.ByYear)-1].Year != 2026 {
		t.Fatalf("timeline shape %d missions, years %d..%d", len(tl.Missions), tl.ByYear[0].Year, tl.ByYear[len(tl.ByYear)-1].Year)
	}
	by := map[int]string{}
	for _, y := range tl.ByYear {
		by[y.Year] = strings.Join(y.Missions, ",")
	}
	if by[1978] != "seasat" || by[1979] != "" || !strings.Contains(by[2026], "nisar") || !strings.Contains(by[2026], "saocom") || !strings.Contains(by[2007], "alos") {
		t.Errorf("by year: 1978=%q 1979=%q 2007=%q 2026=%q", by[1978], by[1979], by[2007], by[2026])
	}
}

func TestTTLCache(t *testing.T) {
	c := newTTLCache[int, string](2, time.Minute)
	now := time.Now()
	c.now = func() time.Time { return now }
	c.Put(1, "a")
	c.Put(2, "b")
	c.Get(1)      // 1 becomes most recent
	c.Put(3, "c") // evicts 2
	if _, ok := c.Get(2); ok || c.Len() != 2 {
		t.Error("LRU eviction failed")
	}
	if v, ok := c.Get(1); !ok || v != "a" {
		t.Error("1 should survive")
	}
	now = now.Add(2 * time.Minute)
	if _, ok := c.Get(1); ok {
		t.Error("expired entry returned")
	}
}
