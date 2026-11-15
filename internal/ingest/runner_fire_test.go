package ingest

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/dwts/backend/internal/domain"
)

type fireWriter struct {
	fakeWriter
	cellBatches [][]domain.FireCell
	cellErr     error
}

func (f *fireWriter) UpsertFireCells(_ context.Context, b []domain.FireCell) (int, error) {
	f.cellBatches = append(f.cellBatches, append([]domain.FireCell(nil), b...))
	if f.cellErr != nil {
		return 0, f.cellErr
	}
	return len(b), nil
}

type fakeFire struct {
	cells []domain.FireCell
	err   error
}

func (fakeFire) Name() string { return "firms" }
func (s fakeFire) FetchCells(_ context.Context, _ Window, emit func(domain.FireCell) error) error {
	for _, c := range s.cells {
		if err := emit(c); err != nil {
			return err
		}
	}
	return s.err
}

func TestRunFireBatchesAndRecords(t *testing.T) {
	w := &fireWriter{}
	r := &Runner{W: w, Log: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), BatchSize: 2}
	cells := make([]domain.FireCell, 5)
	for i := range cells {
		cells[i] = domain.FireCell{Year: 2019, Month: 3, LatIdx: int32(i), Detections: 1, Product: "MODIS_SP"}
	}
	n, err := r.RunFire(context.Background(), fakeFire{cells: cells}, Window{})
	if err != nil || n != 5 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if len(w.cellBatches) != 3 || len(w.cellBatches[0]) != 2 || len(w.cellBatches[2]) != 1 {
		t.Errorf("batches = %v", w.cellBatches)
	}
	if len(w.started) != 1 || w.started[0] != "firms" || len(w.finished) != 1 || w.finished[0].rows != 5 || w.finished[0].err != nil {
		t.Errorf("run record: started %v finished %+v", w.started, w.finished)
	}
}

func TestRunFireErrorIsRecorded(t *testing.T) {
	boom := errors.New("boom")
	w := &fireWriter{}
	_, err := (&Runner{W: w}).RunFire(context.Background(), fakeFire{cells: []domain.FireCell{{Year: 2019}}, err: boom}, Window{})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if len(w.finished) != 1 || !errors.Is(w.finished[0].err, boom) {
		t.Errorf("finished = %+v", w.finished)
	}
	w2 := &fireWriter{cellErr: boom}
	_, err = (&Runner{W: w2}).RunFire(context.Background(), fakeFire{cells: []domain.FireCell{{Year: 2019}}}, Window{})
	if !errors.Is(err, boom) || len(w2.finished) != 1 || w2.finished[0].err == nil {
		t.Errorf("upsert failure: err=%v finished=%+v", err, w2.finished)
	}
}
