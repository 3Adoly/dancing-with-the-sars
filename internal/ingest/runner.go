package ingest

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/dwts/backend/internal/domain"
	"github.com/dwts/backend/internal/store"
)

// DefaultBatchSize is the number of incidents buffered per UpsertIncidents call.
const DefaultBatchSize = 5000

// finishTimeout bounds the final FinishRun call, which runs on a context
// detached from the caller's so a cancelled run is still recorded as failed.
const finishTimeout = 10 * time.Second

// Runner drives one Source through the shared pipeline:
// StartRun, Fetch, Finalize, batch, UpsertIncidents, FinishRun.
type Runner struct {
	W         store.Writer
	Log       *slog.Logger
	BatchSize int // incidents per UpsertIncidents call; <= 0 means DefaultBatchSize
	MinYear   int // incidents that start before this UTC year are skipped
}

// Run fetches s over w and writes the incidents. It returns the number of rows
// the writer reported inserting or changing (re-running an unchanged window
// returns 0) and records it in ingest_runs. A failed run stays recorded as
// failed with the error; rows written before the failure are kept, and a
// re-run picks up cheaply because the upsert is idempotent.
func (r *Runner) Run(ctx context.Context, s Source, w Window) (int, error) {
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

	b := batcher{r: r, log: log, ctx: ctx, size: size, name: s.Name(), skipped: map[string]int{}}
	runErr := s.Fetch(ctx, w, b.emit)
	if runErr == nil {
		runErr = b.flush()
	}
	if runErr != nil {
		runErr = fmt.Errorf("ingest %s: %w", s.Name(), runErr)
	}

	// Detached from ctx: after a cancel the run must still be marked failed.
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finishTimeout)
	defer cancel()
	if ferr := r.W.FinishRun(fctx, runID, b.written, runErr); ferr != nil {
		log.Error("ingest finish run failed", "run", runID, "err", ferr)
		if runErr == nil {
			runErr = fmt.Errorf("ingest %s: finish run: %w", s.Name(), ferr)
		}
	}
	log.Info("ingest done", "run", runID, "emitted", b.emitted, "skipped", skippedSummary(b.skipped),
		"rows", b.written, "dur", time.Since(start).Round(time.Millisecond), "ok", runErr == nil)
	return b.written, runErr
}

// batcher is the emit callback's state: it finalizes each incident, buffers
// up to size of them (last one wins per source+id) and flushes full batches.
type batcher struct {
	r       *Runner
	log     *slog.Logger
	ctx     context.Context
	name    string
	size    int
	buf     []domain.Incident
	index   map[string]int // source\x00id -> position in buf
	emitted int
	written int
	skipped map[string]int
}

func (b *batcher) emit(inc domain.Incident) error {
	if err := b.ctx.Err(); err != nil {
		return err
	}
	b.emitted++
	if inc.Source == "" {
		inc.Source = b.name
	} else if inc.Source != b.name {
		return fmt.Errorf("source %q emitted an incident with source %q", b.name, inc.Source)
	}
	if reason := finalize(&inc, b.r.MinYear); reason != "" {
		b.skipped[reason]++
		b.log.Debug("incident skipped", "reason", reason, "source_id", inc.SourceID)
		return nil
	}
	if b.index == nil {
		b.index = make(map[string]int, b.size)
	}
	key := inc.SourceID // every incident in a run has the same source
	if i, dup := b.index[key]; dup {
		b.buf[i] = inc
		return nil
	}
	b.index[key] = len(b.buf)
	b.buf = append(b.buf, inc)
	if len(b.buf) >= b.size {
		return b.flush()
	}
	return nil
}

func (b *batcher) flush() error {
	if len(b.buf) == 0 {
		return nil
	}
	start := time.Now()
	n, err := b.r.W.UpsertIncidents(b.ctx, b.buf)
	if err != nil {
		return fmt.Errorf("upsert %d incidents: %w", len(b.buf), err)
	}
	b.written += n
	b.log.Info("batch written", "incidents", len(b.buf), "changed", n, "dur", time.Since(start).Round(time.Millisecond))
	b.buf = nil // a fresh slice per batch: the writer may keep a reference
	clear(b.index)
	return nil
}

// skippedSummary renders the per-reason skip counts as "reason=n ..." in a
// stable order, or "0" when nothing was skipped.
func skippedSummary(m map[string]int) string {
	if len(m) == 0 {
		return "0"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s=%d", k, m[k])
	}
	return strings.Join(parts, " ")
}
