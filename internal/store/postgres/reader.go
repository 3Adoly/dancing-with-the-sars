package postgres

// Read side (Ahmed, A2): store.StatsReader, store.IncidentReader and store.SARCache on the
// schema of migrations/0002–0006 (Mohammed, M1). Duplicates (dup_of IS NOT NULL) are hidden from
// lists and search; they stay reachable by id. `counted` only matters for the aggregates (MVs).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/dwts/backend/internal/domain"
	"github.com/dwts/backend/internal/ingest"
	"github.com/dwts/backend/internal/store"
)

var (
	_ store.StatsReader    = (*DB)(nil)
	_ store.IncidentReader = (*DB)(nil)
	_ store.SARCache       = (*DB)(nil)
)

// ErrGridNotImplemented is returned by Grid until task A8.
var ErrGridNotImplemented = fmt.Errorf("grid (A8): %w", domain.ErrNotImplemented)

// ---------- StatsReader ----------

// DatasetVersion reads dataset_meta.
func (d *DB) DatasetVersion(ctx context.Context) (int64, time.Time, error) {
	var v int64
	var at time.Time
	err := d.Pool.QueryRow(ctx, `SELECT version, updated_at FROM dataset_meta LIMIT 1`).Scan(&v, &at)
	return v, at, err
}

// LoadCounts scans mv_counts.
func (d *DB) LoadCounts(ctx context.Context) ([]domain.CountRow, error) {
	rows, err := d.Pool.Query(ctx, `SELECT type, iso3, year, n FROM mv_counts`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.CountRow, error) {
		var c domain.CountRow
		var t string
		err := r.Scan(&t, &c.ISO3, &c.Year, &c.N)
		c.Type = domain.IncidentType(t)
		return c, err
	})
}

// LoadTotals scans mv_totals (DISTINCT incidents).
func (d *DB) LoadTotals(ctx context.Context) ([]domain.TotalRow, error) {
	rows, err := d.Pool.Query(ctx, `SELECT type, year, n FROM mv_totals`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.TotalRow, error) {
		var c domain.TotalRow
		var t string
		err := r.Scan(&t, &c.Year, &c.N)
		c.Type = domain.IncidentType(t)
		return c, err
	})
}

// Coverage merges source_coverage rows per type.
func (d *DB) Coverage(ctx context.Context) ([]domain.Coverage, error) {
	rows, err := d.Pool.Query(ctx, `
		SELECT type, array_agg(source ORDER BY source), coalesce(min(from_year), 0)::int, coalesce(max(to_year), 0)::int,
		       coalesce(string_agg(nullif(note, ''), '; ' ORDER BY source), '')
		FROM source_coverage GROUP BY type ORDER BY type`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.Coverage, error) {
		var c domain.Coverage
		var t string
		err := r.Scan(&t, &c.Sources, &c.FromYear, &c.ToYear, &c.Note)
		c.Type = domain.IncidentType(t)
		return c, err
	})
}

// Countries lists the countries with their label point.
func (d *DB) Countries(ctx context.Context) ([]domain.Country, error) {
	rows, err := d.Pool.Query(ctx, `
		SELECT iso3, coalesce(iso2, ''), name, coalesce(continent, ''),
		       coalesce(ST_X(label_pt), 0), coalesce(ST_Y(label_pt), 0)
		FROM countries ORDER BY iso3`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.Country, error) {
		var c domain.Country
		err := r.Scan(&c.ISO3, &c.ISO2, &c.Name, &c.Continent, &c.Lon, &c.Lat)
		return c, err
	})
}

// ---------- IncidentReader ----------

const incidentCols = `i.id, i.source, i.source_id, i.type, i.title, coalesce(i.name, ''), coalesce(i.description, ''),
	i.start_time, i.end_time, i.time_precision, i.year, ST_X(i.geom), ST_Y(i.geom), i.magnitude,
	coalesce(i.magnitude_unit, ''), i.severity, i.deaths, coalesce(i.primary_iso3, ''), coalesce(i.url, ''), i.props,
	CASE WHEN i.track IS NULL THEN NULL ELSE ST_AsGeoJSON(i.track, 4) END,
	coalesce((SELECT array_agg(ic.iso3 ORDER BY ic.iso3) FROM incident_countries ic WHERE ic.incident_id = i.id), '{}')`

// GetIncident returns one incident with its countries and storm track (GeoJSON).
func (d *DB) GetIncident(ctx context.Context, id int64) (domain.Incident, error) {
	var in domain.Incident
	var typ, prec string
	var props []byte
	var track *string
	err := d.Pool.QueryRow(ctx, `SELECT `+incidentCols+` FROM incidents i WHERE i.id = $1`, id).Scan(
		&in.ID, &in.Source, &in.SourceID, &typ, &in.Title, &in.Name, &in.Description,
		&in.StartTime, &in.EndTime, &prec, &in.Year, &in.Lon, &in.Lat, &in.Magnitude,
		&in.MagnitudeUnit, &in.Severity, &in.Deaths, &in.PrimaryISO3, &in.URL, &props, &track, &in.Countries)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Incident{}, fmt.Errorf("incident %d: %w", id, domain.ErrNotFound)
	}
	if err != nil {
		return domain.Incident{}, err
	}
	in.Type, in.TimePrecision = domain.IncidentType(typ), domain.Precision(prec)
	in.StartTime = in.StartTime.UTC()
	if in.EndTime != nil {
		t := in.EndTime.UTC()
		in.EndTime = &t
	}
	if len(props) > 0 {
		_ = json.Unmarshal(props, &in.Props)
	}
	if len(in.Props) == 0 {
		in.Props = nil
	}
	if track != nil {
		in.Track = json.RawMessage(*track)
	}
	return in, nil
}

// FindBySource maps (source, source_id) to our id.
func (d *DB) FindBySource(ctx context.Context, source, sourceID string) (int64, error) {
	var id int64
	err := d.Pool.QueryRow(ctx, `SELECT id FROM incidents WHERE source = $1 AND source_id = $2`, source, sourceID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("%s/%s: %w", source, sourceID, domain.ErrNotFound)
	}
	return id, err
}

// queryBuilder accumulates WHERE clauses with positional args.
type queryBuilder struct {
	where []string
	args  []any
}

func (q *queryBuilder) arg(v any) string {
	q.args = append(q.args, v)
	return fmt.Sprintf("$%d", len(q.args))
}

func (q *queryBuilder) add(clause string) { q.where = append(q.where, clause) }

func (q *queryBuilder) sql() string {
	if len(q.where) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(q.where, " AND ")
}

// incidentTypes drops fire_activity (aggregate-only; never a row in incidents).
func incidentTypes(ts []domain.IncidentType) []string {
	var out []string
	for _, t := range ts {
		if t != domain.FireActivity {
			out = append(out, string(t))
		}
	}
	return out
}

// bboxClause handles boxes crossing the antimeridian (MinLon > MaxLon) as two envelopes.
func bboxClause(q *queryBuilder, b domain.BBox) string {
	env := func(w, e float64) string {
		return fmt.Sprintf("i.geom && ST_MakeEnvelope(%s, %s, %s, %s, 4326)", q.arg(w), q.arg(b.MinLat), q.arg(e), q.arg(b.MaxLat))
	}
	if b.MinLon > b.MaxLon {
		return "(" + env(b.MinLon, 180) + " OR " + env(-180, b.MaxLon) + ")"
	}
	return env(b.MinLon, b.MaxLon)
}

// buildListQuery returns the SQL and args for ListIncidents (exported to tests via the package).
func buildListQuery(f domain.IncidentFilter) (string, []any, error) {
	q := &queryBuilder{}
	q.add("i.dup_of IS NULL")
	if len(f.Types) > 0 {
		ts := incidentTypes(f.Types)
		if len(ts) == 0 {
			q.add("false") // only fire_activity requested: no incident rows
		} else {
			q.add("i.type = ANY(" + q.arg(ts) + ")")
		}
	}
	if f.Years.From != 0 || f.Years.To != 0 {
		q.add(fmt.Sprintf("i.year BETWEEN %s AND %s", q.arg(int16(f.Years.From)), q.arg(int16(f.Years.To))))
	}
	if f.BBox != nil {
		q.add(bboxClause(q, *f.BBox))
	}
	if f.ISO3 != "" {
		p := q.arg(strings.ToUpper(f.ISO3))
		q.add("(i.primary_iso3 = " + p + " OR EXISTS (SELECT 1 FROM incident_countries ic WHERE ic.incident_id = i.id AND ic.iso3 = " + p + "))")
	}
	if f.MinMagnitude != nil {
		q.add("i.magnitude >= " + q.arg(*f.MinMagnitude))
	}
	if f.MinSeverity > 0 {
		q.add("i.severity >= " + q.arg(int16(f.MinSeverity)))
	}
	var order string
	if f.Cursor != "" {
		c, err := domain.DecodeCursor(f.Cursor)
		if err != nil {
			return "", nil, err
		}
		switch f.Sort {
		case "time":
			if c.T == nil {
				return "", nil, fmt.Errorf("cursor does not match sort=time: %w", domain.ErrInvalid)
			}
			q.add(fmt.Sprintf("(i.start_time, i.id) < (%s, %s)", q.arg(*c.T), q.arg(c.I)))
		default:
			if c.S == nil || c.M == nil {
				return "", nil, fmt.Errorf("cursor does not match sort=severity: %w", domain.ErrInvalid)
			}
			q.add(fmt.Sprintf("(i.severity, coalesce(i.magnitude, -1)::float8, i.id) < (%s, %s, %s)",
				q.arg(*c.S), q.arg(*c.M), q.arg(c.I)))
		}
	}
	if f.Sort == "time" {
		order = " ORDER BY i.start_time DESC, i.id DESC"
	} else {
		order = " ORDER BY i.severity DESC, coalesce(i.magnitude, -1)::float8 DESC, i.id DESC"
	}
	limit := f.Limit
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	sql := `SELECT i.id, i.type, i.title, i.start_time, ST_X(i.geom), ST_Y(i.geom), i.magnitude, i.severity,
	        coalesce(i.primary_iso3, ''), coalesce(i.magnitude, -1)::float8
	        FROM incidents i` + q.sql() + order + " LIMIT " + q.arg(limit+1)
	return sql, q.args, nil
}

// ListIncidents returns one keyset page and the cursor of the next page ("" = last page).
func (d *DB) ListIncidents(ctx context.Context, f domain.IncidentFilter) ([]domain.IncidentSummary, string, error) {
	sql, args, err := buildListQuery(f)
	if err != nil {
		return nil, "", err
	}
	rows, err := d.Pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	limit := f.Limit
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	items := make([]domain.IncidentSummary, 0, min(limit, 1024))
	var lastMag float64
	more := false
	for rows.Next() {
		if len(items) == limit {
			more = true
			break
		}
		var s domain.IncidentSummary
		var typ string
		if err := rows.Scan(&s.ID, &typ, &s.Title, &s.StartTime, &s.Lon, &s.Lat, &s.Magnitude, &s.Severity, &s.PrimaryISO3, &lastMag); err != nil {
			return nil, "", err
		}
		s.Type, s.StartTime = domain.IncidentType(typ), s.StartTime.UTC()
		items = append(items, s)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if more && len(items) > 0 {
		last := items[len(items)-1]
		c := domain.Cursor{I: last.ID}
		if f.Sort == "time" {
			t := last.StartTime
			c.T = &t
		} else {
			sev, m := int16(last.Severity), lastMag
			c.S, c.M = &sev, &m
		}
		next = domain.EncodeCursor(c)
	}
	return items, next, nil
}

// escapeLike escapes LIKE wildcards in user input.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// buildSearchQuery: exact source_id first (score 2), then pg_trgm word similarity on search_text
// (normalized like ingest: lower-case, accents stripped), plus a substring fallback.
func buildSearchQuery(sq domain.SearchQuery) (string, []any) {
	q := &queryBuilder{}
	raw := strings.TrimSpace(sq.Q)
	norm := ingest.SearchText(raw)
	pRaw, pNorm, pLike := q.arg(raw), q.arg(norm), q.arg("%"+escapeLike(norm)+"%")
	q.add("i.dup_of IS NULL")
	q.add(fmt.Sprintf("(i.source_id = %s OR %s <%% i.search_text OR i.search_text LIKE %s)", pRaw, pNorm, pLike))
	if ts := incidentTypes(sq.Types); len(sq.Types) > 0 {
		if len(ts) == 0 {
			q.add("false")
		} else {
			q.add("i.type = ANY(" + q.arg(ts) + ")")
		}
	}
	if sq.Years != nil {
		q.add(fmt.Sprintf("i.year BETWEEN %s AND %s", q.arg(int16(sq.Years.From)), q.arg(int16(sq.Years.To))))
	}
	limit := sq.Limit
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	sql := fmt.Sprintf(`SELECT i.id, i.type, i.title, i.start_time, ST_X(i.geom), ST_Y(i.geom), i.magnitude, i.severity,
	        coalesce(i.primary_iso3, ''),
	        (CASE WHEN i.source_id = %s THEN 2 ELSE word_similarity(%s, i.search_text) END)::float4 AS score
	        FROM incidents i%s
	        ORDER BY score DESC, i.severity DESC, i.magnitude DESC NULLS LAST, i.id DESC
	        LIMIT %s`, pRaw, pNorm, q.sql(), q.arg(limit))
	return sql, q.args
}

// Search finds incidents by name/place/source id (typo-tolerant).
func (d *DB) Search(ctx context.Context, sq domain.SearchQuery) ([]domain.IncidentSummary, error) {
	sql, args := buildSearchQuery(sq)
	rows, err := d.Pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.IncidentSummary, error) {
		var s domain.IncidentSummary
		var typ string
		err := r.Scan(&s.ID, &typ, &s.Title, &s.StartTime, &s.Lon, &s.Lat, &s.Magnitude, &s.Severity, &s.PrimaryISO3, &s.Score)
		s.Type, s.StartTime = domain.IncidentType(typ), s.StartTime.UTC()
		return s, err
	})
	if out == nil {
		out = []domain.IncidentSummary{}
	}
	return out, err
}

// Grid is task A8.
func (d *DB) Grid(context.Context, domain.GridQuery) ([]domain.GridCell, error) {
	return nil, ErrGridNotImplemented
}

// ---------- SARCache ----------

// GetSAR reads sar_cache; FetchedAt comes from the row.
func (d *DB) GetSAR(ctx context.Context, incidentID int64) (domain.SARResult, error) {
	var payload []byte
	var at time.Time
	err := d.Pool.QueryRow(ctx, `SELECT payload, fetched_at FROM sar_cache WHERE incident_id = $1`, incidentID).Scan(&payload, &at)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.SARResult{}, fmt.Errorf("sar cache %d: %w", incidentID, domain.ErrNotFound)
	}
	if err != nil {
		return domain.SARResult{}, err
	}
	var r domain.SARResult
	if err := json.Unmarshal(payload, &r); err != nil {
		return domain.SARResult{}, fmt.Errorf("sar cache %d: bad payload: %w", incidentID, err)
	}
	r.IncidentID, r.FetchedAt = incidentID, at.UTC()
	return r, nil
}

// PutSAR upserts sar_cache.
func (d *DB) PutSAR(ctx context.Context, r domain.SARResult) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	at := r.FetchedAt
	if at.IsZero() {
		at = time.Now()
	}
	_, err = d.Pool.Exec(ctx, `
		INSERT INTO sar_cache (incident_id, fetched_at, payload) VALUES ($1, $2, $3)
		ON CONFLICT (incident_id) DO UPDATE SET fetched_at = EXCLUDED.fetched_at, payload = EXCLUDED.payload`,
		r.IncidentID, at, b)
	return err
}
