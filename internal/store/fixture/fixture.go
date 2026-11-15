// Package fixture is a DEV-ONLY store.StatsReader backed by a JSON file (env STATS_FIXTURE).
// It lets the API serve E2–E5 before the Postgres read side (A2) exists, so the frontend can
// colour the map today. It is a sibling of store/postgres: it imports only domain + store, and
// only cmd/api imports it (same rule R3 as store/postgres). Delete or keep for demos after A2.
package fixture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/dwts/backend/internal/domain"
	"github.com/dwts/backend/internal/store"
)

// Series is a compact run of yearly values starting at FromYear.
type Series struct {
	Type     domain.IncidentType `json:"type"`
	ISO3     string              `json:"iso3,omitempty"` // "" = ocean (counts) / ignored (totals)
	FromYear int                 `json:"fromYear"`
	N        []int32             `json:"n"`
}

// File is the JSON layout of testdata/stats_fixture.json.
type File struct {
	DatasetVersion int64             `json:"datasetVersion"`
	UpdatedAt      time.Time         `json:"updatedAt"`
	Counts         []Series          `json:"counts"` // per type × country (mv_counts)
	Totals         []Series          `json:"totals"` // per type, DISTINCT (mv_totals)
	Coverage       []domain.Coverage `json:"coverage"`
	Countries      []domain.Country  `json:"countries,omitempty"`
}

// Reader implements store.StatsReader from a fixture File.
type Reader struct {
	f      File
	counts []domain.CountRow
	totals []domain.TotalRow
}

var _ store.StatsReader = (*Reader)(nil)

// Load reads and validates a fixture file.
func Load(path string) (*Reader, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("stats fixture: %w", err)
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("stats fixture %s: %w", path, err)
	}
	return New(f)
}

// New builds a Reader from an in-memory File (used by tests).
func New(f File) (*Reader, error) {
	if f.DatasetVersion <= 0 {
		return nil, errors.New("stats fixture: datasetVersion must be > 0")
	}
	r := &Reader{f: f}
	for _, s := range f.Counts {
		if !s.Type.Valid() {
			return nil, fmt.Errorf("stats fixture: unknown type %q", s.Type)
		}
		for i, n := range s.N {
			r.counts = append(r.counts, domain.CountRow{Type: s.Type, ISO3: s.ISO3, Year: int16(s.FromYear + i), N: n})
		}
	}
	for _, s := range f.Totals {
		if !s.Type.Valid() {
			return nil, fmt.Errorf("stats fixture: unknown type %q", s.Type)
		}
		for i, n := range s.N {
			r.totals = append(r.totals, domain.TotalRow{Type: s.Type, Year: int16(s.FromYear + i), N: n})
		}
	}
	return r, nil
}

// DatasetVersion returns the fixture's fixed version.
func (r *Reader) DatasetVersion(context.Context) (int64, time.Time, error) {
	return r.f.DatasetVersion, r.f.UpdatedAt, nil
}

// LoadCounts returns the expanded per-country rows.
func (r *Reader) LoadCounts(context.Context) ([]domain.CountRow, error) { return r.counts, nil }

// LoadTotals returns the expanded DISTINCT totals.
func (r *Reader) LoadTotals(context.Context) ([]domain.TotalRow, error) { return r.totals, nil }

// Coverage returns the fixture coverage.
func (r *Reader) Coverage(context.Context) ([]domain.Coverage, error) { return r.f.Coverage, nil }

// Countries returns the fixture countries (may be empty).
func (r *Reader) Countries(context.Context) ([]domain.Country, error) { return r.f.Countries, nil }
