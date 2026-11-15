-- +goose Up
-- 0004_fire_cells.sql
CREATE TABLE fire_cells (
  product    text     NOT NULL,           -- MODIS_SP
  year       smallint NOT NULL,
  month      smallint NOT NULL,
  lat_idx    integer  NOT NULL,           -- floor(lat / 0.25)
  lon_idx    integer  NOT NULL,           -- floor(lon / 0.25)
  iso3       text REFERENCES countries(iso3),   -- NULL = ocean/unassigned
  detections integer  NOT NULL,
  frp_sum    real     NOT NULL DEFAULT 0,
  PRIMARY KEY (product, year, month, lat_idx, lon_idx)
);
CREATE INDEX fire_cells_iso_year ON fire_cells (iso3, year);

-- +goose Down
DROP TABLE IF EXISTS fire_cells;
