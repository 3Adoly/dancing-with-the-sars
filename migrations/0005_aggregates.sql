-- +goose Up
-- 0005_aggregates.sql
-- Per-country counts. An incident in 3 countries adds 1 to each of them.
CREATE MATERIALIZED VIEW mv_counts AS
  SELECT i.type, COALESCE(ic.iso3, '') AS iso3, i.year, count(*)::int AS n
  FROM incidents i LEFT JOIN incident_countries ic ON ic.incident_id = i.id
  WHERE i.counted
  GROUP BY 1, 2, 3
  UNION ALL
  SELECT 'fire_activity', COALESCE(iso3, ''), year, sum(detections)::int
  FROM fire_cells GROUP BY 2, 3;
CREATE UNIQUE INDEX mv_counts_pk ON mv_counts (type, iso3, year);

-- DISTINCT incidents per type/year (the choropleth "total"). Point C of the review.
CREATE MATERIALIZED VIEW mv_totals AS
  SELECT type, year, count(*)::int AS n FROM incidents WHERE counted GROUP BY 1, 2
  UNION ALL
  SELECT 'fire_activity', year, sum(detections)::int FROM fire_cells GROUP BY 2;
CREATE UNIQUE INDEX mv_totals_pk ON mv_totals (type, year);

CREATE MATERIALIZED VIEW mv_grid_1deg AS
  SELECT type, year, floor(ST_Y(geom))::int AS lat_idx, floor(ST_X(geom))::int AS lon_idx, count(*)::int AS n
  FROM incidents WHERE counted GROUP BY 1, 2, 3, 4
  UNION ALL
  SELECT 'fire_activity', year, floor(lat_idx * 0.25)::int, floor(lon_idx * 0.25)::int, sum(detections)::int
  FROM fire_cells GROUP BY 2, 3, 4;
CREATE UNIQUE INDEX mv_grid_1deg_pk ON mv_grid_1deg (type, year, lat_idx, lon_idx);

-- +goose Down
DROP MATERIALIZED VIEW IF EXISTS mv_grid_1deg;
DROP MATERIALIZED VIEW IF EXISTS mv_totals;
DROP MATERIALIZED VIEW IF EXISTS mv_counts;
