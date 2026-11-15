package postgres

import (
	"context"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fixturePath = "testdata/countries_fixture.geojson"

// newTestDB connects to TEST_DATABASE_URL and refuses any database whose name
// does not end in _test, so a stray URL can never truncate the dev database.
func newTestDB(t testing.TB, ctx context.Context) *DB {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set (run `make test-db` then `make test-int`)")
	}
	db, err := New(ctx, url, 2, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	var name string
	if err := db.Pool.QueryRow(ctx, "SELECT current_database()").Scan(&name); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(name, "_test") {
		t.Fatalf("refusing to run against database %q: integration tests must use the separate dwts_test database", name)
	}
	return db
}

// TestLoadCountriesIntegration loads a three-country fixture (FRA with ISO_A2
// "-99", a plain Polygon, and an invalid bow-tie polygon) into dwts_test.
func TestLoadCountriesIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := newTestDB(t, ctx)

	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	count := func() (n int) {
		t.Helper()
		if err := db.Pool.QueryRow(ctx, "SELECT count(*) FROM countries").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	reset := func() { exec("TRUNCATE countries CASCADE") } // dwts_test only: see newTestDB
	reset()
	t.Cleanup(func() {
		if _, err := db.Pool.Exec(context.Background(), "TRUNCATE countries CASCADE"); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
	writeGeoJSON := func(name, body string) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	t.Run("first load truncates, converts and repairs", func(t *testing.T) {
		exec("INSERT INTO countries (iso3, name, geom) VALUES ('ZZZ', 'Stale', 'MULTIPOLYGON(((0 0,1 0,1 1,0 0)))')")
		n, err := db.LoadCountries(ctx, fixturePath)
		if err != nil {
			t.Fatal(err)
		}
		if n != 3 || count() != 3 {
			t.Fatalf("LoadCountries = %d, table has %d rows, want 3 and 3 (stale ZZZ must be truncated away)", n, count())
		}

		var iso2, continent *string
		var name, gtype string
		var srid int
		var lon, lat float64
		err = db.Pool.QueryRow(ctx, `SELECT name, iso2, continent, GeometryType(geom), ST_SRID(geom), ST_X(label_pt), ST_Y(label_pt)
		                              FROM countries WHERE iso3 = 'FRA'`).Scan(&name, &iso2, &continent, &gtype, &srid, &lon, &lat)
		if err != nil {
			t.Fatalf("FRA must be keyed by ADM0_A3: %v", err)
		}
		if name != "France" || continent == nil || *continent != "Europe" {
			t.Errorf("FRA = %q / %v", name, continent)
		}
		if iso2 == nil || *iso2 != "FR" {
			t.Errorf("FRA iso2 = %v, want FR from ISO_A2_EH (ISO_A2 is -99)", iso2)
		}
		if gtype != "MULTIPOLYGON" || srid != 4326 {
			t.Errorf("FRA geometry = %s srid %d, want MULTIPOLYGON 4326", gtype, srid)
		}
		if math.Abs(lon-2.552275) > 1e-9 || math.Abs(lat-46.696113) > 1e-9 {
			t.Errorf("FRA label_pt = (%v, %v)", lon, lat)
		}

		var jp *string
		if err := db.Pool.QueryRow(ctx, "SELECT iso2, GeometryType(geom) FROM countries WHERE iso3 = 'JPN'").Scan(&jp, &gtype); err != nil {
			t.Fatal(err)
		}
		if jp == nil || *jp != "JP" || gtype != "MULTIPOLYGON" {
			t.Errorf("JPN iso2 = %v geometry = %s, want JP and a Polygon promoted to MULTIPOLYGON", jp, gtype)
		}

		var area float64
		var labelNull bool
		err = db.Pool.QueryRow(ctx, "SELECT GeometryType(geom), ST_Area(geom), label_pt IS NULL FROM countries WHERE iso3 = 'BOW'").Scan(&gtype, &area, &labelNull)
		if err != nil {
			t.Fatal(err)
		}
		if gtype != "MULTIPOLYGON" || math.Abs(area-2) > 1e-9 {
			t.Errorf("repaired BOW = %s area %v, want MULTIPOLYGON of two unit triangles (area 2)", gtype, area)
		}
		if !labelNull {
			t.Error("BOW has null LABEL_X/LABEL_Y, want label_pt NULL")
		}
		var bowISO2 *string
		if err := db.Pool.QueryRow(ctx, "SELECT iso2 FROM countries WHERE iso3 = 'BOW'").Scan(&bowISO2); err != nil {
			t.Fatal(err)
		}
		if bowISO2 != nil {
			t.Errorf("BOW iso2 = %q, want NULL (ISO_A2 and ISO_A2_EH are both -99)", *bowISO2)
		}

		var invalid int
		if err := db.Pool.QueryRow(ctx, "SELECT count(*) FROM countries WHERE NOT ST_IsValid(geom)").Scan(&invalid); err != nil {
			t.Fatal(err)
		}
		if invalid != 0 {
			t.Errorf("%d invalid geometries after load, want 0", invalid)
		}
	})

	t.Run("reload is idempotent and refreshes rows", func(t *testing.T) {
		exec("UPDATE countries SET name = 'Stale name', iso2 = 'XX', continent = NULL WHERE iso3 = 'FRA'")
		n, err := db.LoadCountries(ctx, fixturePath)
		if err != nil || n != 3 || count() != 3 {
			t.Fatalf("reload = %d, %v, table has %d rows, want 3, nil, 3", n, err, count())
		}
		var name string
		var iso2, continent *string
		if err := db.Pool.QueryRow(ctx, "SELECT name, iso2, continent FROM countries WHERE iso3 = 'FRA'").Scan(&name, &iso2, &continent); err != nil {
			t.Fatal(err)
		}
		if name != "France" || iso2 == nil || *iso2 != "FR" || continent == nil || *continent != "Europe" {
			t.Errorf("FRA after reload = %q %v %v, want it restored from the file", name, iso2, continent)
		}
	})

	t.Run("ISO2 map", func(t *testing.T) {
		m, err := db.CountryISO2Map(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(m) != 2 || m["FR"] != "FRA" || m["JP"] != "JPN" {
			t.Errorf("CountryISO2Map = %v, want FR→FRA and JP→JPN only", m)
		}
		// Natural Earth gives territories their sovereign's ISO_A2_EH (IOA
		// and ATC are AU): the largest country keeps the code.
		exec("INSERT INTO countries (iso3, iso2, name, geom) VALUES ('ZZT', 'FR', 'Tiny territory', 'MULTIPOLYGON(((0 0,0.1 0,0.1 0.1,0 0)))')")
		defer exec("DELETE FROM countries WHERE iso3 = 'ZZT'")
		if m, err := db.CountryISO2Map(ctx); err != nil || m["FR"] != "FRA" {
			t.Errorf("with a tiny FR territory: FR → %q, %v, want FRA", m["FR"], err)
		}
	})

	t.Run("name map", func(t *testing.T) {
		m, err := db.CountryNameMap(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(m) != 3 || m["France"] != "FRA" || m["Japan"] != "JPN" || m["Bowtieland"] != "BOW" {
			t.Errorf("CountryNameMap = %v, want France→FRA, Japan→JPN, Bowtieland→BOW", m)
		}
	})

	t.Run("upserts instead of truncating once incidents exist", func(t *testing.T) {
		exec("INSERT INTO countries (iso3, name, geom) VALUES ('ZZZ', 'Stale', 'MULTIPOLYGON(((0 0,1 0,1 1,0 0)))')")
		exec(`INSERT INTO incidents (source, source_id, type, title, start_time, year, geom, raw_hash, search_text, primary_iso3)
		      VALUES ('test', '1', 'earthquake', 'Test quake', '2020-01-01', 2020, 'SRID=4326;POINT(2 46)', 'h', 'test quake', 'FRA')`)
		n, err := db.LoadCountries(ctx, fixturePath)
		if err != nil || n != 3 {
			t.Fatalf("reload = %d, %v", n, err)
		}
		if got := count(); got != 4 {
			t.Errorf("table has %d rows, want 4: ZZZ must survive because incidents reference countries", got)
		}
		var inc int
		if err := db.Pool.QueryRow(ctx, "SELECT count(*) FROM incidents WHERE primary_iso3 = 'FRA'").Scan(&inc); err != nil {
			t.Fatal(err)
		}
		if inc != 1 {
			t.Errorf("incident count = %d, want 1: the reload must not cascade into incidents", inc)
		}
		exec("TRUNCATE countries CASCADE")
		if _, err := db.LoadCountries(ctx, fixturePath); err != nil {
			t.Fatal(err)
		}
	})

	failures := []struct{ name, body, want string }{
		{"no features", `{"type":"FeatureCollection","features":[]}`, "no features"},
		{"missing ADM0_A3", `{"features":[{"properties":{"NAME":"Nowhere"},"geometry":{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,0]]]}}]}`, "missing ADM0_A3"},
		{"missing geometry", `{"features":[{"properties":{"ADM0_A3":"AAA","NAME":"A"},"geometry":null}]}`, "missing geometry"},
		{"no polygon left", `{"features":[{"properties":{"ADM0_A3":"LIN","NAME":"Line"},"geometry":{"type":"LineString","coordinates":[[0,0],[1,1]]}}]}`, "no polygon left after repair for LIN"},
		{"malformed geometry", `{"features":[{"properties":{"ADM0_A3":"BAD","NAME":"Bad"},"geometry":{"type":"Polygon","coordinates":"nope"}}]}`, "BAD"},
	}
	for _, tc := range failures {
		t.Run("rolls back on "+tc.name, func(t *testing.T) {
			// The valid first feature and the truncate must be undone with the failure.
			body := strings.Replace(tc.body, `"features":[`, `"features":[{"properties":{"ADM0_A3":"OKK","NAME":"Fine"},"geometry":{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,0]]]}},`, 1)
			if tc.name == "no features" {
				body = tc.body
			}
			before := count()
			n, err := db.LoadCountries(ctx, writeGeoJSON("bad.geojson", body))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("LoadCountries = %d, %v, want an error containing %q", n, err, tc.want)
			}
			if got := count(); got != before {
				t.Errorf("table has %d rows after a failed load, want the %d from before", got, before)
			}
			var fra int
			if err := db.Pool.QueryRow(ctx, "SELECT count(*) FROM countries WHERE iso3 = 'FRA'").Scan(&fra); err != nil || fra != 1 {
				t.Errorf("FRA rows = %d, %v: the failed load must roll back its truncate", fra, err)
			}
		})
	}

	t.Run("missing file", func(t *testing.T) {
		if _, err := db.LoadCountries(ctx, filepath.Join(t.TempDir(), "nope.geojson")); err == nil {
			t.Error("want an error for a missing file")
		}
	})
}
