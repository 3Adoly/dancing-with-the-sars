// Package ingest holds the ingestion contract (Detailed §4). It never imports
// service, httpapi or chat.
package ingest

import (
	"context"
	"time"

	"github.com/dwts/backend/internal/domain"
)

// Window is the time range an adapter is asked to fetch.
type Window struct{ From, To time.Time }

// Source is an incident adapter. Fetch streams normalized incidents to emit.
type Source interface {
	Name() string // = incidents.source value
	Fetch(ctx context.Context, w Window, emit func(domain.Incident) error) error
}

// FireSource is an aggregate-only adapter (FIRMS) emitting fire_cells rows.
type FireSource interface {
	Name() string
	FetchCells(ctx context.Context, w Window, emit func(domain.FireCell) error) error
}
