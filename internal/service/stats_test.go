package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/dwts/backend/internal/domain"
)

// stubReader is a store.StatsReader whose version and data tests can change.
type stubReader struct {
	mu       sync.Mutex
	version  int64
	counts   []domain.CountRow
	totals   []domain.TotalRow
	coverage []domain.Coverage
	loads    int // number of LoadCounts calls
	failNext bool
}

func (r *stubReader) DatasetVersion(context.Context) (int64, time.Time, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failNext {
		r.failNext = false
		return 0, time.Time{}, errors.New("db down")
	}
	return r.version, time.Date(2026, 11, 16, 9, 12, 0, 0, time.UTC), nil
}
func (r *stubReader) LoadCounts(context.Context) ([]domain.CountRow, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.loads++
	return r.counts, nil
}
func (r *stubReader) LoadTotals(context.Context) ([]domain.TotalRow, error) { return r.totals, nil }
func (r *stubReader) Coverage(context.Context) ([]domain.Coverage, error)   { return r.coverage, nil }
func (r *stubReader) Countries(context.Context) ([]domain.Country, error)   { return nil, nil }

func (r *stubReader) set(v int64, counts []domain.CountRow) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.version, r.counts = v, counts
}

func (r *stubReader) loadCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loads
}

func newTestService(tb testing.TB, r *stubReader) *StatsService {
	tb.Helper()
	s := NewStatsService(r, nil, 1900, map[string]bool{"chat": false, "stories": true},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.now = func() time.Time { return time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC) }
	if _, err := s.Reload(context.Background()); err != nil {
		tb.Fatal(err)
	}
	return s
}

func sampleReader() *stubReader {
	return &stubReader{
		version: 42,
		counts: []domain.CountRow{
			{Type: domain.Earthquake, ISO3: "JPN", Year: 1960, N: 100},
			{Type: domain.Earthquake, ISO3: "IDN", Year: 1990, N: 80},
			{Type: domain.Earthquake, ISO3: "", Year: 1990, N: 500},
			{Type: domain.Storm, ISO3: "PHL", Year: 1990, N: 10},
			{Type: domain.Storm, ISO3: "JPN", Year: 1990, N: 4},
			{Type: domain.Flood, ISO3: "PHL", Year: 1995, N: 6},
		},
		totals: []domain.TotalRow{
			{Type: domain.Earthquake, Year: 1960, N: 100},
			{Type: domain.Earthquake, Year: 1990, N: 580},
			{Type: domain.Storm, Year: 1990, N: 12}, // one storm hit both PHL and JPN → distinct < sum
			{Type: domain.Flood, Year: 1995, N: 6},
		},
		coverage: []domain.Coverage{
			{Type: domain.Flood, FromYear: 1985, ToYear: 2026, Sources: []string{"dfo", "gdacs"}, Note: "DFO 1985–2021"},
			{Type: domain.Earthquake, FromYear: 1900, ToYear: 2026, Sources: []string{"usgs"}, Note: "USGS M≥4.5"},
		},
	}
}

func TestChoropleth(t *testing.T) {
	s := newTestService(t, sampleReader())
	ch, err := s.Choropleth(context.Background(), domain.ChoroplethQuery{
		Types: []domain.IncidentType{domain.Earthquake, domain.Storm, domain.Flood}, Years: domain.YearRange{From: 1950, To: 2000}})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{"JPN": 104, "IDN": 80, "PHL": 16}
	if len(ch.Counts) != len(want) {
		t.Fatalf("counts %v", ch.Counts)
	}
	for k, v := range want {
		if ch.Counts[k] != v {
			t.Errorf("%s=%d want %d", k, ch.Counts[k], v)
		}
	}
	if ch.Ocean != 500 || ch.Total != 698 || ch.Max != 104 || ch.Level != "country" || ch.Unit != "incidents" || ch.DatasetVersion != 42 {
		t.Errorf("choropleth %+v", ch)
	}
	if len(ch.Coverage) != 1 || ch.Coverage[0].Type != domain.Flood || ch.Coverage[0].Note != "1950–1984 not covered" {
		t.Errorf("coverage gaps %+v", ch.Coverage)
	}
	if ch.Breaks[0] != 0 || ch.Breaks[len(ch.Breaks)-1] != 104 {
		t.Errorf("breaks %v", ch.Breaks)
	}
	// Distinct total: one storm in two countries → total 12 < Σ = 14.
	st, _ := s.Choropleth(context.Background(), domain.ChoroplethQuery{Types: []domain.IncidentType{domain.Storm}, Years: domain.YearRange{From: 1900, To: 2026}})
	if st.Total != 12 || st.Counts["PHL"]+st.Counts["JPN"] != 14 {
		t.Errorf("storm distinct total %d, counts %v", st.Total, st.Counts)
	}
	// fire_activity alone has unit detections; mixed is invalid.
	fa, err := s.Choropleth(context.Background(), domain.ChoroplethQuery{Types: []domain.IncidentType{domain.FireActivity}, Years: domain.YearRange{From: 2000, To: 2020}})
	if err != nil || fa.Unit != "detections" || len(fa.Counts) != 0 || len(fa.Breaks) != 1 {
		t.Errorf("fire alone: %+v %v", fa, err)
	}
	if _, err := s.Choropleth(context.Background(), domain.ChoroplethQuery{Types: []domain.IncidentType{domain.FireActivity, domain.Flood}}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("mixed units: %v", err)
	}
}

func TestTimeseries(t *testing.T) {
	s := newTestService(t, sampleReader())
	ts, err := s.Timeseries(context.Background(), domain.TimeseriesQuery{Types: []domain.IncidentType{domain.Storm}, ISO3: "phl", Years: domain.YearRange{From: 1989, To: 1991}})
	if err != nil || ts.ISO3 != "PHL" || len(ts.Points) != 3 || ts.Points[1] != (domain.YearCount{Year: 1990, N: 10}) {
		t.Errorf("country series %+v %v", ts, err)
	}
	world, _ := s.Timeseries(context.Background(), domain.TimeseriesQuery{Types: []domain.IncidentType{domain.Storm}, Years: domain.YearRange{From: 1990, To: 1990}})
	if world.Points[0].N != 12 { // world = DISTINCT totals
		t.Errorf("world series %+v", world.Points)
	}
	if _, err := s.Timeseries(context.Background(), domain.TimeseriesQuery{Types: []domain.IncidentType{domain.Storm}, ISO3: "PH"}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("bad iso3: %v", err)
	}
}

func TestMetaAndTypes(t *testing.T) {
	s := newTestService(t, sampleReader())
	m, err := s.Meta(context.Background())
	if err != nil || m.DatasetVersion != 42 || m.MinYear != 1900 || m.MaxYear != 2026 || !m.Features["stories"] || len(m.Coverage) != 2 {
		t.Fatalf("meta %+v %v", m, err)
	}
	if m.Coverage[0].Type != domain.Earthquake { // AllTypes order
		t.Errorf("coverage order %+v", m.Coverage)
	}
	for _, ti := range s.IncidentTypes() {
		if ti.ToYear != 2026 {
			t.Errorf("%s toYear %d", ti.ID, ti.ToYear)
		}
		if ti.ID == domain.Flood && ti.FromYear != 1985 {
			t.Errorf("flood fromYear %d", ti.FromYear)
		}
	}
}

func TestNotReady(t *testing.T) {
	s := NewStatsService(nil, nil, 1900, nil, nil)
	if s.Ready() || s.Version() != 0 {
		t.Fatal("must not be ready without a reader")
	}
	if err := s.Start(context.Background(), time.Second); !errors.Is(err, domain.ErrNotReady) {
		t.Errorf("Start without reader: %v", err)
	}
	if _, err := s.Meta(context.Background()); !errors.Is(err, domain.ErrNotReady) {
		t.Errorf("Meta: %v", err)
	}
	if _, err := s.Choropleth(context.Background(), domain.ChoroplethQuery{Types: []domain.IncidentType{domain.Flood}}); !errors.Is(err, domain.ErrNotReady) {
		t.Errorf("Choropleth: %v", err)
	}
	if len(s.IncidentTypes()) != len(domain.AllTypes) {
		t.Error("IncidentTypes must work without data")
	}
	if _, err := s.Grid(context.Background(), domain.GridQuery{}); !errors.Is(err, ErrNotImplemented) {
		t.Errorf("Grid: %v", err)
	}
}

func TestReloadSwapsOnlyOnVersionChange(t *testing.T) {
	r := sampleReader()
	s := newTestService(t, r)
	if r.loadCount() != 1 {
		t.Fatalf("loads=%d", r.loadCount())
	}
	ctx := context.Background()
	if changed, err := s.Reload(ctx); changed || err != nil || r.loadCount() != 1 {
		t.Fatalf("same version must not reload: %v %v loads=%d", changed, err, r.loadCount())
	}
	// A failed version check keeps the old cube.
	r.mu.Lock()
	r.failNext = true
	r.mu.Unlock()
	if _, err := s.Reload(ctx); err == nil || s.Version() != 42 {
		t.Fatalf("failed reload must keep cube v42: %v v=%d", err, s.Version())
	}
	// New version → rebuild and swap.
	r.set(43, []domain.CountRow{{Type: domain.Earthquake, ISO3: "CHL", Year: 2010, N: 9}})
	if changed, err := s.Reload(ctx); !changed || err != nil || s.Version() != 43 {
		t.Fatalf("version change must swap: %v %v v=%d", changed, err, s.Version())
	}
	ch, _ := s.Choropleth(ctx, domain.ChoroplethQuery{Types: []domain.IncidentType{domain.Earthquake}, Years: domain.YearRange{From: 1900, To: 2026}})
	if ch.Counts["CHL"] != 9 || ch.Counts["JPN"] != 0 || ch.DatasetVersion != 43 {
		t.Errorf("after swap: %+v", ch)
	}
}

func TestStartReloadLoop(t *testing.T) {
	r := sampleReader()
	s := NewStatsService(r, nil, 1900, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx, 5*time.Millisecond); err != nil || !s.Ready() {
		t.Fatalf("start: %v ready=%v", err, s.Ready())
	}
	r.set(50, nil)
	deadline := time.Now().Add(2 * time.Second)
	for s.Version() != 50 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if s.Version() != 50 {
		t.Fatalf("loop did not pick up the new version, v=%d", s.Version())
	}
}
