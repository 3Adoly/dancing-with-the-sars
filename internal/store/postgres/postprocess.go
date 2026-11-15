package postgres

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// postprocessStep is one step of Plan_Detailed_Level.md §6. Its statements
// run in order inside one transaction, so a failing step leaves no partial
// state behind. Every step is idempotent.
type postprocessStep struct {
	name  string // P0 … P11, as in the plan (P0 = preparation)
	desc  string
	stmts []stmt
}

// stmt is one SQL statement with its arguments.
type stmt struct {
	sql  string
	args []any
}

func sqls(qs ...string) []stmt {
	out := make([]stmt, len(qs))
	for i, q := range qs {
		out[i] = stmt{sql: q}
	}
	return out
}

// countryParts is the session temp table of country pieces (geo.go), built
// by P0 and used by P2 and P5(c) for their search boxes. Postprocess runs
// every step on one connection so it stays visible.
const countryParts = "pp_country_parts"

// nearestRadiusM is the P2 radius; trackRadiusM the P5(c) storm-track radius.
const (
	nearestRadiusM = 200000
	trackRadiusM   = 100000
)

// coverageNotes is the P10 note per type (source_coverage.note), shown next
// to the counts so nobody reads a catalog gap as a trend.
var coverageNotes = map[string]string{
	"earthquake":    "USGS M≥4.5; catalog incomplete before ~1973",
	"flood":         "DFO 1985–2021, GDACS 2022→",
	"landslide":     "NASA GLC 1988–2017, EONET 2018→ (sparse)",
	"fire_activity": "MODIS active-fire detections (not events)",
	"storm":         "IBTrACS tropical cyclones (≥34 kt); a storm counts in every country its track passes within 100 km",
	"tsunami":       "NOAA NCEI HazEL tsunami events",
	"volcano":       "NOAA NCEI HazEL significant eruptions",
	"wildfire":      "NASA EONET wildfire events (2000→; sparse before ~2015)",
	"drought":       "GDACS drought events",
	"other":         "NASA EONET (dust/haze, man-made, sea/lake ice, snow, temperature extremes, water color)",
}

// coverageNoteArgs returns coverageNotes as two parallel arrays for unnest.
func coverageNoteArgs() []any {
	types := make([]string, 0, len(coverageNotes))
	for t := range coverageNotes {
		types = append(types, t)
	}
	sort.Strings(types)
	notes := make([]string, len(types))
	for i, t := range types {
		notes[i] = coverageNotes[t]
	}
	return []any{types, notes}
}

// p8Excluded is the §6.4 "stored but not counted" rule from Mohammed_Guide.md
// M5. Every column it reads is NOT NULL, so it is never NULL.
const p8Excluded = `
     dup_of IS NOT NULL
  OR (source='eonet' AND type IN ('earthquake','volcano','storm','drought'))
  OR (source='eonet' AND type='flood')
  OR (source='eonet' AND type='landslide' AND year <= 2017)
  OR (source='gdacs' AND type='flood' AND year <= 2021)
  OR (source='gdacs' AND type='wildfire')
  OR (source='gdacs' AND type='volcano' AND EXISTS (SELECT 1 FROM incidents WHERE source='gvp'))
`

// p5Statements rebuilds incident_countries from (a) the primary country,
// (b) every hinted country that exists in countries and (c) for storms every
// country within 100 km (geodesic) of the track. The wanted set is built in
// a temp table and applied as a diff (delete the stale pairs, insert the
// missing ones): the same end state as TRUNCATE + INSERT, but a re-run on
// unchanged data writes nothing, and every inserted row pays a foreign-key
// check on incidents (~150 µs each on the dev laptop).
//
// For (c) the track is cut into segments so each has a small box; a segment
// that crosses ±180° gets its western end shifted by +360° for the planar
// tests (pseg), while the geodesic test uses the original. The Guide's
// prefilter (track && ST_Expand(c.geom, 1.0)) would miss matches above ~26°
// latitude, where 100 km is more than 1° of longitude; the boxes (geo.go) are
// a strict superset at any latitude.
func p5Statements() []string {
	qs := []string{
		`CREATE TEMP TABLE pp_wanted (incident_id bigint, iso3 text, PRIMARY KEY (incident_id, iso3)) ON COMMIT DROP`,
		// (a) the primary country
		`INSERT INTO pp_wanted SELECT id, primary_iso3 FROM incidents WHERE primary_iso3 IS NOT NULL
  ON CONFLICT DO NOTHING`,
		// (b) the hinted countries
		`INSERT INTO pp_wanted
  SELECT i.id, h.iso3
    FROM incidents i CROSS JOIN LATERAL unnest(i.country_hints) AS h(iso3)
    JOIN countries c ON c.iso3 = h.iso3
  ON CONFLICT DO NOTHING`,
	}
	// (c) storm tracks
	qs = append(qs, countryBoxesSQL("pp_boxes", countryParts, trackRadiusM)...)
	qs = append(qs,
		`CREATE TEMP TABLE pp_segments ON COMMIT DROP AS
  SELECT i.id, d.geom AS seg,
         CASE WHEN ST_XMax(d.geom) - ST_XMin(d.geom) > 180
              THEN ST_SetSRID(ST_MakeLine(
                     ST_MakePoint(ST_X(ST_PointN(d.geom, 1)) + CASE WHEN ST_X(ST_PointN(d.geom, 1)) < 0 THEN 360 ELSE 0 END, ST_Y(ST_PointN(d.geom, 1))),
                     ST_MakePoint(ST_X(ST_PointN(d.geom, 2)) + CASE WHEN ST_X(ST_PointN(d.geom, 2)) < 0 THEN 360 ELSE 0 END, ST_Y(ST_PointN(d.geom, 2)))), 4326)
              ELSE d.geom END AS pseg
    FROM incidents i, ST_DumpSegments(i.track) AS d
   WHERE i.type = 'storm' AND i.track IS NOT NULL`,
		`CREATE INDEX ON pp_segments USING gist (pseg)`,
		`ANALYZE pp_segments`,
		// Box-first (OFFSET 0 keeps the box as the outer loop); the planar
		// necessary bound, then the planar sufficient bound or the geodesic
		// test. The sufficient bound measures the segment as drawn in lon/lat,
		// which differs from the great-circle segment the geodesic test uses by
		// a few km at most for 6-hourly fixes; on the seed and on 13k synthetic
		// tracks the result equals the geodesic-only query exactly.
		fmt.Sprintf(`
INSERT INTO pp_wanted
  SELECT DISTINCT s.id, b.iso3
    FROM pp_boxes b
    CROSS JOIN LATERAL (
      SELECT s.id FROM pp_segments s
       WHERE s.pseg && b.env
         AND ST_DWithin(b.geom, s.pseg, b.dlim)
         AND (ST_DWithin(b.geom, s.pseg, %g) OR ST_DWithin(b.geog, s.seg::geography, %d))
      OFFSET 0) s
  ON CONFLICT DO NOTHING`, float64(trackRadiusM)/maxMetresPerDegree, trackRadiusM),
		// apply the diff (the PK index makes ON CONFLICT the cheap "is it
		// there" test; a NOT EXISTS against the table being filled was planned
		// as a nested loop over its growing heap)
		`ANALYZE pp_wanted`,
		`DELETE FROM incident_countries ic
  WHERE NOT EXISTS (SELECT 1 FROM pp_wanted w WHERE w.incident_id = ic.incident_id AND w.iso3 = ic.iso3)`,
		`INSERT INTO incident_countries (incident_id, iso3)
  SELECT incident_id, iso3 FROM pp_wanted
  ON CONFLICT DO NOTHING`,
	)
	return qs
}

// postprocessSteps returns the ordered step list.
func postprocessSteps() []postprocessStep {
	return []postprocessStep{
		{
			// Fresh planner statistics after a bulk load: without them P1 picked
			// a plan that took minutes on 28k points instead of one second.
			name: "P0", desc: "analyze, cut countries into pieces",
			stmts: sqls(append([]string{
				`ANALYZE incidents`,
				`DROP TABLE IF EXISTS pg_temp.` + countryParts,
			}, countryPartsSQL(countryParts, false)...)...),
		},
		{
			name: "P1", desc: "country by containment",
			stmts: sqls(`
UPDATE incidents i SET primary_iso3 = c.iso3, country_method = 'contains'
  FROM countries c
 WHERE i.country_method IS NULL AND ST_Contains(c.geom, i.geom)`),
		},
		{
			// The plan's UPDATE … FROM LATERAL cannot reference the target table
			// in Postgres, and one geography distance per (incident, whole
			// country) is ~10 ms/row. The unassigned points go to a temp table
			// with its own index and are joined box-first against the country
			// pieces (geo.go; OFFSET 0 keeps the box as the outer loop). The
			// semantics are unchanged: the nearest country by geodesic
			// distance within 200 km, ties broken by iso3.
			name: "P2", desc: "nearest country within 200 km",
			stmts: sqls(append(countryBoxesSQL("pp_boxes", countryParts, nearestRadiusM),
				`CREATE TEMP TABLE pp_unassigned ON COMMIT DROP AS
  SELECT id, geom FROM incidents WHERE country_method IS NULL`,
				`CREATE INDEX ON pp_unassigned USING gist (geom)`,
				`ANALYZE pp_unassigned`,
				fmt.Sprintf(`
UPDATE incidents i SET primary_iso3 = n.iso3, country_method = 'nearest'
  FROM (SELECT DISTINCT ON (c.id) c.id, c.iso3
          FROM (SELECT u.id, b.iso3, ST_Distance(b.geog, u.geom::geography) AS dist
                  FROM pp_boxes b
                  CROSS JOIN LATERAL (
                    SELECT u.id, u.geom FROM pp_unassigned u
                     WHERE u.geom && b.env
                       AND ST_DWithin(b.geom, u.geom, b.dlim)
                       AND ST_DWithin(b.geog, u.geom::geography, %d)
                    OFFSET 0) u) c
         ORDER BY c.id, c.dist, c.iso3) n
 WHERE i.id = n.id`, nearestRadiusM))...),
		},
		{
			name: "P3", desc: "source hint fallback",
			stmts: sqls(`
UPDATE incidents SET primary_iso3 = country_hints[1], country_method = 'source'
 WHERE country_method IS NULL AND country_hints IS NOT NULL
   AND country_hints[1] IN (SELECT iso3 FROM countries)`),
		},
		{
			// primary_iso3 is cleared too, so a stale country can never survive.
			name: "P4", desc: "ocean",
			stmts: sqls(`UPDATE incidents SET primary_iso3 = NULL, country_method = 'ocean' WHERE country_method IS NULL`),
		},
		{
			name: "P5", desc: "rebuild incident_countries",
			stmts: sqls(p5Statements()...),
		},
		{
			// NOAA→USGS: |Δt| < 120 s and < 100 km. When several USGS quakes
			// qualify (a mainshock and its aftershock), the closest in time, then
			// in distance, then the lowest id wins, so the result is deterministic.
			// The deaths and damage copy runs every time from all current
			// duplicates, so a re-ingested USGS row gets them back.
			name: "P6", desc: "dedup earthquakes NOAA→USGS",
			stmts: sqls(`
UPDATE incidents n SET dup_of = m.uid
  FROM (SELECT DISTINCT ON (n.id) n.id AS nid, u.id AS uid
          FROM incidents n
          JOIN incidents u ON u.source = 'usgs' AND u.type = 'earthquake'
                          AND u.start_time > n.start_time - interval '120 seconds'
                          AND u.start_time < n.start_time + interval '120 seconds'
                          AND abs(extract(epoch FROM n.start_time - u.start_time)) < 120
                          AND ST_DWithin(n.geom::geography, u.geom::geography, 100000)
         WHERE n.source = 'noaa_eq' AND n.dup_of IS NULL
         ORDER BY n.id, abs(extract(epoch FROM n.start_time - u.start_time)),
                  ST_Distance(n.geom::geography, u.geom::geography), u.id) m
 WHERE n.id = m.nid`, `
UPDATE incidents u
   SET deaths = greatest(a.deaths, u.deaths),
       props  = CASE WHEN a.damage IS NULL THEN u.props
                     ELSE u.props || jsonb_build_object('damageAmountOrder', a.damage) END
  FROM (SELECT dup_of AS uid, max(deaths) AS deaths,
               max((props->>'damageAmountOrder')::numeric)
                 FILTER (WHERE jsonb_typeof(props->'damageAmountOrder') = 'number') AS damage
          FROM incidents
         WHERE source = 'noaa_eq' AND dup_of IS NOT NULL
         GROUP BY dup_of) a
 WHERE u.id = a.uid
   AND (u.deaths IS DISTINCT FROM greatest(a.deaths, u.deaths)
        OR (a.damage IS NOT NULL AND (u.props->'damageAmountOrder') IS DISTINCT FROM to_jsonb(a.damage)))`),
		},
		{
			// gvp is canonical for volcanoes: noaa_vo rows matching a gvp eruption are duplicates (not counted, P8).
			name: "P7", desc: "dedup volcanoes NOAA→GVP",
			stmts: sqls(`
UPDATE incidents n SET dup_of = m.gid
  FROM (SELECT n.id AS nid, min(g.id) AS gid
          FROM incidents n
          JOIN incidents g ON g.source = 'gvp' AND g.year = n.year AND lower(g.name) = lower(n.name)
         WHERE n.source = 'noaa_vo' AND n.dup_of IS NULL AND n.name IS NOT NULL
         GROUP BY n.id) m
 WHERE n.id = m.nid
   AND EXISTS (SELECT 1 FROM incidents WHERE source = 'gvp')`),
		},
		{
			// Mohammed_Guide.md M5 (the expression is p8Excluded), plus a WHERE
			// so only rows whose flag changes are rewritten.
			name: "P8", desc: "counted flags",
			stmts: sqls(`UPDATE incidents SET counted = NOT (` + p8Excluded + `) WHERE counted = (` + p8Excluded + `)`),
		},
		{
			name: "P9", desc: "refresh materialized views",
			stmts: sqls(
				`REFRESH MATERIALIZED VIEW CONCURRENTLY mv_counts`,
				`REFRESH MATERIALIZED VIEW CONCURRENTLY mv_totals`,
				`REFRESH MATERIALIZED VIEW CONCURRENTLY mv_grid_1deg`,
			),
		},
		{
			name: "P10", desc: "source coverage",
			stmts: []stmt{
				{sql: `TRUNCATE source_coverage`},
				{sql: `
INSERT INTO source_coverage (type, source, from_year, to_year, n, note)
  SELECT s.type, s.source, s.from_year, s.to_year, s.n, n.note
    FROM (SELECT type, source, min(year) AS from_year, max(year) AS to_year, count(*)::int AS n
            FROM incidents WHERE counted GROUP BY 1, 2
          UNION ALL
          SELECT 'fire_activity', 'firms', min(year), max(year), sum(detections)::int
            FROM fire_cells HAVING count(*) > 0) s
    LEFT JOIN unnest($1::text[], $2::text[]) AS n(type, note) ON n.type = s.type`,
					args: coverageNoteArgs()},
			},
		},
		{
			name: "P11", desc: "bump dataset version",
			stmts: sqls(`UPDATE dataset_meta SET version = version + 1, updated_at = now()`),
		},
	}
}

// Postprocess runs the post-processing steps of Plan_Detailed_Level.md §6 in
// order, each in its own transaction, and logs one line per step with its
// duration and affected rows. It stops at the first failing step; steps that
// already committed stay committed (they are idempotent, so a re-run is safe).
// All steps share one connection because P0 builds a session temp table.
func (d *DB) Postprocess(ctx context.Context) error {
	total := time.Now()
	conn, err := d.Pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("postprocess: acquire connection: %w", err)
	}
	defer conn.Release()
	defer func() {
		dctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := conn.Exec(dctx, `DROP TABLE IF EXISTS pg_temp.`+countryParts); err != nil {
			d.Log.Warn("postprocess: drop temp table", "err", err)
		}
	}()
	steps := postprocessSteps()
	for _, s := range steps {
		start := time.Now()
		rows, err := d.runStep(ctx, conn.Conn(), s)
		if err != nil {
			return fmt.Errorf("postprocess %s (%s): %w", s.name, s.desc, err)
		}
		d.Log.Info("postprocess", "step", s.name, "dur", time.Since(start).Round(time.Millisecond), "rows", rows, "desc", s.desc)
	}
	d.Log.Info("postprocess done", "steps", len(steps), "dur", time.Since(total).Round(time.Millisecond))
	return nil
}

// runStep executes one step's statements in a single transaction and returns
// the total number of rows they inserted, updated or deleted in real tables
// (the pp_* helper temp tables do not count).
func (d *DB) runStep(ctx context.Context, conn *pgx.Conn, s postprocessStep) (int64, error) {
	var rows int64
	err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		for i, q := range s.stmts {
			start := time.Now()
			tag, err := tx.Exec(ctx, q.sql, q.args...)
			if err != nil {
				return err
			}
			d.Log.Debug("postprocess statement", "step", s.name, "stmt", i, "dur", time.Since(start).Round(time.Millisecond), "tag", tag.String())
			if (tag.Insert() || tag.Update() || tag.Delete()) && !strings.HasPrefix(strings.TrimSpace(q.sql), "INSERT INTO pp_") {
				rows += tag.RowsAffected()
			}
		}
		return nil
	})
	return rows, err
}
