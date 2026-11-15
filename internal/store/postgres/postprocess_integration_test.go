package postgres

import (
	"context"
	"math"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dwts/backend/internal/domain"
)

// seedPath is the M3 development seed, relative to this package.
const seedPath = "../../../testdata/seed.sql"

// resetIncidents empties every table Postprocess writes and reloads the
// three-country fixture (FRA, JPN = lon 130..142 lat 31..45, BOW).
func resetIncidents(t testing.TB, ctx context.Context, db *DB) {
	t.Helper()
	if _, err := db.Pool.Exec(ctx, "TRUNCATE incidents, countries RESTART IDENTITY CASCADE"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.LoadCountries(ctx, fixturePath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.Pool.Exec(context.Background(), "TRUNCATE incidents, countries RESTART IDENTITY CASCADE"); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
}

func datasetVersion(t *testing.T, ctx context.Context, db *DB) int64 {
	t.Helper()
	var v int64
	if err := db.Pool.QueryRow(ctx, "SELECT version FROM dataset_meta").Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// TestPostprocessIntegration checks each M3 step on a handful of incidents
// placed around the fixture countries, then runs it again to show it is
// idempotent apart from the version bump.
func TestPostprocessIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := newTestDB(t, ctx)
	resetIncidents(t, ctx, db)

	_, err := db.Pool.Exec(ctx, `
INSERT INTO incidents (source, source_id, type, title, start_time, year, geom, track, country_hints, raw_hash, search_text) VALUES
 ('t', 'inland',  'earthquake', 'Inside Japan',        '2011-03-11T05:46:24Z', 2011, 'SRID=4326;POINT(139 36)',   NULL, NULL, 'h1', 'inside japan'),
 ('t', 'coast',   'earthquake', '131 km east of JPN',  '2011-03-11T06:00:00Z', 2011, 'SRID=4326;POINT(143.5 38)', NULL, NULL, 'h2', 'coast'),
 ('t', 'deep',    'earthquake', 'Mid Pacific',         '1999-01-01T00:00:00Z', 1999, 'SRID=4326;POINT(-150 0)',   NULL, NULL, 'h3', 'mid pacific'),
 ('t', 'hinted',  'flood',      'Far away, hint FRA',  '2002-08-07T00:00:00Z', 2002, 'SRID=4326;POINT(-30 -40)',  NULL, ARRAY['FRA'], 'h4', 'hinted'),
 ('t', 'storm',   'storm',      'Storm hitting JPN',   '2019-10-06T00:00:00Z', 2019, 'SRID=4326;POINT(139 35)',
   'SRID=4326;LINESTRING(145 25,139 35,141 40)', ARRAY['JPN','FRA','XXX'], 'h5', 'storm')`)
	if err != nil {
		t.Fatal(err)
	}

	type assigned struct{ iso, method string }
	want := map[string]assigned{
		"inland": {"JPN", "contains"},
		"coast":  {"JPN", "nearest"},
		"deep":   {"", "ocean"},
		"hinted": {"FRA", "source"},
		"storm":  {"JPN", "contains"},
	}
	wantCountries := map[string]string{"inland": "JPN", "coast": "JPN", "deep": "", "hinted": "FRA", "storm": "FRA,JPN"}

	for run := 1; run <= 2; run++ {
		before := datasetVersion(t, ctx, db)
		if err := db.Postprocess(ctx); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		if got := datasetVersion(t, ctx, db); got != before+1 {
			t.Errorf("run %d: version = %d, want %d", run, got, before+1)
		}

		rows, err := db.Pool.Query(ctx, `
SELECT i.source_id, coalesce(i.primary_iso3, ''), coalesce(i.country_method, ''),
       coalesce((SELECT string_agg(iso3, ',' ORDER BY iso3) FROM incident_countries ic WHERE ic.incident_id = i.id), '')
  FROM incidents i`)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for rows.Next() {
			var id, iso, method, list string
			if err := rows.Scan(&id, &iso, &method, &list); err != nil {
				t.Fatal(err)
			}
			n++
			if got := (assigned{iso, method}); got != want[id] {
				t.Errorf("run %d: %s assigned %+v, want %+v", run, id, got, want[id])
			}
			if list != wantCountries[id] {
				t.Errorf("run %d: %s incident_countries = %q, want %q", run, id, list, wantCountries[id])
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if n != len(want) {
			t.Fatalf("run %d: %d incidents, want %d", run, n, len(want))
		}

		// mv_counts: one row per (type, country, year); the ocean quake counts under ''.
		var jpnEQ, ocean, totals int
		err = db.Pool.QueryRow(ctx, `
SELECT coalesce(sum(n) FILTER (WHERE type = 'earthquake' AND iso3 = 'JPN' AND year = 2011), 0),
       coalesce(sum(n) FILTER (WHERE iso3 = ''), 0),
       (SELECT coalesce(sum(n), 0) FROM mv_totals WHERE type <> 'fire_activity')
  FROM mv_counts`).Scan(&jpnEQ, &ocean, &totals)
		if err != nil {
			t.Fatal(err)
		}
		if jpnEQ != 2 || ocean != 1 || totals != 5 {
			t.Errorf("run %d: mv_counts JPN/earthquake/2011 = %d (want 2), ocean = %d (want 1); mv_totals = %d (want 5)", run, jpnEQ, ocean, totals)
		}
	}

	// A new incident is assigned on the next run; assigned ones keep their country.
	if _, err := db.Pool.Exec(ctx, `INSERT INTO incidents (source, source_id, type, title, start_time, year, geom, raw_hash, search_text)
	                                VALUES ('t', 'paris', 'flood', 'Paris', '2016-06-01', 2016, 'SRID=4326;POINT(2.35 48.85)', 'h6', 'paris')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Postprocess(ctx); err != nil {
		t.Fatal(err)
	}
	var iso, method string
	if err := db.Pool.QueryRow(ctx, "SELECT primary_iso3, country_method FROM incidents WHERE source_id = 'paris'").Scan(&iso, &method); err != nil {
		t.Fatal(err)
	}
	if iso != "FRA" || method != "contains" {
		t.Errorf("paris = %s/%s, want FRA/contains", iso, method)
	}
}

// TestSeedIntegration loads testdata/seed.sql into dwts_test (with the
// fixture countries) and checks the M3 acceptance rules that do not depend on
// the full country set: every type has at least 10 rows, year is the UTC year,
// search_text is lowercase ASCII, ids are real-looking, and the sar_cache row
// points at the Türkiye 2023 earthquake.
func TestSeedIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := newTestDB(t, ctx)
	resetIncidents(t, ctx, db)

	seed, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	// No arguments: pgx sends the whole file with the simple protocol, so the
	// file's own BEGIN/COMMIT apply, exactly like psql in `make seed`.
	if _, err := db.Pool.Exec(ctx, string(seed)); err != nil {
		t.Fatalf("load seed: %v", err)
	}

	counts := map[string]int{}
	rows, err := db.Pool.Query(ctx, "SELECT type, count(*) FROM incidents GROUP BY 1")
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for rows.Next() {
		var typ string
		var n int
		if err := rows.Scan(&typ, &n); err != nil {
			t.Fatal(err)
		}
		counts[typ] = n
		total += n
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, typ := range []string{"earthquake", "volcano", "tsunami", "wildfire", "flood", "landslide", "storm", "drought", "other"} {
		if counts[typ] < 10 {
			t.Errorf("type %s has %d rows, want >= 10", typ, counts[typ])
		}
	}
	if total < 250 {
		t.Errorf("seed has %d incidents, want about 300", total)
	}

	checks := []struct{ name, sql string }{
		{"year is the UTC year of start_time", `SELECT count(*) FROM incidents WHERE year <> extract(year FROM start_time AT TIME ZONE 'UTC')`},
		{"raw is NULL (ruling R-B.2)", `SELECT count(*) FROM incidents WHERE raw IS NOT NULL`},
		{"raw_hash is an md5", `SELECT count(*) FROM incidents WHERE raw_hash !~ '^[0-9a-f]{32}$'`},
		{"end_time is not before start_time", `SELECT count(*) FROM incidents WHERE end_time < start_time`},
		{"IBTrACS storms have a track", `SELECT count(*) FROM incidents WHERE source = 'ibtracs' AND (track IS NULL OR ST_NPoints(track) < 2)`},
		{"only storms have a track", `SELECT count(*) FROM incidents WHERE type <> 'storm' AND track IS NOT NULL`},
		{"geometries are valid", `SELECT count(*) FROM incidents WHERE NOT ST_IsValid(geom) OR (track IS NOT NULL AND NOT ST_IsValid(track))`},
	}
	for _, c := range checks {
		var bad int
		if err := db.Pool.QueryRow(ctx, c.sql).Scan(&bad); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if bad != 0 {
			t.Errorf("%s: %d offending rows", c.name, bad)
		}
	}

	ascii := regexp.MustCompile(`^[\x20-\x7e]+$`)
	srows, err := db.Pool.Query(ctx, "SELECT source_id, search_text FROM incidents")
	if err != nil {
		t.Fatal(err)
	}
	for srows.Next() {
		var id, st string
		if err := srows.Scan(&id, &st); err != nil {
			t.Fatal(err)
		}
		if st != strings.ToLower(st) || !ascii.MatchString(st) || !strings.Contains(st, strings.ToLower(id)) {
			t.Errorf("%s: search_text %q must be lowercase ASCII and contain the source id", id, st)
		}
	}
	if err := srows.Err(); err != nil {
		t.Fatal(err)
	}

	// Guide checklist events, by their real catalog ids.
	for _, k := range []struct{ source, id string }{
		{"usgs", "us6000jllz"}, {"usgs", "official20110311054624120_30"}, {"usgs", "usp000h60h"},
		{"usgs", "official19600522191120_30"}, {"usgs", "us20002926"}, {"usgs", "official20041226005853450_30"},
		{"ibtracs", "2005236N23285"}, {"ibtracs", "2013306N07162"}, {"ibtracs", "2019063S18038"},
		{"eonet", "EONET_4027"}, {"gdacs", "FL-1101522"}, {"gdacs", "FL-1102204"},
	} {
		var n int
		if err := db.Pool.QueryRow(ctx, "SELECT count(*) FROM incidents WHERE source = $1 AND source_id = $2", k.source, k.id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("%s:%s rows = %d, want 1", k.source, k.id, n)
		}
	}
	var hints []string
	if err := db.Pool.QueryRow(ctx, "SELECT country_hints FROM incidents WHERE source = 'ibtracs' AND source_id = '2019063S18038'").Scan(&hints); err != nil {
		t.Fatal(err)
	}
	sort.Strings(hints)
	if strings.Join(hints, ",") != "MOZ,MWI,ZWE" {
		t.Errorf("Idai country_hints = %v, want MOZ, MWI, ZWE", hints)
	}

	var sarID, incID int64
	var sarSource string
	err = db.Pool.QueryRow(ctx, `SELECT (s.payload->>'incidentId')::bigint, s.incident_id, i.source || ':' || i.source_id
	                               FROM sar_cache s JOIN incidents i ON i.id = s.incident_id`).Scan(&sarID, &incID, &sarSource)
	if err != nil {
		t.Fatalf("sar_cache row: %v", err)
	}
	if sarID != incID || sarSource != "usgs:us6000jllz" {
		t.Errorf("sar_cache payload.incidentId = %d for incident %d (%s), want the us6000jllz incident id", sarID, incID, sarSource)
	}

	// The seed must postprocess cleanly; with only the fixture countries most rows are ocean or source.
	if err := db.Postprocess(ctx); err != nil {
		t.Fatal(err)
	}
	var unassigned, mv int
	if err := db.Pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM incidents WHERE country_method IS NULL), (SELECT count(*) FROM mv_counts)").Scan(&unassigned, &mv); err != nil {
		t.Fatal(err)
	}
	if unassigned != 0 || mv == 0 {
		t.Errorf("after postprocess: %d unassigned incidents (want 0), %d mv_counts rows (want > 0)", unassigned, mv)
	}
}

// TestPostprocessM5Integration covers the M5 steps on the fixture countries
// (JPN = lon 130..142 lat 31..45) plus a small test country on the
// antimeridian: P2 across ±180°, P4 clearing a stale country, P5(c) storm
// tracks within 100 km (including a high-latitude pass that the Guide's
// 1° prefilter would miss), P6 NOAA→USGS dedup with the deaths/damage copy,
// P7 NOAA→GVP, P8 counted rules and P10 coverage, run twice.
func TestPostprocessM5Integration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := newTestDB(t, ctx)
	resetIncidents(t, ctx, db)

	// TST: lon 179..180, lat -17..-16 and TSW: lon -180..-179, lat 10..11, one on
	// each side of the antimeridian.
	if _, err := db.Pool.Exec(ctx, `INSERT INTO countries (iso3, name, geom) VALUES
	   ('TST', 'Test Island', ST_Multi(ST_MakeEnvelope(179, -17, 180, -16, 4326))),
	   ('TSW', 'Test West', ST_Multi(ST_MakeEnvelope(-180, 10, -179, 11, 4326)))`); err != nil {
		t.Fatal(err)
	}
	_, err := db.Pool.Exec(ctx, `
INSERT INTO incidents (source, source_id, type, title, start_time, year, geom, track, deaths, props, primary_iso3, country_method, raw_hash, search_text) VALUES
 ('t',       'dateline',  'earthquake', 'East of 180',  '2010-01-01T00:00:00Z', 2010, 'SRID=4326;POINT(-179.5 -16.5)', NULL, NULL, '{}', NULL, NULL, 'a', 'a'),
 ('t',       'stale',     'earthquake', 'Stale FRA',    '2010-01-01T00:00:00Z', 2010, 'SRID=4326;POINT(-150 0)', NULL, NULL, '{}', 'FRA', NULL, 'a', 'a'),
 ('ibtracs', 'near',      'storm', 'Track 79 km off JPN',   '2015-08-01T00:00:00Z', 2015, 'SRID=4326;POINT(150 20)', 'SRID=4326;LINESTRING(142.9 33,142.9 38)', NULL, '{}', NULL, NULL, 'a', 'a'),
 ('ibtracs', 'far',       'storm', 'Track 140 km off JPN',  '2015-09-01T00:00:00Z', 2015, 'SRID=4326;POINT(150 20)', 'SRID=4326;LINESTRING(143.6 33,143.6 38)', NULL, '{}', NULL, NULL, 'a', 'a'),
 ('ibtracs', 'north',     'storm', 'Track 91 km off JPN at 45N', '2015-10-01T00:00:00Z', 2015, 'SRID=4326;POINT(150 20)', 'SRID=4326;LINESTRING(143.15 43,143.15 44.9)', NULL, '{}', NULL, NULL, 'a', 'a'),
 ('ibtracs', 'cross-e',   'storm', 'Crosses 180, 55 km N of TST', '2016-01-01T00:00:00Z', 2016, 'SRID=4326;POINT(150 20)', 'SRID=4326;LINESTRING(-179 -15.5,178 -15.5)', NULL, '{}', NULL, NULL, 'a', 'a'),
 ('ibtracs', 'cross-w',   'storm', 'Crosses 180, 55 km N of TSW', '2016-02-01T00:00:00Z', 2016, 'SRID=4326;POINT(150 20)', 'SRID=4326;LINESTRING(178 11.5,-178 11.5)', NULL, '{}', NULL, NULL, 'a', 'a'),
 ('ibtracs', 'cross-far', 'storm', 'Crosses 180, 167 km N of TSW', '2016-03-01T00:00:00Z', 2016, 'SRID=4326;POINT(150 20)', 'SRID=4326;LINESTRING(178 12.5,-178 12.5)', NULL, '{}', NULL, NULL, 'a', 'a'),
 ('usgs',    'main',      'earthquake', 'USGS main',    '2011-03-11T05:46:24Z', 2011, 'SRID=4326;POINT(139 36)', NULL, NULL, '{"depthKm": 10}', NULL, NULL, 'a', 'a'),
 ('usgs',    'after',     'earthquake', 'USGS after',   '2011-03-11T05:47:50Z', 2011, 'SRID=4326;POINT(139.5 36)', NULL, NULL, '{}', NULL, NULL, 'a', 'a'),
 ('noaa_eq', 'dup',       'earthquake', 'NOAA dup',     '2011-03-11T05:46:00Z', 2011, 'SRID=4326;POINT(139.2 36.1)', NULL, 18000, '{"damageAmountOrder": 4}', NULL, NULL, 'a', 'a'),
 ('noaa_eq', 'late',      'earthquake', 'NOAA 130 s',   '2011-03-11T05:52:00Z', 2011, 'SRID=4326;POINT(139 36)', NULL, 5, '{}', NULL, NULL, 'a', 'a'),
 ('noaa_eq', 'distant',   'earthquake', 'NOAA 150 km',  '2011-03-11T05:46:30Z', 2011, 'SRID=4326;POINT(137.3 36)', NULL, 7, '{}', NULL, NULL, 'a', 'a'),
 ('noaa_vo', 'v1',        'volcano', 'Fuji NOAA',       '2001-01-01T00:00:00Z', 2001, 'SRID=4326;POINT(138.7 35.3)', NULL, NULL, '{}', NULL, NULL, 'a', 'a'),
 ('gvp',     'g1',        'volcano', 'Fuji GVP',        '2001-02-01T00:00:00Z', 2001, 'SRID=4326;POINT(138.7 35.3)', NULL, NULL, '{}', NULL, NULL, 'a', 'a'),
 ('noaa_vo', 'v2',        'volcano', 'Fuji other year', '2002-01-01T00:00:00Z', 2002, 'SRID=4326;POINT(138.7 35.3)', NULL, NULL, '{}', NULL, NULL, 'a', 'a'),
 ('eonet',   'e-eq',      'earthquake', 'x', '2019-01-01', 2019, 'SRID=4326;POINT(139 36)', NULL, NULL, '{}', NULL, NULL, 'a', 'a'),
 ('eonet',   'e-storm',   'storm',      'x', '2019-01-01', 2019, 'SRID=4326;POINT(139 36)', NULL, NULL, '{}', NULL, NULL, 'a', 'a'),
 ('eonet',   'e-flood',   'flood',      'x', '2023-01-01', 2023, 'SRID=4326;POINT(139 36)', NULL, NULL, '{}', NULL, NULL, 'a', 'a'),
 ('eonet',   'e-ls-2017', 'landslide',  'x', '2017-12-31', 2017, 'SRID=4326;POINT(139 36)', NULL, NULL, '{}', NULL, NULL, 'a', 'a'),
 ('eonet',   'e-ls-2018', 'landslide',  'x', '2018-01-01', 2018, 'SRID=4326;POINT(139 36)', NULL, NULL, '{}', NULL, NULL, 'a', 'a'),
 ('eonet',   'e-wf',      'wildfire',   'x', '2019-01-01', 2019, 'SRID=4326;POINT(139 36)', NULL, NULL, '{}', NULL, NULL, 'a', 'a'),
 ('eonet',   'e-other',   'other',      'x', '2019-01-01', 2019, 'SRID=4326;POINT(139 36)', NULL, NULL, '{}', NULL, NULL, 'a', 'a'),
 ('gdacs',   'g-fl-2021', 'flood',      'x', '2021-06-01', 2021, 'SRID=4326;POINT(139 36)', NULL, NULL, '{}', NULL, NULL, 'a', 'a'),
 ('gdacs',   'g-fl-2022', 'flood',      'x', '2022-06-01', 2022, 'SRID=4326;POINT(139 36)', NULL, NULL, '{}', NULL, NULL, 'a', 'a'),
 ('gdacs',   'g-wf',      'wildfire',   'x', '2022-06-01', 2022, 'SRID=4326;POINT(139 36)', NULL, NULL, '{}', NULL, NULL, 'a', 'a'),
 ('gdacs',   'g-dr',      'drought',    'x', '2022-06-01', 2022, 'SRID=4326;POINT(139 36)', NULL, NULL, '{}', NULL, NULL, 'a', 'a')`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE incidents SET name = 'Fuji' WHERE source IN ('noaa_vo', 'gvp')`); err != nil {
		t.Fatal(err)
	}

	notCounted := map[string]bool{
		"noaa_eq:dup": true, "noaa_vo:v1": true,
		"eonet:e-eq": true, "eonet:e-storm": true, "eonet:e-flood": true, "eonet:e-ls-2017": true,
		"gdacs:g-fl-2021": true, "gdacs:g-wf": true,
	}
	type row struct {
		iso, method, countries string
		dupOf                  string
		counted                bool
		deaths                 *int32
		damage                 *string
	}
	var firstCounts string
	for run := 1; run <= 2; run++ {
		if run == 2 { // a stale pair must be removed by the P5 rebuild
			if _, err := db.Pool.Exec(ctx, `INSERT INTO incident_countries SELECT id, 'FRA' FROM incidents WHERE source_id = 'stale'`); err != nil {
				t.Fatal(err)
			}
		}
		if err := db.Postprocess(ctx); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		got := map[string]row{}
		rows, err := db.Pool.Query(ctx, `
SELECT i.source || ':' || i.source_id, coalesce(i.primary_iso3, ''), coalesce(i.country_method, ''),
       coalesce((SELECT string_agg(iso3, ',' ORDER BY iso3) FROM incident_countries ic WHERE ic.incident_id = i.id), ''),
       coalesce((SELECT d.source || ':' || d.source_id FROM incidents d WHERE d.id = i.dup_of), ''),
       i.counted, i.deaths, i.props->>'damageAmountOrder'
  FROM incidents i`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var k string
			var r row
			if err := rows.Scan(&k, &r.iso, &r.method, &r.countries, &r.dupOf, &r.counted, &r.deaths, &r.damage); err != nil {
				t.Fatal(err)
			}
			got[k] = r
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}

		check := func(k, iso, method, countries string) {
			t.Helper()
			r := got[k]
			if r.iso != iso || r.method != method || r.countries != countries {
				t.Errorf("run %d: %s = %s/%s [%s], want %s/%s [%s]", run, k, r.iso, r.method, r.countries, iso, method, countries)
			}
		}
		check("t:dateline", "TST", "nearest", "TST") // 53 km across ±180°
		check("t:stale", "", "ocean", "")            // P4 clears the stale FRA
		check("ibtracs:near", "", "ocean", "JPN")    // the primary point is far out; only the track is 79 km off
		check("ibtracs:far", "", "ocean", "")        // 140 km: no country
		check("ibtracs:north", "", "ocean", "JPN")   // 91 km at 44.9°N (1.15° of longitude)
		check("ibtracs:cross-e", "", "ocean", "TST")
		check("ibtracs:cross-w", "", "ocean", "TSW")
		check("ibtracs:cross-far", "", "ocean", "")
		check("noaa_eq:distant", "JPN", "contains", "JPN") // 150 km from the USGS quake: not a duplicate

		for k, r := range got {
			if r.counted == notCounted[k] {
				t.Errorf("run %d: %s counted = %v, want %v", run, k, r.counted, !notCounted[k])
			}
		}
		if d := got["noaa_eq:dup"].dupOf; d != "usgs:main" {
			t.Errorf("run %d: noaa_eq:dup dup_of = %q, want usgs:main (closest in time, not the aftershock)", run, d)
		}
		for _, k := range []string{"noaa_eq:late", "noaa_eq:distant", "noaa_vo:v2", "usgs:after"} {
			if d := got[k].dupOf; d != "" {
				t.Errorf("run %d: %s dup_of = %q, want none", run, k, d)
			}
		}
		if d := got["noaa_vo:v1"].dupOf; d != "gvp:g1" {
			t.Errorf("run %d: noaa_vo:v1 dup_of = %q, want gvp:g1", run, d)
		}
		m := got["usgs:main"]
		if m.deaths == nil || *m.deaths != 18000 || m.damage == nil || *m.damage != "4" {
			t.Errorf("run %d: usgs:main deaths/damage = %v/%v, want 18000/4 copied from NOAA", run, m.deaths, m.damage)
		}
		if a := got["usgs:after"]; a.deaths != nil || a.damage != nil {
			t.Errorf("run %d: usgs:after got deaths/damage %v/%v", run, a.deaths, a.damage)
		}

		// P10: one row per counted (type, source), with the type's note.
		var cov string
		if err := db.Pool.QueryRow(ctx, `
SELECT string_agg(type || '/' || source || '/' || from_year || '-' || to_year || '/' || n || '/' || (note IS NOT NULL), ' ' ORDER BY type, source)
  FROM source_coverage`).Scan(&cov); err != nil {
			t.Fatal(err)
		}
		wantCov := "drought/gdacs/2022-2022/1/true earthquake/noaa_eq/2011-2011/2/true earthquake/t/2010-2010/2/true earthquake/usgs/2011-2011/2/true " +
			"flood/gdacs/2022-2022/1/true landslide/eonet/2018-2018/1/true other/eonet/2019-2019/1/true storm/ibtracs/2015-2016/6/true " +
			"volcano/gvp/2001-2001/1/true volcano/noaa_vo/2002-2002/1/true wildfire/eonet/2019-2019/1/true"
		if cov != wantCov {
			t.Errorf("run %d: source_coverage =\n%s\nwant\n%s", run, cov, wantCov)
		}

		// Idempotent aggregates; per-country sums never below the distinct totals.
		var counts string
		if err := db.Pool.QueryRow(ctx, `SELECT string_agg(type || '/' || iso3 || '/' || year || '=' || n, ' ' ORDER BY type, iso3, year) FROM mv_counts`).Scan(&counts); err != nil {
			t.Fatal(err)
		}
		if run == 1 {
			firstCounts = counts
		} else if counts != firstCounts {
			t.Errorf("mv_counts changed between runs:\n%s\n%s", firstCounts, counts)
		}
		var bad int
		if err := db.Pool.QueryRow(ctx, `
SELECT count(*) FROM mv_totals t
 WHERE t.n > (SELECT coalesce(sum(c.n), 0) FROM mv_counts c WHERE c.type = t.type AND c.year = t.year)`).Scan(&bad); err != nil {
			t.Fatal(err)
		}
		if bad != 0 {
			t.Errorf("run %d: %d (type, year) with per-country sum < distinct total", run, bad)
		}
	}
}

// TestAssignCellCountriesIntegration: cell centres inside a country, within
// 50 km of one (and across the antimeridian), and in the open ocean.
func TestAssignCellCountriesIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	db := newTestDB(t, ctx)
	resetIncidents(t, ctx, db)
	if _, err := db.Pool.Exec(ctx, `INSERT INTO countries (iso3, name, geom)
	   VALUES ('TST', 'Test Island', ST_Multi(ST_MakeEnvelope(179, -17, 180, -16, 4326)))`); err != nil {
		t.Fatal(err)
	}
	cell := func(lat, lon float64, iso string) domain.FireCell {
		return domain.FireCell{Product: "MODIS_SP", Year: 2020, Month: 1, Detections: 1,
			LatIdx: int32(math.Floor(lat / 0.25)), LonIdx: int32(math.Floor(lon / 0.25)), ISO3: iso}
	}
	cells := []domain.FireCell{
		cell(36.1, 139.1, ""),      // inside JPN
		cell(36.1, 142.3, ""),      // centre 142.375: 34 km east of JPN
		cell(36.1, 142.8, "XXX"),   // centre 142.875: 79 km, ocean; stale value replaced
		cell(-16.4, -179.9, ""),    // centre -179.875: 14 km east of TST across ±180°
		cell(0.1, -150.1, ""),      // open Pacific
		cell(36.1, 139.1, "stale"), // repeated cell
		cell(48.1, 2.3, ""),        // inside FRA
	}
	if err := db.AssignCellCountries(ctx, cells); err != nil {
		t.Fatal(err)
	}
	want := []string{"JPN", "JPN", "", "TST", "", "JPN", "FRA"}
	for i, c := range cells {
		if c.ISO3 != want[i] {
			t.Errorf("cell %d (%d,%d) ISO3 = %q, want %q", i, c.LatIdx, c.LonIdx, c.ISO3, want[i])
		}
	}
	if err := db.AssignCellCountries(ctx, nil); err != nil {
		t.Errorf("empty batch: %v", err)
	}
}

// boundariesPath is the full Natural Earth file (`make boundaries`); it is
// gitignored, so the test that needs it skips when it is missing.
const boundariesPath = "../../../data/boundaries/ne_50m_admin_0_countries.geojson"

// TestSeedPostprocessIntegration runs the full postprocess on the seed with
// the real countries: offshore quakes get their nearest country (checked
// against the plain exhaustive query), Idai spans its hinted countries plus
// any within 100 km of its track, Katrina's track reaches the USA, the P8
// rules hold, two runs give identical mv_counts, every per-country sum is at
// least the distinct total, and a run stays under 10 s.
func TestSeedPostprocessIntegration(t *testing.T) {
	if _, err := os.Stat(boundariesPath); err != nil {
		t.Skipf("full boundaries missing (%v): run `make boundaries`", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	db := newTestDB(t, ctx)
	resetIncidents(t, ctx, db)
	if _, err := db.Pool.Exec(ctx, "TRUNCATE countries CASCADE"); err != nil {
		t.Fatal(err)
	}
	if n, err := db.LoadCountries(ctx, boundariesPath); err != nil || n < 200 {
		t.Fatalf("LoadCountries = %d, %v", n, err)
	}
	seed, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, string(seed)); err != nil {
		t.Fatalf("load seed: %v", err)
	}

	var firstCounts string
	for run := 1; run <= 2; run++ {
		start := time.Now()
		if err := db.Postprocess(ctx); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		if d := time.Since(start); d > 10*time.Second {
			t.Errorf("run %d took %s, want < 10 s on the seed", run, d)
		}
		var counts string
		if err := db.Pool.QueryRow(ctx, `SELECT string_agg(type || '/' || iso3 || '/' || year || '=' || n, ' ' ORDER BY type, iso3, year) FROM mv_counts`).Scan(&counts); err != nil {
			t.Fatal(err)
		}
		if run == 1 {
			firstCounts = counts
		} else if counts != firstCounts {
			t.Error("mv_counts differ between two runs")
		}
	}

	countries := func(source, id string) string {
		t.Helper()
		var s string
		err := db.Pool.QueryRow(ctx, `
SELECT coalesce(string_agg(ic.iso3, ',' ORDER BY ic.iso3), '')
  FROM incidents i LEFT JOIN incident_countries ic ON ic.incident_id = i.id
 WHERE i.source = $1 AND i.source_id = $2`, source, id).Scan(&s)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	// Idai: MOZ, MWI, ZWE (hints and track) and Madagascar, whose west coast
	// the track loop passes at 86 km.
	if got := countries("ibtracs", "2019063S18038"); got != "MDG,MOZ,MWI,ZWE" {
		t.Errorf("Idai countries = %s, want MDG,MOZ,MWI,ZWE", got)
	}
	if got := countries("ibtracs", "2005236N23285"); !strings.Contains(got, "USA") {
		t.Errorf("Katrina countries = %s, want USA among them", got)
	}
	if got := countries("usgs", "official20110311054624120_30"); got != "JPN" {
		t.Errorf("Tohoku (offshore) countries = %s, want JPN", got)
	}

	// Offshore quakes: the box/piece P2 must agree with the exhaustive query.
	var checked, mismatched int
	err = db.Pool.QueryRow(ctx, `
SELECT count(*), count(*) FILTER (WHERE x.iso3 IS DISTINCT FROM i.primary_iso3)
  FROM incidents i
  CROSS JOIN LATERAL (SELECT (SELECT c.iso3 FROM countries c
                               WHERE ST_DWithin(c.geom::geography, i.geom::geography, 200000)
                               ORDER BY ST_Distance(c.geom::geography, i.geom::geography), c.iso3 LIMIT 1) AS iso3) x
 WHERE i.country_method IN ('nearest', 'ocean')`).Scan(&checked, &mismatched)
	if err != nil {
		t.Fatal(err)
	}
	var nearest int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM incidents WHERE country_method = 'nearest' AND type = 'earthquake'`).Scan(&nearest); err != nil {
		t.Fatal(err)
	}
	if mismatched != 0 || checked < 20 || nearest == 0 {
		t.Errorf("P2 vs exhaustive: %d of %d differ (%d nearest quakes)", mismatched, checked, nearest)
	}

	// P8 on the seed, and the M5 verify query: Σ per-country ≥ distinct, per type.
	var wrongFlags, badSums int
	err = db.Pool.QueryRow(ctx, `
SELECT (SELECT count(*) FROM incidents WHERE counted = (`+p8Excluded+`)),
       (SELECT count(*) FROM (SELECT type, sum(n) AS n FROM mv_totals GROUP BY 1) t
         WHERE t.n > (SELECT coalesce(sum(c.n), 0) FROM mv_counts c WHERE c.type = t.type))`).Scan(&wrongFlags, &badSums)
	if err != nil {
		t.Fatal(err)
	}
	if wrongFlags != 0 || badSums != 0 {
		t.Errorf("%d rows with a wrong counted flag, %d types with per-country sum < distinct total", wrongFlags, badSums)
	}
	var eonetStorm, gdacsWF bool
	if err := db.Pool.QueryRow(ctx, `SELECT bool_or(counted) FILTER (WHERE source = 'eonet' AND type = 'storm'),
	                                        bool_or(counted) FILTER (WHERE source = 'gdacs' AND type = 'wildfire') FROM incidents`).Scan(&eonetStorm, &gdacsWF); err != nil {
		t.Fatal(err)
	}
	if eonetStorm || gdacsWF {
		t.Errorf("EONET storms / GDACS wildfires must not be counted (%v, %v)", eonetStorm, gdacsWF)
	}
}

// BenchmarkPostprocessP2Integration measures P2 (nearest country within
// 200 km) at scale on the real countries: 12k uniform random points (lat
// ±75°) plus 12k points within ±2° of a random country vertex, under a fixed
// setseed. P0 and P1 run untimed, so the timed part is P2 alone over the
// points left unassigned (offshore). Reports unassigned rows/s. The synthetic
// rows and countries are truncated afterwards.
//
//	TEST_DATABASE_URL=… go test ./internal/store/postgres/ -run '^$' -bench P2 -benchtime 1x -p 1
func BenchmarkPostprocessP2Integration(b *testing.B) {
	if _, err := os.Stat(boundariesPath); err != nil {
		b.Skipf("full boundaries missing (%v): run `make boundaries`", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	db := newTestDB(b, ctx)
	resetIncidents(b, ctx, db)
	if _, err := db.Pool.Exec(ctx, "TRUNCATE countries CASCADE"); err != nil {
		b.Fatal(err)
	}
	if _, err := db.LoadCountries(ctx, boundariesPath); err != nil {
		b.Fatal(err)
	}
	conn, err := db.Pool.Acquire(ctx)
	if err != nil {
		b.Fatal(err)
	}
	defer conn.Release()
	for _, q := range []string{
		`SELECT setseed(0.42)`,
		`CREATE TEMP TABLE bench_vertices AS
  SELECT row_number() OVER () AS rn, d.geom FROM countries c, ST_DumpPoints(c.geom) AS d`,
		`INSERT INTO incidents (source, source_id, type, title, start_time, year, geom, raw_hash, search_text)
  SELECT 'bench', 'u' || g, 'earthquake', 'bench', '2000-01-01', 2000,
         ST_SetSRID(ST_MakePoint(random() * 360 - 180, random() * 150 - 75), 4326), 'h', 'bench'
    FROM generate_series(1, 12000) AS g`,
		`INSERT INTO incidents (source, source_id, type, title, start_time, year, geom, raw_hash, search_text)
  SELECT 'bench', 'c' || r.g, 'earthquake', 'bench', '2000-01-01', 2000,
         ST_SetSRID(ST_MakePoint(greatest(-180, least(180, ST_X(v.geom) + r.dx)),
                                 greatest(-89, least(89, ST_Y(v.geom) + r.dy))), 4326), 'h', 'bench'
    FROM (SELECT g, 1 + floor(random() * (SELECT count(*) FROM bench_vertices))::bigint AS rn,
                 random() * 4 - 2 AS dx, random() * 4 - 2 AS dy
            FROM generate_series(1, 12000) AS g) r
    JOIN bench_vertices v USING (rn)`,
		`DROP TABLE bench_vertices`,
	} {
		if _, err := conn.Exec(ctx, q); err != nil {
			b.Fatalf("generate: %v", err)
		}
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), `DROP TABLE IF EXISTS pg_temp.`+countryParts)
	}()

	steps := map[string]postprocessStep{}
	for _, s := range postprocessSteps() {
		steps[s.name] = s
	}
	var unassigned, assigned int64
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		for _, q := range []string{
			`UPDATE incidents SET primary_iso3 = NULL, country_method = NULL WHERE country_method IS NOT NULL`,
		} {
			if _, err := conn.Exec(ctx, q); err != nil {
				b.Fatal(err)
			}
		}
		for _, name := range []string{"P0", "P1"} {
			if _, err := db.runStep(ctx, conn.Conn(), steps[name]); err != nil {
				b.Fatalf("%s: %v", name, err)
			}
		}
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM incidents WHERE country_method IS NULL`).Scan(&unassigned); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		n, err := db.runStep(ctx, conn.Conn(), steps["P2"])
		if err != nil {
			b.Fatalf("P2: %v", err)
		}
		assigned = n
	}
	b.StopTimer()
	perOp := b.Elapsed().Seconds() / float64(b.N)
	b.ReportMetric(float64(unassigned)/perOp, "rows/s")
	b.ReportMetric(float64(unassigned), "unassigned_rows")
	b.ReportMetric(float64(assigned), "nearest_rows")
	b.ReportMetric(float64(unassigned)*300000/24000/(float64(unassigned)/perOp), "s_per_300k_extrapolated")
}
