package ingest

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/dwts/backend/internal/domain"
)

// RunFire is Run for an aggregate-only FireSource: it records an ingest run,
// buffers the emitted cells (countries already assigned by the source) and
// writes them with UpsertFireCells. It returns the rows the writer reported
// inserting or changing.
func (r *Runner) RunFire(ctx context.Context, s FireSource, w Window) (int, error) {
	log := r.Log
	if log == nil {
		log = slog.Default()
	}
	log = log.With("source", s.Name())
	size := r.BatchSize
	if size <= 0 {
		size = DefaultBatchSize
	}
	runID, err := r.W.StartRun(ctx, s.Name())
	if err != nil {
		return 0, fmt.Errorf("ingest %s: start run: %w", s.Name(), err)
	}
	start := time.Now()
	log.Info("ingest start", "run", runID, "from", w.From, "to", w.To, "batch", size)

	var buf []domain.FireCell
	emitted, written := 0, 0
	flush := func() error {
		if len(buf) == 0 {
			return nil
		}
		n, err := r.W.UpsertFireCells(ctx, buf)
		if err != nil {
			return fmt.Errorf("upsert %d fire cells: %w", len(buf), err)
		}
		written += n
		log.Info("batch written", "cells", len(buf), "changed", n)
		buf = nil // the writer may keep a reference
		return nil
	}
	runErr := s.FetchCells(ctx, w, func(c domain.FireCell) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		emitted++
		buf = append(buf, c)
		if len(buf) >= size {
			return flush()
		}
		return nil
	})
	if runErr == nil {
		runErr = flush()
	}
	if runErr != nil {
		runErr = fmt.Errorf("ingest %s: %w", s.Name(), runErr)
	}
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finishTimeout)
	defer cancel()
	if ferr := r.W.FinishRun(fctx, runID, written, runErr); ferr != nil {
		log.Error("ingest finish run failed", "run", runID, "err", ferr)
		if runErr == nil {
			runErr = fmt.Errorf("ingest %s: finish run: %w", s.Name(), ferr)
		}
	}
	log.Info("ingest done", "run", runID, "emitted", emitted, "rows", written,
		"dur", time.Since(start).Round(time.Millisecond), "ok", runErr == nil)
	return written, runErr
}
