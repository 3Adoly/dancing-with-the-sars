package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/dwts/backend/internal/config"
	"github.com/dwts/backend/internal/domain"
	"github.com/dwts/backend/internal/ingest"
	"github.com/dwts/backend/internal/ingest/fetch"
	"github.com/dwts/backend/internal/ingest/sources"
	"github.com/dwts/backend/internal/store"
)

var (
	testFrom = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	testTo   = time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
)

func TestFireRegistry(t *testing.T) {
	deps := sourceDeps{F: fetch.New(nil, t.TempDir(), nil), Cfg: config.Config{RawDir: "/raw", FIRMSMapKey: "k", FIRMSProduct: "MODIS_SP"}, Log: slog.Default()}
	for name, build := range fireRegistry {
		if got := build(deps).Name(); got != name {
			t.Errorf("fireRegistry[%q] builds %q", name, got)
		}
	}
	fs, ok := buildSource("firms", deps).(fireSource)
	if !ok {
		t.Fatalf("buildSource(firms) = %T", buildSource("firms", deps))
	}
	if fs.Dir != "/raw/firms" || fs.MapKey != "k" || fs.Product != "MODIS_SP" {
		t.Errorf("firms deps: %+v", fs.FIRMS)
	}
	if _, ok := buildSource("usgs", deps).(*sources.USGS); !ok {
		t.Errorf("usgs still builds from registry")
	}
	if slices.Contains(registryNames(), "firms") {
		t.Errorf("--all must not include firms (it needs an archive or a key)")
	}
	opts, err := parseRunFlags([]string{"--source", "firms"}, io.Discard)
	if err != nil || len(opts.sources) != 1 || opts.sources[0] != "firms" {
		t.Errorf("--source firms: %+v %v", opts, err)
	}
}

type fireRunWriter struct {
	store.Writer
	assigned bool
}

func (w *fireRunWriter) StartRun(context.Context, string) (int64, error)    { return 1, nil }
func (w *fireRunWriter) FinishRun(context.Context, int64, int, error) error { return nil }
func (w *fireRunWriter) AssignCellCountries(_ context.Context, c []domain.FireCell) error {
	w.assigned = true
	return nil
}
func (w *fireRunWriter) UpsertFireCells(_ context.Context, b []domain.FireCell) (int, error) {
	return len(b), nil
}

// Neither archive CSVs nor a MAP_KEY: a clear error, and the run is failed.
func TestFireRunnerNoInput(t *testing.T) {
	deps := sourceDeps{F: fetch.New(nil, t.TempDir(), nil), Cfg: config.Config{RawDir: t.TempDir(), FIRMSProduct: "MODIS_SP"}, Log: slog.Default()}
	w := &fireRunWriter{}
	r := dispatchRunner{&ingest.Runner{W: w, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	_, err := r.Run(context.Background(), buildSource("firms", deps), ingest.Window{From: testFrom, To: testTo})
	if !errors.Is(err, sources.ErrFIRMSNoInput) {
		t.Fatalf("err = %v", err)
	}
}

// With an archive the runner wires AssignCellCountries into the adapter.
func TestFireRunnerAssignsCountries(t *testing.T) {
	raw := t.TempDir()
	if err := os.MkdirAll(raw+"/firms", 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("../../testdata/fixtures/firms_sample.csv")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(raw+"/firms/a.csv", b, 0o644); err != nil {
		t.Fatal(err)
	}
	deps := sourceDeps{F: fetch.New(nil, t.TempDir(), nil), Cfg: config.Config{RawDir: raw, FIRMSProduct: "MODIS_SP"}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	w := &fireRunWriter{}
	r := dispatchRunner{&ingest.Runner{W: w, Log: deps.Log}}
	n, err := r.Run(context.Background(), buildSource("firms", deps), ingest.Window{From: testFrom, To: testTo})
	if err != nil || n != 5 || !w.assigned {
		t.Errorf("n=%d err=%v assigned=%v", n, err, w.assigned)
	}
}
