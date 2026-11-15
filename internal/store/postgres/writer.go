package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/dwts/backend/internal/domain"
	"github.com/dwts/backend/internal/store"
)

// *DB is the ingest-side store.Writer.
var _ store.Writer = (*DB)(nil)

// maxRunError bounds the error text kept in ingest_runs.error.
const maxRunError = 2000

// StartRun records the start of an ingest run for source and returns its id.
func (d *DB) StartRun(ctx context.Context, source string) (int64, error) {
	var id int64
	err := d.Pool.QueryRow(ctx, `INSERT INTO ingest_runs (source) VALUES ($1) RETURNING id`, source).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("start run for %s: %w", source, err)
	}
	return id, nil
}

// FinishRun closes the run: status ok when runErr is nil, failed otherwise
// (with the error text, truncated), plus the number of rows written.
func (d *DB) FinishRun(ctx context.Context, runID int64, rows int, runErr error) error {
	status, errText := "ok", (*string)(nil)
	if runErr != nil {
		status = "failed"
		t := truncateUTF8(storableText(runErr.Error()), maxRunError)
		errText = &t
	}
	tag, err := d.Pool.Exec(ctx,
		`UPDATE ingest_runs SET finished_at = now(), status = $2, rows = $3, error = $4 WHERE id = $1`,
		runID, status, rows, errText)
	if err != nil {
		return fmt.Errorf("finish run %d: %w", runID, err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("finish run %d: no such run", runID)
	}
	return nil
}

// storableText makes s storable in a Postgres text column: invalid UTF-8
// becomes U+FFFD and NUL bytes (an upstream body quoted in an error) are
// removed.
func storableText(s string) string {
	s = strings.ToValidUTF8(s, "\uFFFD")
	return strings.ReplaceAll(s, "\x00", "")
}

// truncateUTF8 cuts s to at most max bytes without splitting a rune.
func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[:max]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// stageColumns are the columns of the temp table, in COPY order (Guide M4).
var stageColumns = []string{
	"source", "source_id", "type", "title", "name", "description", "start_time", "end_time", "time_precision",
	"year", "lon", "lat", "track_wkt", "magnitude", "magnitude_unit", "severity", "deaths", "country_hints",
	"url", "props", "raw", "raw_hash", "search_text",
}

const createStage = `
CREATE TEMP TABLE stage (
  source text, source_id text, type text, title text, name text, description text,
  start_time timestamptz, end_time timestamptz, time_precision text, year smallint,
  lon float8, lat float8, track_wkt text, magnitude real, magnitude_unit text, severity smallint,
  deaths int, country_hints text[], url text, props jsonb, raw jsonb, raw_hash text, search_text text
) ON COMMIT DROP`

// upsertSQL is Plan_Detailed_Level.md §5 with the geometry built from the
// staged lon/lat/track_wkt. The WHERE clause makes re-ingesting an unchanged
// record a no-op (it is neither updated nor counted in RowsAffected).
//
// When the point moves, country_method is reset to NULL so Postprocess P1-P4
// assigns the country again; primary_iso3 is reset with it, otherwise a point
// that moved into the ocean would keep its old country (P4 only sets the
// method).
const upsertSQL = `
INSERT INTO incidents (source, source_id, type, title, name, description, start_time, end_time, time_precision, year,
                       geom, track, magnitude, magnitude_unit, severity, deaths, country_hints, url, props, raw, raw_hash, search_text)
SELECT source, source_id, type, title, name, description, start_time, end_time, time_precision, year,
       ST_SetSRID(ST_MakePoint(lon, lat), 4326),
       CASE WHEN track_wkt IS NULL THEN NULL ELSE ST_GeomFromText(track_wkt, 4326) END,
       magnitude, magnitude_unit, severity, deaths, country_hints, url, props, raw, raw_hash, search_text
  FROM stage
ON CONFLICT (source, source_id) DO UPDATE SET
  type=EXCLUDED.type, title=EXCLUDED.title, name=EXCLUDED.name, description=EXCLUDED.description,
  start_time=EXCLUDED.start_time, end_time=EXCLUDED.end_time, time_precision=EXCLUDED.time_precision, year=EXCLUDED.year,
  geom=EXCLUDED.geom, track=EXCLUDED.track, magnitude=EXCLUDED.magnitude, magnitude_unit=EXCLUDED.magnitude_unit,
  severity=EXCLUDED.severity, deaths=EXCLUDED.deaths, country_hints=EXCLUDED.country_hints, url=EXCLUDED.url,
  props=EXCLUDED.props, raw=EXCLUDED.raw, raw_hash=EXCLUDED.raw_hash, search_text=EXCLUDED.search_text,
  country_method = CASE WHEN incidents.geom IS DISTINCT FROM EXCLUDED.geom THEN NULL ELSE incidents.country_method END,
  primary_iso3   = CASE WHEN incidents.geom IS DISTINCT FROM EXCLUDED.geom THEN NULL ELSE incidents.primary_iso3 END,
  ingested_at = now()
WHERE incidents.raw_hash <> EXCLUDED.raw_hash`

// UpsertIncidents writes batch in one transaction: COPY into a temp table,
// then INSERT ... ON CONFLICT (source, source_id) DO UPDATE ... WHERE the
// raw_hash differs. It returns the rows inserted or changed, so replaying an
// unchanged batch returns 0. Duplicate (source, source_id) pairs inside the
// batch (which ON CONFLICT cannot take in one statement) are collapsed first,
// keeping the last one. Every incident must be finalized (ingest.Finalize):
// RawHash and SearchText are required. incidents.raw is always written NULL
// (ruling R-B.2); the hash alone detects changes.
func (d *DB) UpsertIncidents(ctx context.Context, batch []domain.Incident) (int, error) {
	batch = dedupeLast(batch)
	if len(batch) == 0 {
		return 0, nil
	}
	rows := make([][]any, len(batch))
	for i := range batch {
		row, err := stageRow(&batch[i])
		if err != nil {
			return 0, fmt.Errorf("incident %s/%s: %w", batch[i].Source, batch[i].SourceID, err)
		}
		rows[i] = row
	}

	var affected int64
	err := pgx.BeginFunc(ctx, d.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, createStage); err != nil {
			return fmt.Errorf("create stage: %w", err)
		}
		copied, err := tx.CopyFrom(ctx, pgx.Identifier{"stage"}, stageColumns, pgx.CopyFromRows(rows))
		if err != nil {
			return fmt.Errorf("copy to stage: %w", err)
		}
		if copied != int64(len(rows)) {
			return fmt.Errorf("copy to stage: copied %d of %d rows", copied, len(rows))
		}
		tag, err := tx.Exec(ctx, upsertSQL)
		if err != nil {
			return fmt.Errorf("upsert: %w", err)
		}
		affected = tag.RowsAffected()
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("upsert %d incidents: %w", len(batch), err)
	}
	return int(affected), nil
}

// dedupeLast returns a copy of batch without repeated (source, source_id)
// pairs. The first occurrence keeps its position and the last occurrence
// supplies its content.
func dedupeLast(batch []domain.Incident) []domain.Incident {
	type key struct{ source, id string }
	pos := make(map[key]int, len(batch))
	out := make([]domain.Incident, 0, len(batch))
	for i := range batch {
		k := key{batch[i].Source, batch[i].SourceID}
		if p, dup := pos[k]; dup {
			out[p] = batch[i]
			continue
		}
		pos[k] = len(out)
		out = append(out, batch[i])
	}
	return out
}

// stageRow converts one incident to the stage columns (see stageColumns).
func stageRow(in *domain.Incident) ([]any, error) {
	if in.RawHash == "" || in.SearchText == "" {
		return nil, errors.New("RawHash/SearchText empty: run ingest.Finalize before writing")
	}
	props := []byte("{}")
	if len(in.Props) > 0 {
		b, err := json.Marshal(in.Props)
		if err != nil {
			return nil, fmt.Errorf("marshal props: %w", err)
		}
		props = b
	}
	var hints any
	if len(in.CountryHints) > 0 {
		hints = in.CountryHints
	}
	var endTime, magnitude, deaths any
	if in.EndTime != nil {
		endTime = in.EndTime.UTC()
	}
	if in.Magnitude != nil {
		magnitude = *in.Magnitude
	}
	if in.Deaths != nil {
		deaths = *in.Deaths
	}
	return []any{
		in.Source, in.SourceID, string(in.Type), in.Title, nullText(in.Name), nullText(in.Description),
		in.StartTime.UTC(), endTime, string(in.TimePrecision), in.Year,
		in.Lon, in.Lat, nullText(in.TrackWKT), magnitude, nullText(in.MagnitudeUnit), int16(in.Severity), deaths, hints,
		nullText(in.URL), props, nil /* raw: never stored (R-B.2) */, in.RawHash, in.SearchText,
	}, nil
}

// nullText maps the empty string to SQL NULL.
func nullText(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

const upsertFireSQL = `
INSERT INTO fire_cells (product, year, month, lat_idx, lon_idx, iso3, detections, frp_sum)
SELECT product, year, month, lat_idx, lon_idx, iso3, detections, frp_sum FROM fire_stage
ON CONFLICT (product, year, month, lat_idx, lon_idx) DO UPDATE
   SET detections = EXCLUDED.detections, frp_sum = EXCLUDED.frp_sum, iso3 = EXCLUDED.iso3
 WHERE (fire_cells.detections, fire_cells.frp_sum, fire_cells.iso3) IS DISTINCT FROM
       (EXCLUDED.detections, EXCLUDED.frp_sum, EXCLUDED.iso3)`

// UpsertFireCells writes batch in one transaction: COPY into a temp table,
// then INSERT ... ON CONFLICT (product, year, month, lat_idx, lon_idx) DO
// UPDATE, only where something differs, so replaying an unchanged batch
// returns 0. An empty ISO3 (ocean) is stored as NULL. A key repeated inside
// the batch keeps its last cell.
func (d *DB) UpsertFireCells(ctx context.Context, batch []domain.FireCell) (int, error) {
	type key struct {
		product        string
		year, month    int16
		latIdx, lonIdx int32
	}
	pos := make(map[key]int, len(batch))
	rows := make([][]any, 0, len(batch))
	for _, c := range batch {
		row := []any{c.Product, c.Year, c.Month, c.LatIdx, c.LonIdx, nullText(c.ISO3), c.Detections, c.FRPSum}
		k := key{c.Product, c.Year, c.Month, c.LatIdx, c.LonIdx}
		if p, dup := pos[k]; dup {
			rows[p] = row
			continue
		}
		pos[k] = len(rows)
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return 0, nil
	}
	var affected int64
	err := pgx.BeginFunc(ctx, d.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `CREATE TEMP TABLE fire_stage (product text, year smallint, month smallint, lat_idx int, lon_idx int, iso3 text, detections int, frp_sum real) ON COMMIT DROP`); err != nil {
			return fmt.Errorf("create stage: %w", err)
		}
		cols := []string{"product", "year", "month", "lat_idx", "lon_idx", "iso3", "detections", "frp_sum"}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"fire_stage"}, cols, pgx.CopyFromRows(rows)); err != nil {
			return fmt.Errorf("copy to stage: %w", err)
		}
		tag, err := tx.Exec(ctx, upsertFireSQL)
		if err != nil {
			return fmt.Errorf("upsert: %w", err)
		}
		affected = tag.RowsAffected()
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("upsert %d fire cells: %w", len(rows), err)
	}
	return int(affected), nil
}

// cellRadiusM is the AssignCellCountries "nearest" radius.
const cellRadiusM = 50000

// AssignCellCountries sets ISO3 on every cell by the cell centre
// ((idx + 0.5) · 0.25°): the containing country, else the nearest country
// within 50 km (geodesic, ties by iso3), else "" (ocean/unassigned). Each
// distinct (lat_idx, lon_idx) is resolved once, in one transaction.
func (d *DB) AssignCellCountries(ctx context.Context, cells []domain.FireCell) error {
	if len(cells) == 0 {
		return nil
	}
	type key struct{ lat, lon int32 }
	seen := make(map[key]bool, len(cells))
	var rows [][]any
	for _, c := range cells {
		k := key{c.LatIdx, c.LonIdx}
		if !seen[k] {
			seen[k] = true
			rows = append(rows, []any{c.LatIdx, c.LonIdx})
		}
	}
	iso := make(map[key]string, len(rows))
	err := pgx.BeginFunc(ctx, d.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `CREATE TEMP TABLE cell_centres (lat_idx int, lon_idx int, iso3 text, geom geometry(Point, 4326)) ON COMMIT DROP`); err != nil {
			return err
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"cell_centres"}, []string{"lat_idx", "lon_idx"}, pgx.CopyFromRows(rows)); err != nil {
			return fmt.Errorf("copy cell centres: %w", err)
		}
		for _, q := range []string{
			`UPDATE cell_centres SET geom = ST_SetSRID(ST_MakePoint((lon_idx + 0.5) * 0.25, (lat_idx + 0.5) * 0.25), 4326)`,
			`CREATE INDEX ON cell_centres USING gist (geom)`,
			`ANALYZE cell_centres`,
			`UPDATE cell_centres cc SET iso3 = c.iso3
  FROM (SELECT DISTINCT ON (cc.lat_idx, cc.lon_idx) cc.lat_idx, cc.lon_idx, c.iso3
          FROM cell_centres cc JOIN countries c ON ST_Contains(c.geom, cc.geom)
         ORDER BY cc.lat_idx, cc.lon_idx, c.iso3) c
 WHERE cc.lat_idx = c.lat_idx AND cc.lon_idx = c.lon_idx`,
		} {
			if _, err := tx.Exec(ctx, q); err != nil {
				return err
			}
		}
		var left bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM cell_centres WHERE iso3 IS NULL)`).Scan(&left); err != nil {
			return err
		}
		if left {
			qs := countryPartsSQL("cell_parts", true)
			qs = append(qs, countryBoxesSQL("cell_boxes", "cell_parts", cellRadiusM)...)
			qs = append(qs, fmt.Sprintf(`
UPDATE cell_centres cc SET iso3 = n.iso3
  FROM (SELECT DISTINCT ON (c.lat_idx, c.lon_idx) c.lat_idx, c.lon_idx, c.iso3
          FROM (SELECT p.lat_idx, p.lon_idx, b.iso3, ST_Distance(b.geog, p.geom::geography) AS dist
                  FROM cell_boxes b
                  CROSS JOIN LATERAL (
                    SELECT p.lat_idx, p.lon_idx, p.geom FROM cell_centres p
                     WHERE p.iso3 IS NULL AND p.geom && b.env
                       AND ST_DWithin(b.geom, p.geom, b.dlim)
                       AND ST_DWithin(b.geog, p.geom::geography, %d)
                    OFFSET 0) p) c
         ORDER BY c.lat_idx, c.lon_idx, c.dist, c.iso3) n
 WHERE cc.lat_idx = n.lat_idx AND cc.lon_idx = n.lon_idx`, cellRadiusM))
			for _, q := range qs {
				if _, err := tx.Exec(ctx, q); err != nil {
					return err
				}
			}
		}
		res, err := tx.Query(ctx, `SELECT lat_idx, lon_idx, iso3 FROM cell_centres WHERE iso3 IS NOT NULL`)
		if err != nil {
			return err
		}
		defer res.Close()
		for res.Next() {
			var k key
			var s string
			if err := res.Scan(&k.lat, &k.lon, &s); err != nil {
				return err
			}
			iso[k] = s
		}
		return res.Err()
	})
	if err != nil {
		return fmt.Errorf("assign cell countries (%d cells): %w", len(rows), err)
	}
	for i := range cells {
		cells[i].ISO3 = iso[key{cells[i].LatIdx, cells[i].LonIdx}]
	}
	return nil
}
