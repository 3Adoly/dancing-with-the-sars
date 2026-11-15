package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// upsertCountrySQL is the Guide M2 statement. The key is Natural Earth
// ADM0_A3, never ISO_A3 (which is -99 for FRA and NOR). Geometries are
// repaired with ST_MakeValid, reduced to their polygonal parts with
// ST_CollectionExtract(…,3) and normalised to MultiPolygon with ST_Multi.
// iso2 falls back to ISO_A2_EH ($8) when ISO_A2 is -99, so FRA, NOR and KOS
// get FR, NO and XK (controller ruling, Task 3/12).
const upsertCountrySQL = `
INSERT INTO countries (iso3, iso2, name, continent, geom, label_pt)
VALUES ($1, COALESCE(NULLIF($2,'-99'), NULLIF($8,'-99')), $3, $4,
        ST_Multi(ST_CollectionExtract(ST_MakeValid(ST_SetSRID(ST_GeomFromGeoJSON($5),4326)),3)),
        ST_SetSRID(ST_MakePoint($6,$7),4326))
ON CONFLICT (iso3) DO UPDATE SET name=EXCLUDED.name, iso2=EXCLUDED.iso2, continent=EXCLUDED.continent,
  geom=EXCLUDED.geom, label_pt=EXCLUDED.label_pt`

// hasDependentRowsSQL is true when TRUNCATE countries CASCADE would destroy
// data: incidents, incident_countries and fire_cells all reference countries.
const hasDependentRowsSQL = `
SELECT EXISTS (SELECT 1 FROM incidents)
    OR EXISTS (SELECT 1 FROM incident_countries)
    OR EXISTS (SELECT 1 FROM fire_cells)`

// countryFeature is the part of a Natural Earth admin-0 feature that is loaded.
// encoding/json matches the property names case-insensitively, so this reads
// both the uppercase 50m files and the older lowercase ones.
type countryFeature struct {
	Properties struct {
		ADM0A3    string   `json:"ADM0_A3"`
		ISOA2     string   `json:"ISO_A2"`
		ISOA2EH   string   `json:"ISO_A2_EH"`
		Name      string   `json:"NAME"`
		Continent string   `json:"CONTINENT"`
		LabelX    *float64 `json:"LABEL_X"`
		LabelY    *float64 `json:"LABEL_Y"`
	} `json:"properties"`
	Geometry json.RawMessage `json:"geometry"`
}

// LoadCountries streams the Natural Earth admin-0 FeatureCollection at
// geojsonPath into the countries table in one transaction and returns the
// number of features loaded.
//
// On a first load (no incidents, incident_countries or fire_cells yet) the
// table is truncated first so countries missing from the file do not linger.
// Once data references countries it only upserts, because TRUNCATE … CASCADE
// would wipe that data. Any error, including an empty file or a geometry that
// is empty after repair, rolls the whole load back.
func (d *DB) LoadCountries(ctx context.Context, geojsonPath string) (int, error) {
	f, err := os.Open(geojsonPath)
	if err != nil {
		return 0, fmt.Errorf("load countries: %w", err)
	}
	defer f.Close()

	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("load countries: begin: %w", err)
	}
	defer tx.Rollback(ctx) // no-op once committed

	var hasData bool
	if err := tx.QueryRow(ctx, hasDependentRowsSQL).Scan(&hasData); err != nil {
		return 0, fmt.Errorf("load countries: check dependent rows: %w", err)
	}
	if !hasData {
		if _, err := tx.Exec(ctx, "TRUNCATE countries CASCADE"); err != nil {
			return 0, fmt.Errorf("load countries: truncate: %w", err)
		}
	}

	seen := map[string]bool{}
	n := 0
	err = decodeFeatures(f, func(i int, ft countryFeature) error {
		p := ft.Properties
		iso3 := strings.TrimSpace(p.ADM0A3)
		switch {
		case iso3 == "" || iso3 == "-99":
			return fmt.Errorf("feature %d (%q): missing ADM0_A3", i, p.Name)
		case len(ft.Geometry) == 0 || string(ft.Geometry) == "null":
			return fmt.Errorf("feature %d (%s): missing geometry", i, iso3)
		}
		if seen[iso3] {
			d.Log.WarnContext(ctx, "duplicate ADM0_A3 in boundaries file, last one wins", "iso3", iso3)
		}
		seen[iso3] = true
		// NULL when absent; "-99" is turned into NULL by the SQL.
		iso2, iso2EH := nullText(strings.TrimSpace(p.ISOA2)), nullText(strings.TrimSpace(p.ISOA2EH))
		if _, err := tx.Exec(ctx, upsertCountrySQL,
			iso3, iso2, p.Name, p.Continent, string(ft.Geometry), p.LabelX, p.LabelY, iso2EH,
		); err != nil {
			return fmt.Errorf("feature %d (%s): %w", i, iso3, err)
		}
		n++
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("load countries: %w", err)
	}
	if n == 0 {
		return 0, errors.New("load countries: the file has no features")
	}

	// ST_CollectionExtract returns an empty geometry when nothing polygonal
	// survives the repair; such a country would silently match nothing.
	rows, err := tx.Query(ctx, "SELECT iso3 FROM countries WHERE ST_IsEmpty(geom) ORDER BY iso3")
	if err != nil {
		return 0, fmt.Errorf("load countries: check empty geometries: %w", err)
	}
	var empty []string
	for rows.Next() {
		var iso3 string
		if err := rows.Scan(&iso3); err != nil {
			rows.Close()
			return 0, fmt.Errorf("load countries: check empty geometries: %w", err)
		}
		empty = append(empty, iso3)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("load countries: check empty geometries: %w", err)
	}
	if len(empty) > 0 {
		return 0, fmt.Errorf("load countries: no polygon left after repair for %s", strings.Join(empty, ", "))
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("load countries: commit: %w", err)
	}
	d.Log.InfoContext(ctx, "countries loaded", "count", n, "truncated_first", !hasData)
	return n, nil
}

// decodeFeatures streams a GeoJSON FeatureCollection from r, calling fn with
// each feature and its zero-based index while only one feature is in memory.
// Other top-level members (type, name, crs, bbox) are skipped wherever they
// sit relative to "features".
func decodeFeatures(r io.Reader, fn func(i int, f countryFeature) error) error {
	dec := json.NewDecoder(r)
	if err := expectDelim(dec, '{'); err != nil {
		return fmt.Errorf("not a GeoJSON object: %w", err)
	}
	sawFeatures := false
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("read GeoJSON: %w", err)
		}
		key, ok := tok.(string)
		if !ok {
			return fmt.Errorf("read GeoJSON: unexpected token %v", tok)
		}
		if key != "features" {
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return fmt.Errorf("read GeoJSON member %q: %w", key, err)
			}
			continue
		}
		sawFeatures = true
		if err := expectDelim(dec, '['); err != nil {
			return fmt.Errorf(`"features" is not an array: %w`, err)
		}
		for i := 0; dec.More(); i++ {
			var ft countryFeature
			if err := dec.Decode(&ft); err != nil {
				return fmt.Errorf("decode feature %d: %w", i, err)
			}
			if err := fn(i, ft); err != nil {
				return err
			}
		}
		if err := expectDelim(dec, ']'); err != nil {
			return fmt.Errorf("read GeoJSON: %w", err)
		}
	}
	if err := expectDelim(dec, '}'); err != nil {
		return fmt.Errorf("read GeoJSON: %w", err)
	}
	if !sawFeatures {
		return errors.New(`GeoJSON has no "features" member`)
	}
	return nil
}

// expectDelim consumes the next token and requires it to be the delimiter want.
func expectDelim(dec *json.Decoder, want json.Delim) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != want {
		return fmt.Errorf("expected %q, found %v", rune(want), tok)
	}
	return nil
}

// countryISO2SQL maps each iso2 to one iso3. Territories share their
// sovereign's ISO_A2_EH (Natural Earth gives IOA and ATC "AU"), so for a
// shared code the largest country wins, ties broken by iso3.
const countryISO2SQL = `
SELECT DISTINCT ON (iso2) iso2, iso3
  FROM countries
 WHERE iso2 IS NOT NULL
 ORDER BY iso2, ST_Area(geom) DESC, iso3`

// CountryISO2Map returns countries.iso2 → iso3 (ADM0_A3), for adapters
// whose source gives ISO 3166-1 alpha-2 codes (glc). It is empty when the
// countries table is.
func (d *DB) CountryISO2Map(ctx context.Context) (map[string]string, error) {
	rows, err := d.Pool.Query(ctx, countryISO2SQL)
	if err != nil {
		return nil, fmt.Errorf("country iso2 map: %w", err)
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var iso2, iso3 string
		if err := rows.Scan(&iso2, &iso3); err != nil {
			return nil, fmt.Errorf("country iso2 map: %w", err)
		}
		m[iso2] = iso3
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("country iso2 map: %w", err)
	}
	return m, nil
}

// countryNameSQL lists every country by its Natural Earth NAME.
const countryNameSQL = `SELECT name, iso3 FROM countries WHERE name IS NOT NULL AND name <> '' ORDER BY iso3`

// CountryNameMap returns countries.name → iso3 (ADM0_A3), for adapters whose
// source names countries in words (dfo). Names are returned as stored; the
// adapter folds case and accents. It is empty when the countries table is.
func (d *DB) CountryNameMap(ctx context.Context) (map[string]string, error) {
	rows, err := d.Pool.Query(ctx, countryNameSQL)
	if err != nil {
		return nil, fmt.Errorf("country name map: %w", err)
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var name, iso3 string
		if err := rows.Scan(&name, &iso3); err != nil {
			return nil, fmt.Errorf("country name map: %w", err)
		}
		m[name] = iso3
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("country name map: %w", err)
	}
	return m, nil
}
