// Package store declares the persistence interfaces (CONTRACT, Plan_Detailed §4).
// Rule R2: interfaces + sentinel errors only. The pgx implementation lives in
// internal/store/postgres and is imported only by cmd/* (rule R3).
package store

import (
	"context"
	"time"

	"github.com/dwts/backend/internal/domain"
)

// Sentinel errors re-exported for convenience; they are the same values as in domain.
var (
	ErrNotFound = domain.ErrNotFound
	ErrInvalid  = domain.ErrInvalid
)

// IncidentReader is the read side for incidents (implemented by Ahmed, A2).
type IncidentReader interface {
	GetIncident(ctx context.Context, id int64) (domain.Incident, error) // ErrNotFound
	ListIncidents(ctx context.Context, f domain.IncidentFilter) (items []domain.IncidentSummary, nextCursor string, err error)
	Search(ctx context.Context, q domain.SearchQuery) ([]domain.IncidentSummary, error)
	Grid(ctx context.Context, q domain.GridQuery) ([]domain.GridCell, error)
	FindBySource(ctx context.Context, source, sourceID string) (int64, error) // ErrNotFound; used to link stories
}

// StatsReader feeds the in-memory cube (implemented by Ahmed, A2).
type StatsReader interface {
	DatasetVersion(ctx context.Context) (version int64, updatedAt time.Time, err error)
	LoadCounts(ctx context.Context) ([]domain.CountRow, error) // full scan of mv_counts
	LoadTotals(ctx context.Context) ([]domain.TotalRow, error) // full scan of mv_totals
	Coverage(ctx context.Context) ([]domain.Coverage, error)
	Countries(ctx context.Context) ([]domain.Country, error)
}

// SARCache persists ASF results per incident (implemented by Ahmed, A5).
type SARCache interface {
	GetSAR(ctx context.Context, incidentID int64) (domain.SARResult, error) // ErrNotFound on miss
	PutSAR(ctx context.Context, r domain.SARResult) error
}

// Writer is the ingest/write side (implemented by Mohammed, M1–M5, M15).
type Writer interface {
	StartRun(ctx context.Context, source string) (runID int64, err error)
	FinishRun(ctx context.Context, runID int64, rows int, runErr error) error
	UpsertIncidents(ctx context.Context, batch []domain.Incident) (int, error)
	UpsertFireCells(ctx context.Context, batch []domain.FireCell) (int, error)
	LoadCountries(ctx context.Context, geojsonPath string) (int, error)
	AssignCellCountries(ctx context.Context, cells []domain.FireCell) error // fills ISO3 by cell center
	Postprocess(ctx context.Context) error
}
