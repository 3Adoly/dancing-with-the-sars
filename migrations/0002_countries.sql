-- +goose Up
-- 0002_countries.sql
CREATE TABLE countries (
  iso3       text PRIMARY KEY,                 -- Natural Earth ADM0_A3 (NOT ISO_A3: -99 for FRA/NOR)
  iso2       text,                             -- ISO_A2 (may be -99 → NULL); used to map GLC country_code
  name       text NOT NULL,
  continent  text,
  geom       geometry(MultiPolygon, 4326) NOT NULL,
  label_pt   geometry(Point, 4326)
);
CREATE INDEX countries_geom_gix ON countries USING gist (geom);
CREATE INDEX countries_geog_gix ON countries USING gist ((geom::geography));
CREATE INDEX countries_iso2 ON countries (iso2);

-- +goose Down
DROP TABLE IF EXISTS countries;
