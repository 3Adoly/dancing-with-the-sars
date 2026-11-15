package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dwts/backend/internal/domain"
	"github.com/dwts/backend/internal/store"
)

// ErrNotImplemented marks service methods whose task has not landed yet (e.g. Grid → A8).
var ErrNotImplemented = errors.New("not implemented yet")

// snapshot is everything the stats endpoints need, swapped atomically as one unit.
type snapshot struct {
	cube      *Cube
	coverage  map[domain.IncidentType]domain.Coverage
	updatedAt time.Time
}

// StatsService serves E2–E5 from an in-memory Cube that is reloaded when the dataset version changes.
type StatsService struct {
	st       store.StatsReader    // nil = no data source (service stays not ready)
	ir       store.IncidentReader // used by Grid (A8); may be nil
	minYear  int
	features map[string]bool
	log      *slog.Logger
	now      func() time.Time

	snap   atomic.Pointer[snapshot]
	loadMu sync.Mutex // serializes reloads
}

// NewStatsService creates the service. st may be nil (no DB / no fixture): the service then
// reports Ready()=false and E2/E4/E5 answer 503 not_ready, while IncidentTypes still works.
func NewStatsService(st store.StatsReader, ir store.IncidentReader, minYear int, features map[string]bool, log *slog.Logger) *StatsService {
	if log == nil {
		log = slog.Default()
	}
	f := maps.Clone(features)
	if f == nil {
		f = map[string]bool{}
	}
	return &StatsService{st: st, ir: ir, minYear: minYear, features: f, log: log, now: time.Now}
}

// Start performs the first load (blocking) and then reloads every `every` in a goroutine until ctx
// is done. The loop runs even if the first load failed (e.g. DB not up yet), so the service becomes
// ready as soon as data is reachable. The returned error is the first load's error, for logging.
func (s *StatsService) Start(ctx context.Context, every time.Duration) error {
	if s.st == nil {
		return fmt.Errorf("stats: no data source configured: %w", domain.ErrNotReady)
	}
	_, err := s.Reload(ctx)
	if every > 0 {
		go func() {
			t := time.NewTicker(every)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					if _, err := s.Reload(ctx); err != nil && ctx.Err() == nil {
						s.log.Warn("stats reload failed; keeping previous cube", "err", err)
					}
				}
			}
		}()
	}
	return err
}

// Reload checks DatasetVersion and rebuilds the cube only when it changed. It returns whether a
// new cube was swapped in. On error the previous cube stays in place.
func (s *StatsService) Reload(ctx context.Context) (bool, error) {
	if s.st == nil {
		return false, domain.ErrNotReady
	}
	s.loadMu.Lock()
	defer s.loadMu.Unlock()

	v, updatedAt, err := s.st.DatasetVersion(ctx)
	if err != nil {
		return false, fmt.Errorf("dataset version: %w", err)
	}
	if cur := s.snap.Load(); cur != nil && cur.cube.Version == v {
		return false, nil
	}
	start := time.Now()
	counts, err := s.st.LoadCounts(ctx)
	if err != nil {
		return false, fmt.Errorf("load counts: %w", err)
	}
	totals, err := s.st.LoadTotals(ctx)
	if err != nil {
		return false, fmt.Errorf("load totals: %w", err)
	}
	cov, err := s.st.Coverage(ctx)
	if err != nil {
		return false, fmt.Errorf("load coverage: %w", err)
	}
	cube, err := BuildCube(v, s.minYear, max(s.minYear, s.now().UTC().Year()), counts, totals)
	if err != nil {
		return false, err
	}
	cm := make(map[domain.IncidentType]domain.Coverage, len(cov))
	for _, c := range cov {
		cm[c.Type] = c
	}
	s.snap.Store(&snapshot{cube: cube, coverage: cm, updatedAt: updatedAt})
	s.log.Info("stats cube loaded", "version", v, "countries", cube.Countries()-1, "rows", len(counts),
		"skipped", cube.Skipped, "ms", time.Since(start).Milliseconds())
	return true, nil
}

// Ready reports whether a cube is loaded.
func (s *StatsService) Ready() bool { return s.snap.Load() != nil }

// Version returns the loaded dataset version (0 if not ready).
func (s *StatsService) Version() int64 {
	if sn := s.snap.Load(); sn != nil {
		return sn.cube.Version
	}
	return 0
}

// YearBounds returns the queryable year range [minYear, current year].
func (s *StatsService) YearBounds() (int, int) {
	if sn := s.snap.Load(); sn != nil {
		return sn.cube.MinYear, sn.cube.MaxYear
	}
	return s.minYear, max(s.minYear, s.now().UTC().Year())
}

func (s *StatsService) ready() (*snapshot, error) {
	sn := s.snap.Load()
	if sn == nil {
		return nil, fmt.Errorf("stats cube not loaded: %w", domain.ErrNotReady)
	}
	return sn, nil
}

// Meta builds E2.
func (s *StatsService) Meta(ctx context.Context) (domain.Meta, error) {
	sn, err := s.ready()
	if err != nil {
		return domain.Meta{}, err
	}
	cov := make([]domain.Coverage, 0, len(sn.coverage))
	for _, t := range domain.AllTypes { // stable order
		if c, ok := sn.coverage[t]; ok {
			cov = append(cov, c)
		}
	}
	return domain.Meta{
		DatasetVersion: sn.cube.Version, UpdatedAt: sn.updatedAt.UTC(),
		MinYear: sn.cube.MinYear, MaxYear: sn.cube.MaxYear,
		Features: maps.Clone(s.features), Coverage: cov,
	}, nil
}

// IncidentTypes builds E3. It works without data: coverage (when loaded) refines fromYear.
func (s *StatsService) IncidentTypes() []domain.TypeInfo {
	_, maxYear := s.YearBounds()
	sn := s.snap.Load()
	out := domain.TypeCatalog()
	for i := range out {
		out[i].ToYear = maxYear
		if sn != nil {
			if c, ok := sn.coverage[out[i].ID]; ok && c.FromYear > 0 {
				out[i].FromYear = max(c.FromYear, s.minYear)
			}
		}
	}
	return out
}

// Choropleth builds E4. total = DISTINCT incidents; counts omit zero countries.
func (s *StatsService) Choropleth(ctx context.Context, q domain.ChoroplethQuery) (domain.Choropleth, error) {
	sn, err := s.ready()
	if err != nil {
		return domain.Choropleth{}, err
	}
	if err := validateTypes(q.Types); err != nil {
		return domain.Choropleth{}, err
	}
	c := sn.cube
	from, to, _ := c.ClampYears(q.Years.From, q.Years.To)
	per := c.Sum(q.Types, from, to)
	counts := make(map[string]int64, len(per))
	var maxN int64
	vals := make([]int64, 0, len(per))
	for i := 1; i < len(per); i++ {
		if per[i] > 0 {
			counts[c.ISO(i)] = per[i]
			vals = append(vals, per[i])
			maxN = max(maxN, per[i])
		}
	}
	return domain.Choropleth{
		Types: q.Types, From: from, To: to, Level: "country", Unit: unitOf(q.Types),
		Counts: counts, Ocean: per[0], Total: c.Total(q.Types, from, to), Max: maxN,
		Breaks:         Breaks(vals),
		Coverage:       coverageGaps(sn.coverage, q.Types, from, to),
		DatasetVersion: c.Version,
	}, nil
}

// Timeseries builds E5 for the world (ISO3 "", DISTINCT totals) or one country.
func (s *StatsService) Timeseries(ctx context.Context, q domain.TimeseriesQuery) (domain.Timeseries, error) {
	sn, err := s.ready()
	if err != nil {
		return domain.Timeseries{}, err
	}
	if err := validateTypes(q.Types); err != nil {
		return domain.Timeseries{}, err
	}
	iso := strings.ToUpper(strings.TrimSpace(q.ISO3))
	if iso != "" && normISO(iso) == "" {
		return domain.Timeseries{}, fmt.Errorf("iso3 must be a 3-letter ADM0_A3 code: %w", domain.ErrInvalid)
	}
	return domain.Timeseries{
		Types: q.Types, ISO3: iso, Unit: unitOf(q.Types),
		Points:         sn.cube.Series(q.Types, iso, q.Years.From, q.Years.To),
		DatasetVersion: sn.cube.Version,
	}, nil
}

// Grid is task A8.
func (s *StatsService) Grid(ctx context.Context, q domain.GridQuery) ([]domain.GridCell, error) {
	return nil, fmt.Errorf("stats grid (A8): %w", ErrNotImplemented)
}

func validateTypes(types []domain.IncidentType) error {
	if len(types) == 0 {
		return fmt.Errorf("types is required: %w", domain.ErrInvalid)
	}
	for _, t := range types {
		if !t.Valid() {
			return fmt.Errorf("unknown type %q: %w", t, domain.ErrInvalid)
		}
		if t == domain.FireActivity && len(types) > 1 {
			return fmt.Errorf("fire_activity must be requested alone: %w", domain.ErrInvalid)
		}
	}
	return nil
}

func unitOf(types []domain.IncidentType) string {
	if len(types) == 1 {
		return types[0].Unit()
	}
	return "incidents"
}

// coverageGaps returns, for each requested type whose coverage does not span [from, to], a copy of
// its coverage with a note naming the uncovered years (e.g. "1950–1984 not covered").
func coverageGaps(cov map[domain.IncidentType]domain.Coverage, types []domain.IncidentType, from, to int) []domain.Coverage {
	out := []domain.Coverage{}
	for _, t := range types {
		c, ok := cov[t]
		if !ok {
			continue
		}
		var gaps []string
		if c.FromYear > from {
			gaps = append(gaps, yearSpan(from, min(c.FromYear-1, to)))
		}
		if c.ToYear > 0 && c.ToYear < to {
			gaps = append(gaps, yearSpan(max(c.ToYear+1, from), to))
		}
		if len(gaps) == 0 {
			continue
		}
		c.Note = strings.Join(gaps, ", ") + " not covered"
		out = append(out, c)
	}
	return out
}

func yearSpan(a, b int) string {
	if a == b {
		return fmt.Sprint(a)
	}
	return fmt.Sprintf("%d–%d", a, b)
}
