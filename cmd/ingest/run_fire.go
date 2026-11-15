package main

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/dwts/backend/internal/domain"
	"github.com/dwts/backend/internal/ingest"
	"github.com/dwts/backend/internal/ingest/sources"
)

// fireRegistry holds the aggregate-only sources (fire_cells). They are valid
// --source names but, unlike registry, never part of --all: FIRMS needs an
// archive in RAW_DIR/firms or a FIRMS_MAP_KEY, and fails clearly without.
var fireRegistry = map[string]func(sourceDeps) *sources.FIRMS{
	"firms": func(d sourceDeps) *sources.FIRMS {
		return sources.NewFIRMS(d.F, filepath.Join(d.Cfg.RawDir, "firms"), d.Cfg.FIRMSMapKey, d.Cfg.FIRMSProduct, d.Log)
	},
}

// isSource reports whether name is a registered incident or fire source.
func isSource(name string) bool {
	_, ok := registry[name]
	_, fire := fireRegistry[name]
	return ok || fire
}

// fireSource lets a FireSource travel through runSources, which deals in
// ingest.Source; dispatchRunner recognises it.
type fireSource struct{ *sources.FIRMS }

func (fireSource) Fetch(context.Context, ingest.Window, func(domain.Incident) error) error {
	return errors.New("firms is a fire-cell source: it has no incidents")
}

// buildSource builds the adapter for a registered name.
func buildSource(name string, d sourceDeps) ingest.Source {
	if b, ok := fireRegistry[name]; ok {
		return fireSource{b(d)}
	}
	return registry[name](d)
}

// dispatchRunner runs incident sources with Runner.Run and fire sources with
// Runner.RunFire, giving them the database's country assignment.
type dispatchRunner struct{ *ingest.Runner }

func (r dispatchRunner) Run(ctx context.Context, s ingest.Source, w ingest.Window) (int, error) {
	if fs, ok := s.(fireSource); ok {
		fs.Assign = r.W.AssignCellCountries
		return r.RunFire(ctx, fs.FIRMS, w)
	}
	return r.Runner.Run(ctx, s, w)
}
