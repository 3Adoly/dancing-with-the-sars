package postgres

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/dwts/backend/internal/domain"
	"github.com/dwts/backend/internal/ingest"
)

const wSource = "wtest" // every row these tests write; deleted again afterwards

// wInc builds an incident the way ingest.Finalize leaves it: RawHash and
// SearchText set. The hash covers the title and coordinates, so changing
// either one is a content change.
func wInc(id, title string, lon, lat float64) domain.Incident {
	sum := md5.Sum([]byte(fmt.Sprintf("%s|%s|%v|%v", id, title, lon, lat)))
	mag := float32(6.5)
	deaths := int32(12)
	end := time.Date(2020, 5, 2, 0, 0, 0, 0, time.UTC)
	return domain.Incident{
		Source: wSource, SourceID: id, Type: domain.Earthquake, Title: title,
		StartTime: time.Date(2020, 5, 1, 10, 30, 15, 0, time.UTC), EndTime: &end, TimePrecision: "second", Year: 2020,
		Lon: lon, Lat: lat, Magnitude: &mag, MagnitudeUnit: "Mw", Severity: 2, Deaths: &deaths,
		CountryHints: []string{"JPN", "FRA"}, URL: "https://example.org/" + id,
		Props:      map[string]any{"depthKm": 10.5, "felt": 3},
		RawHash:    hex.EncodeToString(sum[:]),
		SearchText: strings.ToLower(title) + " " + id,
	}
}

// wClean removes the rows and runs this file's tests write (dwts_test only).
func wClean(t *testing.T, db *DB) {
	t.Helper()
	del := func() {
		for _, q := range []string{
			"DELETE FROM incidents WHERE source = '" + wSource + "'",
			"DELETE FROM ingest_runs WHERE source = '" + wSource + "'",
		} {
			if _, err := db.Pool.Exec(context.Background(), q); err != nil {
				t.Errorf("cleanup %q: %v", q, err)
			}
		}
	}
	del()
	t.Cleanup(del)
}

func wCount(t *testing.T, ctx context.Context, db *DB) (n int) {
	t.Helper()
	if err := db.Pool.QueryRow(ctx, "SELECT count(*) FROM incidents WHERE source = $1", wSource).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestUpsertIncidentsIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := newTestDB(t, ctx)
	resetIncidents(t, ctx, db) // empties incidents and loads the FRA/JPN/BOW fixture
	wClean(t, db)

	storm := wInc("storm1", "Storm One", 139, 35)
	storm.Type = domain.Storm
	storm.TrackWKT = "LINESTRING(145 25,139 35,141 40)"
	storm.Name = "ONE"
	storm.Description = "a storm"
	batch := []domain.Incident{
		wInc("a", "Quake A", 139, 36),
		wInc("b", "Quake B", 143.5, 38),
		storm,
	}

	if n, err := db.UpsertIncidents(ctx, batch); err != nil || n != 3 {
		t.Fatalf("first upsert = %d, %v; want 3", n, err)
	}
	if n, err := db.UpsertIncidents(ctx, batch); err != nil || n != 0 {
		t.Fatalf("second upsert of the same batch = %d, %v; want 0 (no-op)", n, err)
	}
	if got := wCount(t, ctx, db); got != 3 {
		t.Fatalf("rows = %d, want 3", got)
	}

	t.Run("columns", func(t *testing.T) {
		var (
			name, desc, unit, url, raw, hash, search, prec *string
			typ                                            string
			year, sev                                      int16
			lon, lat                                       float64
			mag                                            *float32
			deaths                                         *int32
			hints                                          []string
			props                                          string
			hasTrack                                       bool
			start, end                                     time.Time
			method                                         *string
		)
		err := db.Pool.QueryRow(ctx, `
SELECT name, description, magnitude_unit, url, raw::text, raw_hash, search_text, time_precision, type, year, severity,
       ST_X(geom), ST_Y(geom), magnitude, deaths, country_hints, props::text, track IS NOT NULL, start_time, end_time, country_method
  FROM incidents WHERE source = $1 AND source_id = 'storm1'`, wSource).Scan(
			&name, &desc, &unit, &url, &raw, &hash, &search, &prec, &typ, &year, &sev,
			&lon, &lat, &mag, &deaths, &hints, &props, &hasTrack, &start, &end, &method)
		if err != nil {
			t.Fatal(err)
		}
		if name == nil || *name != "ONE" || desc == nil || *desc != "a storm" || unit == nil || *unit != "Mw" {
			t.Errorf("text columns: name=%v desc=%v unit=%v", name, desc, unit)
		}
		if raw != nil {
			t.Errorf("raw = %q, want NULL (R-B.2)", *raw)
		}
		if hash == nil || *hash != storm.RawHash || search == nil || *search != storm.SearchText {
			t.Errorf("raw_hash/search_text = %v / %v", hash, search)
		}
		if typ != "storm" || year != 2020 || sev != 2 || *prec != "second" {
			t.Errorf("type=%s year=%d severity=%d precision=%s", typ, year, sev, *prec)
		}
		if lon != 139 || lat != 35 {
			t.Errorf("point = %v,%v", lon, lat)
		}
		if mag == nil || *mag != 6.5 || deaths == nil || *deaths != 12 {
			t.Errorf("magnitude=%v deaths=%v", mag, deaths)
		}
		if len(hints) != 2 || hints[0] != "JPN" || hints[1] != "FRA" {
			t.Errorf("country_hints = %v", hints)
		}
		var p map[string]any
		if err := json.Unmarshal([]byte(props), &p); err != nil || p["depthKm"] != 10.5 || p["felt"] != float64(3) {
			t.Errorf("props = %s (%v)", props, err)
		}
		if !hasTrack {
			t.Error("track was not stored")
		}
		if !start.Equal(storm.StartTime) || !end.Equal(*storm.EndTime) {
			t.Errorf("times = %v / %v", start, end)
		}
		if method != nil {
			t.Errorf("country_method = %q, want NULL before postprocess", *method)
		}

		// Optional text columns are NULL, not empty strings, when unset.
		var nullName, nullTrack bool
		if err := db.Pool.QueryRow(ctx, `SELECT name IS NULL AND description IS NULL, track IS NULL FROM incidents WHERE source = $1 AND source_id = 'a'`, wSource).Scan(&nullName, &nullTrack); err != nil {
			t.Fatal(err)
		}
		if !nullName || !nullTrack {
			t.Errorf("unset name/description/track must be NULL (name+desc null=%v, track null=%v)", nullName, nullTrack)
		}
	})

	t.Run("changed content updates only that row", func(t *testing.T) {
		before := map[string]time.Time{}
		rows, err := db.Pool.Query(ctx, "SELECT source_id, ingested_at FROM incidents WHERE source = $1", wSource)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id string
			var at time.Time
			if err := rows.Scan(&id, &at); err != nil {
				t.Fatal(err)
			}
			before[id] = at
		}
		rows.Close()

		changed := append([]domain.Incident(nil), batch...)
		changed[1] = wInc("b", "Quake B revised", 143.5, 38)
		if n, err := db.UpsertIncidents(ctx, changed); err != nil || n != 1 {
			t.Fatalf("upsert after one change = %d, %v; want 1", n, err)
		}
		var title string
		var at, atA time.Time
		if err := db.Pool.QueryRow(ctx, "SELECT title, ingested_at FROM incidents WHERE source = $1 AND source_id = 'b'", wSource).Scan(&title, &at); err != nil {
			t.Fatal(err)
		}
		if title != "Quake B revised" || !at.After(before["b"]) {
			t.Errorf("row b: title=%q ingested_at moved=%v", title, at.After(before["b"]))
		}
		if err := db.Pool.QueryRow(ctx, "SELECT ingested_at FROM incidents WHERE source = $1 AND source_id = 'a'", wSource).Scan(&atA); err != nil {
			t.Fatal(err)
		}
		if !atA.Equal(before["a"]) {
			t.Error("untouched row a must keep its ingested_at")
		}
	})

	t.Run("geometry change resets the country, other changes keep it", func(t *testing.T) {
		if err := db.Postprocess(ctx); err != nil {
			t.Fatal(err)
		}
		state := func(id string) (method, iso *string) {
			t.Helper()
			err := db.Pool.QueryRow(ctx, "SELECT country_method, primary_iso3 FROM incidents WHERE source = $1 AND source_id = $2", wSource, id).Scan(&method, &iso)
			if err != nil {
				t.Fatal(err)
			}
			return method, iso
		}
		show := func(p *string) string {
			if p == nil {
				return "NULL"
			}
			return *p
		}
		if m, iso := state("a"); m == nil || *m != "contains" || iso == nil || *iso != "JPN" {
			t.Fatalf("setup: postprocess should place a in JPN by containment, got %s / %s", show(m), show(iso))
		}

		// Same point, new title: the country assignment stays.
		titleOnly := wInc("a", "Quake A, new title", 139, 36)
		if n, err := db.UpsertIncidents(ctx, []domain.Incident{titleOnly}); err != nil || n != 1 {
			t.Fatalf("title change = %d, %v", n, err)
		}
		if m, iso := state("a"); m == nil || *m != "contains" || iso == nil || *iso != "JPN" {
			t.Errorf("a non-geometry change must keep the country, got %s / %s", show(m), show(iso))
		}

		// New point (far out in the ocean): both reset so postprocess can reassign.
		// (No country hints, or postprocess P3 would assign the hinted country.)
		moved := wInc("a", "Quake A, new title", -150, 0)
		moved.CountryHints = nil
		if n, err := db.UpsertIncidents(ctx, []domain.Incident{moved}); err != nil || n != 1 {
			t.Fatalf("geometry change = %d, %v", n, err)
		}
		if m, iso := state("a"); m != nil || iso != nil {
			t.Errorf("a moved point must reset country_method and primary_iso3 to NULL, got %s / %s", show(m), show(iso))
		}
		if err := db.Postprocess(ctx); err != nil {
			t.Fatal(err)
		}
		if m, iso := state("a"); m == nil || *m != "ocean" || iso != nil {
			t.Errorf("after postprocess the moved point should be ocean, got %s / %s", show(m), show(iso))
		}
	})

	t.Run("duplicates inside a batch are collapsed, last wins", func(t *testing.T) {
		in := []domain.Incident{
			wInc("dup", "first version", 10, 10),
			wInc("solo", "solo", 11, 11),
			wInc("dup", "last version", 12, 12),
		}
		n, err := db.UpsertIncidents(ctx, in)
		if err != nil || n != 2 {
			t.Fatalf("upsert with a duplicate = %d, %v; want 2", n, err)
		}
		var title string
		var lon float64
		if err := db.Pool.QueryRow(ctx, "SELECT title, ST_X(geom) FROM incidents WHERE source = $1 AND source_id = 'dup'", wSource).Scan(&title, &lon); err != nil {
			t.Fatal(err)
		}
		if title != "last version" || lon != 12 {
			t.Errorf("dup row = %q @ %v, want the last occurrence", title, lon)
		}
		if in[0].Title != "first version" || len(in) != 3 {
			t.Error("the caller's slice must not be modified")
		}
	})

	t.Run("empty batch", func(t *testing.T) {
		if n, err := db.UpsertIncidents(ctx, nil); err != nil || n != 0 {
			t.Errorf("empty batch = %d, %v", n, err)
		}
	})

	t.Run("unfinalized incident is refused", func(t *testing.T) {
		bad := wInc("nohash", "x", 1, 1)
		bad.RawHash = ""
		if _, err := db.UpsertIncidents(ctx, []domain.Incident{bad}); err == nil || !strings.Contains(err.Error(), "Finalize") {
			t.Errorf("err = %v, want a hint to run Finalize", err)
		}
	})

	t.Run("a failing row rolls the whole batch back", func(t *testing.T) {
		before := wCount(t, ctx, db)
		good := wInc("rollback-good", "good", 5, 5)
		bad := wInc("rollback-bad", "bad", 6, 6)
		bad.Type = "bogus" // violates the type CHECK
		if _, err := db.UpsertIncidents(ctx, []domain.Incident{good, bad}); err == nil {
			t.Fatal("want a CHECK violation")
		}
		if got := wCount(t, ctx, db); got != before {
			t.Errorf("rows = %d, want %d: the failed batch must leave nothing behind", got, before)
		}
	})
}

func TestStartFinishRunIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	db := newTestDB(t, ctx)
	wClean(t, db)

	type run struct {
		status   string
		rows     *int
		errText  *string
		finished bool
	}
	get := func(id int64) (r run) {
		t.Helper()
		err := db.Pool.QueryRow(ctx, "SELECT status, rows, error, finished_at IS NOT NULL FROM ingest_runs WHERE id = $1", id).Scan(&r.status, &r.rows, &r.errText, &r.finished)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	id, err := db.StartRun(ctx, wSource)
	if err != nil {
		t.Fatal(err)
	}
	if r := get(id); r.status != "running" || r.finished || r.rows != nil {
		t.Errorf("after StartRun: %+v", r)
	}
	if err := db.FinishRun(ctx, id, 123, nil); err != nil {
		t.Fatal(err)
	}
	if r := get(id); r.status != "ok" || !r.finished || r.rows == nil || *r.rows != 123 || r.errText != nil {
		t.Errorf("after ok FinishRun: %+v", r)
	}

	id2, _ := db.StartRun(ctx, wSource)
	if id2 == id {
		t.Error("each run needs its own id")
	}
	long := fmt.Errorf("boom %s é", strings.Repeat("é", 3000))
	if err := db.FinishRun(ctx, id2, 7, long); err != nil {
		t.Fatal(err)
	}
	r := get(id2)
	if r.status != "failed" || !r.finished || r.rows == nil || *r.rows != 7 || r.errText == nil || !strings.HasPrefix(*r.errText, "boom ") {
		t.Fatalf("after failed FinishRun: %+v", r)
	}
	if len(*r.errText) > 2000 || !strings.HasSuffix(*r.errText, "é") {
		t.Errorf("error text must be cut to 2000 bytes on a rune boundary, got %d bytes", len(*r.errText))
	}

	// An upstream body quoted in an error may carry NUL bytes or Latin-1:
	// Postgres text rejects both, and the run must still be recorded.
	id3, _ := db.StartRun(ctx, wSource)
	if err := db.FinishRun(ctx, id3, 0, errors.New("bad body: a\x00b Caf\xe9")); err != nil {
		t.Fatalf("FinishRun with NUL/invalid UTF-8 error text: %v", err)
	}
	if r := get(id3); r.status != "failed" || r.errText == nil || *r.errText != "bad body: ab Caf\uFFFD" {
		t.Errorf("sanitized error text = %+v", r)
	}

	if err := db.FinishRun(ctx, 1<<40, 0, nil); err == nil {
		t.Error("finishing an unknown run must fail")
	}
}

// fakeSource emits a fixed list of incidents, with or without the source
// record in Raw.
type fakeSource struct {
	incs []domain.Incident
}

func (fakeSource) Name() string { return wSource }
func (s fakeSource) Fetch(ctx context.Context, w ingest.Window, emit func(domain.Incident) error) error {
	for _, inc := range s.incs {
		if err := emit(inc); err != nil {
			return err
		}
	}
	return nil
}

// TestRunnerIdempotentIntegration drives Runner -> Finalize -> UpsertIncidents
// against Postgres: a re-run of unchanged data must write 0 rows, with the
// hash taken from Raw and with the hash derived from the normalized incident.
func TestRunnerIdempotentIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	db := newTestDB(t, ctx)
	wClean(t, db)

	for _, withRaw := range []bool{true, false} {
		t.Run(fmt.Sprintf("raw=%v", withRaw), func(t *testing.T) {
			wClean(t, db)
			mk := func(id, title string) domain.Incident {
				inc := domain.Incident{
					Source: wSource, SourceID: id, Type: domain.Flood, Title: title,
					StartTime: time.Date(2015, 7, 4, 0, 0, 0, 0, time.UTC), TimePrecision: "day", Lon: 20.5, Lat: -10.25,
					Props: map[string]any{"b": 2, "a": 1, "c": []int{3}}, CountryHints: []string{"zmb"},
				}
				if withRaw {
					inc.Raw = json.RawMessage(fmt.Sprintf(`{"id":%q,"title":%q}`, id, title))
				}
				return inc
			}
			src := fakeSource{incs: []domain.Incident{mk("1", "Flood one"), mk("2", "Flood two"), mk("3", "Flood tres é")}}
			r := &ingest.Runner{W: db, Log: db.Log, BatchSize: 2, MinYear: 1900}

			if n, err := r.Run(ctx, src, ingest.Window{}); err != nil || n != 3 {
				t.Fatalf("first run = %d, %v; want 3", n, err)
			}
			if n, err := r.Run(ctx, src, ingest.Window{}); err != nil || n != 0 {
				t.Fatalf("second run = %d, %v; want 0", n, err)
			}
			src.incs[1] = mk("2", "Flood two, corrected")
			if n, err := r.Run(ctx, src, ingest.Window{}); err != nil || n != 1 {
				t.Fatalf("run after one change = %d, %v; want 1", n, err)
			}

			var runs, ok int
			var lastRows int
			if err := db.Pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE status = 'ok'), (SELECT rows FROM ingest_runs WHERE source = $1 ORDER BY id DESC LIMIT 1) FROM ingest_runs WHERE source = $1`, wSource).Scan(&runs, &ok, &lastRows); err != nil {
				t.Fatal(err)
			}
			if runs != 3 || ok != 3 || lastRows != 1 {
				t.Errorf("ingest_runs: %d runs, %d ok, last rows %d; want 3, 3, 1", runs, ok, lastRows)
			}
			var st, hints string
			if err := db.Pool.QueryRow(ctx, "SELECT search_text, country_hints::text FROM incidents WHERE source = $1 AND source_id = '3'", wSource).Scan(&st, &hints); err != nil {
				t.Fatal(err)
			}
			if st != "flood tres e flood 2015 3" || hints != "{ZMB}" {
				t.Errorf("search_text = %q, hints = %s", st, hints)
			}
		})
	}
}

// TestUpsertIncidents5000Integration is the M4 performance check: 5000 rows
// in one batch should take well under a second. The assertion is loose (a
// loaded CI box must not fail it); the measured times are logged.
func TestUpsertIncidents5000Integration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := newTestDB(t, ctx)
	wClean(t, db)

	const n = 5000
	batch := make([]domain.Incident, n)
	for i := range batch {
		// A deterministic scatter over the globe.
		lon := math.Mod(float64(i)*7.31, 360) - 180
		lat := math.Mod(float64(i)*3.17, 170) - 85
		batch[i] = wInc(fmt.Sprintf("perf-%05d", i), fmt.Sprintf("M 5.%d - synthetic quake %d", i%10, i), lon, lat)
		if i%10 == 0 {
			batch[i].Type = domain.Storm
			batch[i].TrackWKT = fmt.Sprintf("LINESTRING(%v %v,%v %v,%v %v)", lon, lat, lon+1, lat+1, lon+2, lat+1)
		}
	}

	timed := func(label string, want int) time.Duration {
		start := time.Now()
		got, err := db.UpsertIncidents(ctx, batch)
		d := time.Since(start)
		if err != nil || got != want {
			t.Fatalf("%s: %d rows, %v; want %d", label, got, err, want)
		}
		t.Logf("%s: %d rows in %v", label, got, d.Round(time.Millisecond))
		return d
	}
	insert := timed("insert 5000", n)
	timed("re-run (no-op)", 0)
	for i := range batch { // new hash on every row: all 5000 are updated
		batch[i] = wInc(batch[i].SourceID, batch[i].Title+" (rev)", batch[i].Lon, batch[i].Lat)
	}
	timed("update 5000", n)

	if insert > 5*time.Second {
		t.Errorf("inserting 5000 rows took %v, target is < 1s", insert)
	}
}
