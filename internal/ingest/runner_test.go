package ingest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/dwts/backend/internal/domain"
	"github.com/dwts/backend/internal/store"
)

// fakeWriter records every call. A batch is copied so the test sees what the
// writer saw even if the runner reuses its buffer.
type fakeWriter struct {
	store.Writer // the fire/country/postprocess methods are never called

	started  []string
	batches  [][]domain.Incident
	finished []finishCall

	startErr  error
	upsertErr error
	failBatch int // 1-based batch number that fails; 0 = use upsertErr for all when set
	changed   func(batch []domain.Incident) int
}

type finishCall struct {
	runID    int64
	rows     int
	err      error
	ctxAlive bool
}

func (f *fakeWriter) StartRun(_ context.Context, source string) (int64, error) {
	if f.startErr != nil {
		return 0, f.startErr
	}
	f.started = append(f.started, source)
	return 42, nil
}

func (f *fakeWriter) FinishRun(ctx context.Context, id int64, rows int, runErr error) error {
	f.finished = append(f.finished, finishCall{id, rows, runErr, ctx.Err() == nil})
	return nil
}

func (f *fakeWriter) UpsertIncidents(_ context.Context, batch []domain.Incident) (int, error) {
	f.batches = append(f.batches, append([]domain.Incident(nil), batch...))
	if f.upsertErr != nil && (f.failBatch == 0 || f.failBatch == len(f.batches)) {
		return 0, f.upsertErr
	}
	if f.changed != nil {
		return f.changed(batch), nil
	}
	return len(batch), nil
}

// fakeSource emits the given incidents, then returns err.
type fakeSource struct {
	name string
	incs []domain.Incident
	err  error
	// each runs before emit i is delivered (to cancel a context, say)
	each func(i int)
}

func (s fakeSource) Name() string { return s.name }
func (s fakeSource) Fetch(ctx context.Context, w Window, emit func(domain.Incident) error) error {
	for i, inc := range s.incs {
		if s.each != nil {
			s.each(i)
		}
		if err := emit(inc); err != nil {
			return err
		}
	}
	return s.err
}

func inc(id string, year int) domain.Incident {
	return domain.Incident{
		Source: "fake", SourceID: id, Type: domain.Flood, Title: "Flood " + id,
		StartTime: time.Date(year, 6, 1, 12, 0, 0, 0, time.UTC), TimePrecision: "day", Lon: 10, Lat: 20,
	}
}

func newRunner(w store.Writer, batch int) (*Runner, *bytes.Buffer) {
	logs := &bytes.Buffer{}
	return &Runner{W: w, Log: slog.New(slog.NewTextHandler(logs, nil)), BatchSize: batch, MinYear: 1900}, logs
}

func TestRunBatchesAndCountsRows(t *testing.T) {
	w := &fakeWriter{}
	r, logs := newRunner(w, 2)
	src := fakeSource{name: "fake", incs: []domain.Incident{inc("a", 2001), inc("b", 2002), inc("c", 2003), inc("d", 2004), inc("e", 2005)}}

	n, err := r.Run(context.Background(), src, Window{})
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Errorf("rows = %d, want 5", n)
	}
	var sizes []int
	for _, b := range w.batches {
		sizes = append(sizes, len(b))
	}
	if fmt.Sprint(sizes) != "[2 2 1]" {
		t.Errorf("batch sizes = %v, want [2 2 1]", sizes)
	}
	if len(w.started) != 1 || w.started[0] != "fake" {
		t.Errorf("StartRun calls = %v", w.started)
	}
	if len(w.finished) != 1 || w.finished[0].rows != 5 || w.finished[0].err != nil || w.finished[0].runID != 42 {
		t.Errorf("FinishRun calls = %+v", w.finished)
	}
	// Every incident was finalized before it reached the writer.
	for _, b := range w.batches {
		for _, i := range b {
			if i.Year == 0 || i.SearchText == "" || len(i.RawHash) != 32 {
				t.Errorf("incident %s reached the writer unfinalized: %+v", i.SourceID, i)
			}
		}
	}
	if !strings.Contains(logs.String(), "ingest done") {
		t.Errorf("missing summary log:\n%s", logs)
	}
}

func TestRunReportsAffectedRowsNotEmitted(t *testing.T) {
	w := &fakeWriter{changed: func([]domain.Incident) int { return 0 }} // second run: everything unchanged
	r, _ := newRunner(w, 10)
	n, err := r.Run(context.Background(), fakeSource{name: "fake", incs: []domain.Incident{inc("a", 2001), inc("b", 2002)}}, Window{})
	if err != nil || n != 0 {
		t.Errorf("Run = %d, %v; want 0 rows for a no-op re-run", n, err)
	}
	if w.finished[0].rows != 0 {
		t.Errorf("FinishRun rows = %d, want 0", w.finished[0].rows)
	}
}

func TestRunDefaultBatchSize(t *testing.T) {
	w := &fakeWriter{}
	r, _ := newRunner(w, 0)
	var incs []domain.Incident
	for i := range DefaultBatchSize + 1 {
		incs = append(incs, inc(fmt.Sprint(i), 2001))
	}
	if _, err := r.Run(context.Background(), fakeSource{name: "fake", incs: incs}, Window{}); err != nil {
		t.Fatal(err)
	}
	if len(w.batches) != 2 || len(w.batches[0]) != DefaultBatchSize || len(w.batches[1]) != 1 {
		t.Errorf("batches = %d, want %d then 1", len(w.batches), DefaultBatchSize)
	}
}

func TestRunSkipsInvalidAndBelowMinYear(t *testing.T) {
	bad := inc("badlat", 2001)
	bad.Lat = 123
	notitle := inc("notitle", 2001)
	notitle.Title = ""
	w := &fakeWriter{}
	r, logs := newRunner(w, 10)
	r.MinYear = 1950
	src := fakeSource{name: "fake", incs: []domain.Incident{inc("ok", 2001), inc("old", 1949), bad, notitle, inc("old2", 1900)}}

	n, err := r.Run(context.Background(), src, Window{})
	if err != nil || n != 1 {
		t.Fatalf("Run = %d, %v; want 1 row", n, err)
	}
	if len(w.batches) != 1 || len(w.batches[0]) != 1 || w.batches[0][0].SourceID != "ok" {
		t.Errorf("only the valid incident may be written: %+v", w.batches)
	}
	for _, want := range []string{"below_min_year=2", "bad_coords=1", "missing_title=1"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("summary should contain %q:\n%s", want, logs)
		}
	}
}

func TestRunDedupesWithinABatchKeepingLast(t *testing.T) {
	first, last := inc("dup", 2001), inc("dup", 2001)
	first.Title, last.Title = "first version", "last version"
	w := &fakeWriter{}
	r, _ := newRunner(w, 10)
	src := fakeSource{name: "fake", incs: []domain.Incident{first, inc("other", 2002), last}}
	if _, err := r.Run(context.Background(), src, Window{}); err != nil {
		t.Fatal(err)
	}
	if len(w.batches) != 1 || len(w.batches[0]) != 2 {
		t.Fatalf("batches = %+v, want one batch of 2", w.batches)
	}
	if got := w.batches[0][0]; got.SourceID != "dup" || got.Title != "last version" {
		t.Errorf("dup row = %s %q, want the last version in place", got.SourceID, got.Title)
	}
}

func TestRunFetchErrorIsRecordedAndNotFlushed(t *testing.T) {
	boom := errors.New("upstream melted")
	w := &fakeWriter{}
	r, _ := newRunner(w, 2)
	src := fakeSource{name: "fake", incs: []domain.Incident{inc("a", 2001), inc("b", 2002), inc("c", 2003)}, err: boom}

	n, err := r.Run(context.Background(), src, Window{})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap the fetch error", err)
	}
	if n != 2 {
		t.Errorf("rows = %d, want 2 (the full batch written before the failure)", n)
	}
	if len(w.finished) != 1 || !errors.Is(w.finished[0].err, boom) || w.finished[0].rows != 2 {
		t.Errorf("FinishRun must record the failure: %+v", w.finished)
	}
}

func TestRunUpsertErrorStopsTheRun(t *testing.T) {
	boom := errors.New("db down")
	w := &fakeWriter{upsertErr: boom, failBatch: 2}
	r, _ := newRunner(w, 1)
	src := fakeSource{name: "fake", incs: []domain.Incident{inc("a", 2001), inc("b", 2002), inc("c", 2003)}}

	n, err := r.Run(context.Background(), src, Window{})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if n != 1 || len(w.batches) != 2 {
		t.Errorf("rows=%d batches=%d, want 1 row and a stop at batch 2", n, len(w.batches))
	}
	if len(w.finished) != 1 || w.finished[0].err == nil || w.finished[0].rows != 1 {
		t.Errorf("FinishRun = %+v", w.finished)
	}
}

func TestRunStartRunErrorMeansNothingRuns(t *testing.T) {
	w := &fakeWriter{startErr: errors.New("no db")}
	r, _ := newRunner(w, 5)
	called := false
	src := fakeSource{name: "fake", each: func(int) { called = true }, incs: []domain.Incident{inc("a", 2001)}}
	if _, err := r.Run(context.Background(), src, Window{}); err == nil {
		t.Fatal("want an error")
	}
	if called || len(w.finished) != 0 {
		t.Error("a failed StartRun must not fetch or finish")
	}
}

// After a cancel the run must still be finished (on a live context) as failed.
func TestRunCancelStillFinishesTheRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	w := &fakeWriter{}
	r, _ := newRunner(w, 10)
	src := fakeSource{name: "fake", incs: []domain.Incident{inc("a", 2001), inc("b", 2002), inc("c", 2003)},
		each: func(i int) {
			if i == 1 {
				cancel()
			}
		}}
	_, err := r.Run(ctx, src, Window{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(w.finished) != 1 || !w.finished[0].ctxAlive || !errors.Is(w.finished[0].err, context.Canceled) {
		t.Errorf("FinishRun after cancel = %+v, want a live ctx and the cancel error", w.finished)
	}
}

func TestRunRejectsForeignSource(t *testing.T) {
	foreign := inc("a", 2001)
	foreign.Source = "usgs"
	w := &fakeWriter{}
	r, _ := newRunner(w, 5)
	_, err := r.Run(context.Background(), fakeSource{name: "fake", incs: []domain.Incident{foreign}}, Window{})
	if err == nil || !strings.Contains(err.Error(), "usgs") {
		t.Errorf("err = %v, want a source mismatch", err)
	}
}

func TestRunFillsEmptySource(t *testing.T) {
	i := inc("a", 2001)
	i.Source = ""
	w := &fakeWriter{}
	r, _ := newRunner(w, 5)
	if _, err := r.Run(context.Background(), fakeSource{name: "fake", incs: []domain.Incident{i}}, Window{}); err != nil {
		t.Fatal(err)
	}
	if w.batches[0][0].Source != "fake" {
		t.Errorf("Source = %q, want the adapter name", w.batches[0][0].Source)
	}
}

func TestRunNilLoggerIsFine(t *testing.T) {
	old := slog.Default()
	t.Cleanup(func() { slog.SetDefault(old) })
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := &Runner{W: &fakeWriter{}, MinYear: 1900}
	if _, err := r.Run(context.Background(), fakeSource{name: "fake", incs: []domain.Incident{inc("a", 2001)}}, Window{}); err != nil {
		t.Fatal(err)
	}
}
