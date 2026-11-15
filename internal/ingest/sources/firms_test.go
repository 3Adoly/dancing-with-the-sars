package sources

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dwts/backend/internal/domain"
	"github.com/dwts/backend/internal/ingest"
	"github.com/dwts/backend/internal/ingest/fetch"
)

func firmsFixtureDir(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../../testdata/fixtures/firms_sample.csv")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "MODIS_2019.csv"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func collectCells(t *testing.T, s *FIRMS, w ingest.Window) ([]domain.FireCell, error) {
	t.Helper()
	var out []domain.FireCell
	err := s.FetchCells(context.Background(), w, func(c domain.FireCell) error { out = append(out, c); return nil })
	return out, err
}

var firmsAllTime = ingest.Window{From: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)}

func TestFIRMSCSVCells(t *testing.T) {
	var logs bytes.Buffer
	s := &FIRMS{Dir: firmsFixtureDir(t), Product: "MODIS_SP", MapKey: "SECRETKEY123", Log: slog.New(slog.NewTextHandler(&logs, nil))}
	var assigned int
	s.Assign = func(_ context.Context, cells []domain.FireCell) error {
		for i := range cells {
			assigned++
			cells[i].ISO3 = "XXX"
		}
		return nil
	}
	cells, err := collectCells(t, s, firmsAllTime)
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.FireCell{
		{Year: 2019, Month: 3, LatIdx: -1, LonIdx: -1, Detections: 1, FRPSum: 2, Product: "MODIS_SP", ISO3: "XXX"},
		{Year: 2019, Month: 3, LatIdx: 0, LonIdx: 40, Detections: 2, FRPSum: 15.5, Product: "MODIS_SP", ISO3: "XXX"},
		{Year: 2019, Month: 4, LatIdx: 0, LonIdx: 40, Detections: 1, FRPSum: 1, Product: "MODIS_SP", ISO3: "XXX"},
		{Year: 2020, Month: 1, LatIdx: 0, LonIdx: 40, Detections: 1, FRPSum: 0, Product: "MODIS_SP", ISO3: "XXX"},
		{Year: 2020, Month: 1, LatIdx: 180, LonIdx: 8, Detections: 1, FRPSum: 20, Product: "MODIS_SP", ISO3: "XXX"},
	}
	if len(cells) != len(want) {
		t.Fatalf("got %d cells %+v, want %d", len(cells), cells, len(want))
	}
	for i := range want {
		if cells[i] != want[i] {
			t.Errorf("cell %d = %+v, want %+v", i, cells[i], want[i])
		}
	}
	if assigned != 5 {
		t.Errorf("Assign saw %d cells, want 5", assigned)
	}
	if strings.Contains(logs.String(), "SECRETKEY123") {
		t.Errorf("log leaks the MAP_KEY: %s", logs.String())
	}
	for _, f := range []string{"heap_mb", "cells=5", "detections=6", "below_confidence=1", "unusable=1"} {
		if !strings.Contains(logs.String(), f) {
			t.Errorf("log lacks %q: %s", f, logs.String())
		}
	}
}

func TestFIRMSCSVWindow(t *testing.T) {
	s := &FIRMS{Dir: firmsFixtureDir(t), Product: "MODIS_SP"}
	cells, err := collectCells(t, s, ingest.Window{
		From: time.Date(2019, 3, 15, 0, 0, 0, 0, time.UTC), To: time.Date(2019, 4, 2, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	// 03-15 (conf 30) and 04-01 only.
	if len(cells) != 2 || cells[0].Month != 3 || cells[0].Detections != 1 || cells[1].Month != 4 {
		t.Errorf("cells = %+v", cells)
	}
}

func TestFIRMSNoInput(t *testing.T) {
	s := &FIRMS{Dir: t.TempDir(), Product: "MODIS_SP"}
	_, err := collectCells(t, s, firmsAllTime)
	if !errors.Is(err, ErrFIRMSNoInput) {
		t.Fatalf("err = %v, want ErrFIRMSNoInput", err)
	}
	if s2 := (&FIRMS{Dir: filepath.Join(t.TempDir(), "missing"), Product: "MODIS_SP"}); true {
		if _, err := collectCells(t, s2, firmsAllTime); !errors.Is(err, ErrFIRMSNoInput) {
			t.Errorf("missing dir: err = %v", err)
		}
	}
}

func TestFIRMSBadHeader(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "x.csv"), []byte("a,b\n1,2\n"), 0o644)
	_, err := collectCells(t, &FIRMS{Dir: dir, Product: "MODIS_SP"}, firmsAllTime)
	if err == nil || !strings.Contains(err.Error(), "latitude") {
		t.Errorf("err = %v", err)
	}
}

func TestFIRMSAPI(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	srv := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		b, _ := os.ReadFile("../../../testdata/fixtures/firms_sample.csv")
		// Only the first call returns detections; later ones are header-only.
		if !strings.HasSuffix(r.URL.Path, "/2019-03-01") {
			b = []byte(strings.SplitN(string(b), "\n", 2)[0] + "\n")
		}
		io.Copy(w, bytes.NewReader(b))
	})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	f := fetch.New(ts.Client(), t.TempDir(), log)
	s := NewFIRMS(f, t.TempDir(), "KEY42", "MODIS_SP", log)
	s.BaseURL = ts.URL
	s.Now = func() time.Time { return time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC) }
	if s.Name() != "firms" {
		t.Errorf("Name = %q", s.Name())
	}
	if f.Limiter != nil || s.F == f || s.F.Limiter == nil {
		t.Errorf("constructor must pace a copy of the Fetcher")
	}
	// Do not wait 700 ms between the calls in the test.
	s.F.Limiter.SetLimit(1000)
	w := ingest.Window{From: time.Date(2019, 3, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2019, 3, 12, 0, 0, 0, 0, time.UTC)}
	cells, err := collectCells(t, s, w)
	if err != nil {
		t.Fatal(err)
	}
	wantPaths := []string{}
	for _, d := range []string{"2019-03-01", "2019-03-06", "2019-03-11"} {
		wantPaths = append(wantPaths, "/api/area/csv/KEY42/MODIS_SP/world/5/"+d)
	}
	if fmt.Sprint(paths) != fmt.Sprint(wantPaths) {
		t.Errorf("paths = %v, want %v", paths, wantPaths)
	}
	// Window ends 03-12: the fixture's 2019-03 rows up to 03-11 count (4 rows, 3 kept; 03-15/03-16 outside).
	if len(cells) != 2 || cells[0].Detections != 1 || cells[1].Detections != 1 {
		t.Errorf("cells = %+v", cells)
	}
	// Cached once: a second run makes no request (all calls end before now-8 days).
	paths = nil
	if _, err := collectCells(t, s, w); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 0 {
		t.Errorf("second run requested %v", paths)
	}
	if strings.Contains(logs.String(), "KEY42") {
		t.Errorf("log leaks the MAP_KEY: %s", logs.String())
	}
}
