package sources

import (
	"bufio"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/dwts/backend/internal/domain"
	"github.com/dwts/backend/internal/ingest"
	"github.com/dwts/backend/internal/ingest/fetch"
)

const (
	// FIRMSBaseURL is the FIRMS Area API root.
	FIRMSBaseURL = "https://firms.modaps.eosdis.nasa.gov"
	// firmsCellDeg is the aggregation cell size in degrees.
	firmsCellDeg = 0.25
	// firmsMinConfidence: MODIS detections below it are dropped (Guide M15).
	firmsMinConfidence = 30
	// firmsDays is the most days one Area API call may ask for.
	firmsDays = 5
	// firmsAssignBatch is the number of cells per AssignCellCountries call.
	firmsAssignBatch = 10000
	// firmsRefreshDays: an API call reaching this close to now is fetched
	// again (FIRMS still adds detections to recent days); older calls are
	// cached once.
	firmsRefreshDays = 3
)

// firmsColumns are the CSV header names the adapter needs, in the order of
// the index array readCSV builds.
var firmsColumns = []string{"latitude", "longitude", "acq_date", "confidence", "frp"}

// ErrFIRMSNoInput means neither archive CSVs nor a MAP_KEY are available.
var ErrFIRMSNoInput = errors.New("firms: no archive CSVs found and FIRMS_MAP_KEY is not set (request MODIS archive CSVs at https://firms.modaps.eosdis.nasa.gov/download/ into the firms directory, or set FIRMS_MAP_KEY for the Area API fallback)")

// FIRMS aggregates NASA FIRMS MODIS fire detections into 0.25 degree x month
// cells (Guide M15). Raw detections are never stored. The primary input is
// the archive CSVs in Dir (*.csv, one year each, whole world or per country);
// when Dir has none the Area API is used (needs MapKey).
type FIRMS struct {
	F       *fetch.Fetcher
	Dir     string // archive CSV directory
	MapKey  string // secret: never logged
	Product string // MODIS_SP; one product only, never mixed with VIIRS
	Log     *slog.Logger
	// Assign fills ISO3 on cells (the DB's AssignCellCountries). It is called
	// with at most firmsAssignBatch cells at a time. nil leaves ISO3 empty.
	Assign  func(ctx context.Context, cells []domain.FireCell) error
	BaseURL string           // "" = FIRMSBaseURL; tests point it at an httptest server
	Now     func() time.Time // nil = time.Now
}

// NewFIRMS returns the adapter with its own copy of f, whose limiter paces
// network calls to one per 700 ms (about 857 calls per 10 minutes, 5
// transactions each, under the 5,000 per 10 minutes allowance). The MAP_KEY
// is added to the copy's Secrets so the fetcher redacts it.
func NewFIRMS(f *fetch.Fetcher, dir, mapKey, product string, log *slog.Logger) *FIRMS {
	fc := *f
	fc.Limiter = rate.NewLimiter(rate.Every(700*time.Millisecond), 1)
	if mapKey != "" {
		fc.Secrets = append(slices.Clone(fc.Secrets), mapKey)
	}
	if log == nil {
		log = slog.Default()
	}
	return &FIRMS{F: &fc, Dir: dir, MapKey: mapKey, Product: product, Log: log}
}

// Name is the ingest_runs.source value.
func (s *FIRMS) Name() string { return "firms" }

func (s *FIRMS) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *FIRMS) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

type firmsKey struct {
	year, month    int16
	latIdx, lonIdx int32
}

type firmsAcc struct {
	n   int32
	frp float64
}

// firmsAgg accumulates the detections of one file (or one API year).
type firmsAgg struct {
	m       map[firmsKey]*firmsAcc
	product string
	from    time.Time // detections outside [from, to) are ignored
	to      time.Time
	// counters for the log
	kept, low, bad, outside int
}

// add counts one record. Rows below the confidence floor, with a
// non-numeric confidence (VIIRS uses letters) or unparsable fields are dropped.
func (a *firmsAgg) add(rec []string, idx [5]int) {
	conf, err := strconv.ParseFloat(strings.TrimSpace(rec[idx[3]]), 64)
	if err != nil {
		a.bad++
		return
	}
	lat, err1 := strconv.ParseFloat(strings.TrimSpace(rec[idx[0]]), 64)
	lon, err2 := strconv.ParseFloat(strings.TrimSpace(rec[idx[1]]), 64)
	day, err3 := time.Parse(time.DateOnly, strings.TrimSpace(rec[idx[2]]))
	if err1 != nil || err2 != nil || err3 != nil || !validCoordPair(lon, lat) {
		a.bad++
		return
	}
	if conf < firmsMinConfidence {
		a.low++
		return
	}
	if day.Before(a.from) || !day.Before(a.to) {
		a.outside++
		return
	}
	frp := 0.0 // an empty FRP counts as 0
	if v, err := strconv.ParseFloat(strings.TrimSpace(rec[idx[4]]), 64); err == nil && !math.IsNaN(v) && !math.IsInf(v, 0) {
		frp = v
	}
	k := firmsKey{int16(day.Year()), int16(day.Month()), int32(math.Floor(lat / firmsCellDeg)), int32(math.Floor(lon / firmsCellDeg))}
	c := a.m[k]
	if c == nil {
		c = &firmsAcc{}
		a.m[k] = c
	}
	c.n++
	c.frp += frp
	a.kept++
}

// cells returns the accumulated cells in key order.
func (a *firmsAgg) cells() []domain.FireCell {
	out := make([]domain.FireCell, 0, len(a.m))
	for k, c := range a.m {
		out = append(out, domain.FireCell{Year: k.year, Month: k.month, LatIdx: k.latIdx, LonIdx: k.lonIdx,
			Detections: c.n, FRPSum: float32(c.frp), Product: a.product})
	}
	slices.SortFunc(out, func(x, y domain.FireCell) int {
		for _, d := range [...]int{int(x.Year) - int(y.Year), int(x.Month) - int(y.Month),
			int(x.LatIdx) - int(y.LatIdx), int(x.LonIdx) - int(y.LonIdx)} {
			if d != 0 {
				return d
			}
		}
		return 0
	})
	return out
}

// readCSV streams one CSV body into a.
func (a *firmsAgg) readCSV(r io.Reader, name string) error {
	cr := csv.NewReader(bufio.NewReaderSize(r, 1<<20))
	cr.FieldsPerRecord = -1
	cr.ReuseRecord = true
	head, err := cr.Read()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil // an empty file has no header
		}
		return fmt.Errorf("%s: header: %w", name, err)
	}
	var idx [5]int
	for i, col := range firmsColumns {
		idx[i] = slices.IndexFunc(head, func(h string) bool {
			return strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(h, "\ufeff")), col)
		})
		if idx[i] < 0 {
			// The API answers a bad key or an exceeded limit with plain
			// text, not CSV; the text is not echoed (it could hold the URL).
			return fmt.Errorf("%s: header lacks column %q (%d columns)", name, col, len(head))
		}
	}
	need := slices.Max(idx[:]) + 1
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			var pe *csv.ParseError
			if errors.As(err, &pe) {
				a.bad++
				continue
			}
			return fmt.Errorf("%s: %w", name, err)
		}
		if len(rec) < need {
			a.bad++
			continue
		}
		a.add(rec, idx)
	}
}

func (s *FIRMS) newAgg(w ingest.Window) *firmsAgg {
	return &firmsAgg{m: map[firmsKey]*firmsAcc{}, product: s.Product, from: dayStart(w.From), to: w.To.UTC()}
}

func dayStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// FetchCells emits the aggregated cells of w with ISO3 assigned, one file
// (or one year of API calls) at a time. Archive CSVs win over the API.
//
// A cell that two CSVs both touch (per-country files meeting at a border)
// is emitted twice; the upsert keeps the last. Give it whole-world files.
func (s *FIRMS) FetchCells(ctx context.Context, w ingest.Window, emit func(domain.FireCell) error) error {
	if !w.From.Before(w.To) {
		return nil
	}
	if s.Product == "" {
		return errors.New("firms: product is empty")
	}
	files, err := filepath.Glob(filepath.Join(s.Dir, "*.csv"))
	if err != nil {
		return err
	}
	slices.Sort(files)
	switch {
	case len(files) > 0:
		return s.fromFiles(ctx, w, files, emit)
	case s.MapKey != "":
		return s.fromAPI(ctx, w, emit)
	}
	return ErrFIRMSNoInput
}

func (s *FIRMS) fromFiles(ctx context.Context, w ingest.Window, files []string, emit func(domain.FireCell) error) error {
	for _, path := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		fh, err := os.Open(path)
		if err != nil {
			return err
		}
		agg := s.newAgg(w)
		err = agg.readCSV(fh, filepath.Base(path))
		fh.Close()
		if err != nil {
			return err
		}
		if err := s.flush(ctx, filepath.Base(path), agg, emit); err != nil {
			return err
		}
	}
	return nil
}

// firmsAPIMin is the start of the MODIS record; earlier API calls return nothing.
var firmsAPIMin = time.Date(2000, 11, 1, 0, 0, 0, 0, time.UTC)

// clampFIRMSStart raises a window start before the MODIS era to 2000-11-01
// (the zero time included, which would otherwise mean 1900 upstream).
func clampFIRMSStart(from time.Time) time.Time {
	if from.Before(firmsAPIMin) {
		return firmsAPIMin
	}
	return from
}

// fromAPI walks the window in 5-day calls and flushes the map each time a
// call starts in a new year. A call straddling New Year puts its January
// rows in the same map; they are flushed with the next year.
func (s *FIRMS) fromAPI(ctx context.Context, w ingest.Window, emit func(domain.FireCell) error) error {
	base := s.BaseURL
	if base == "" {
		base = FIRMSBaseURL
	}
	w.From = clampFIRMSStart(w.From)
	agg := s.newAgg(w)
	now := s.now().UTC()
	year := dayStart(w.From).Year()
	for day := dayStart(w.From); day.Before(w.To); day = day.AddDate(0, 0, firmsDays) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if y := day.Year(); y != year {
			if err := s.flushUpTo(ctx, agg, year, emit); err != nil {
				return err
			}
			year = y
		}
		url := fmt.Sprintf("%s/api/area/csv/%s/%s/world/%d/%s", base, s.MapKey, s.Product, firmsDays, day.Format(time.DateOnly))
		f := s.F
		if day.AddDate(0, 0, firmsDays+firmsRefreshDays).After(now) {
			fc := *s.F // same limiter and client; recent days are still being filled in
			fc.Refresh = true
			f = &fc
		}
		body, err := f.Get(ctx, "firms", url)
		if err != nil {
			return fmt.Errorf("firms: area API %s: %w", day.Format(time.DateOnly), err)
		}
		err = agg.readCSV(body, "area API "+day.Format(time.DateOnly))
		body.Close()
		if err != nil {
			return err
		}
	}
	return s.flush(ctx, "area API", agg, emit)
}

// flushUpTo emits and drops the cells of years <= year.
func (s *FIRMS) flushUpTo(ctx context.Context, agg *firmsAgg, year int, emit func(domain.FireCell) error) error {
	part := &firmsAgg{m: map[firmsKey]*firmsAcc{}, product: agg.product, kept: agg.kept, low: agg.low, bad: agg.bad, outside: agg.outside}
	for k, c := range agg.m {
		if int(k.year) <= year {
			part.m[k] = c
			delete(agg.m, k)
		}
	}
	return s.flush(ctx, fmt.Sprintf("area API %d", year), part, emit)
}

// flush assigns countries to agg's cells in batches, emits them and logs the
// counts and the memory in use (Guide M15: report memory per file).
func (s *FIRMS) flush(ctx context.Context, name string, agg *firmsAgg, emit func(domain.FireCell) error) error {
	cells := agg.cells()
	for i := 0; i < len(cells); i += firmsAssignBatch {
		part := cells[i:min(i+firmsAssignBatch, len(cells))]
		if s.Assign != nil {
			if err := s.Assign(ctx, part); err != nil {
				return fmt.Errorf("firms: assign countries: %w", err)
			}
		}
		for _, c := range part {
			if err := emit(c); err != nil {
				return err
			}
		}
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	s.log().Info("firms file done", "file", name, "cells", len(cells), "detections", agg.kept,
		"below_confidence", agg.low, "unusable", agg.bad, "outside_window", agg.outside,
		"heap_mb", ms.HeapAlloc>>20, "sys_mb", ms.Sys>>20)
	clear(agg.m)
	return ctx.Err()
}

var _ ingest.FireSource = (*FIRMS)(nil)
