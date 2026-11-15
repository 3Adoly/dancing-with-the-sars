-- +goose Up
-- 0003_incidents.sql
CREATE TABLE incidents (
  id             bigserial PRIMARY KEY,
  source         text        NOT NULL,
  source_id      text        NOT NULL,
  type           text        NOT NULL CHECK (type IN ('earthquake','volcano','tsunami','wildfire',
                                                      'flood','landslide','storm','drought','other')),
  title          text        NOT NULL,
  name           text,
  description    text,
  start_time     timestamptz NOT NULL,
  end_time       timestamptz,
  time_precision text        NOT NULL DEFAULT 'second' CHECK (time_precision IN ('second','day','month','year')),
  year           smallint    NOT NULL,              -- UTC year, set by loader, >= MIN_YEAR
  geom           geometry(Point, 4326) NOT NULL,
  track          geometry(LineString, 4326),        -- storms (simplified)
  magnitude      real,
  magnitude_unit text,
  severity       smallint    NOT NULL DEFAULT 0 CHECK (severity BETWEEN 0 AND 3),
  deaths         integer,
  primary_iso3   text REFERENCES countries(iso3),
  country_method text,                              -- contains | nearest | source | ocean
  country_hints  text[],                            -- ISO3 list given by the source (DFO, GDACS, GLC)
  url            text,
  props          jsonb       NOT NULL DEFAULT '{}',
  raw            jsonb,
  raw_hash       text        NOT NULL,              -- md5(raw) → skip no-op updates
  search_text    text        NOT NULL,              -- lower(unaccent(title name place type year source_id))
  dup_of         bigint REFERENCES incidents(id),
  counted        boolean     NOT NULL DEFAULT true, -- false = stored & clickable but excluded from counts (§6.4)
  ingested_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE (source, source_id)
);
CREATE INDEX incidents_geom_gix    ON incidents USING gist (geom);
CREATE INDEX incidents_type_year   ON incidents (type, year) INCLUDE (severity, magnitude);
CREATE INDEX incidents_type_time   ON incidents (type, start_time DESC, id DESC);
CREATE INDEX incidents_iso_type_yr ON incidents (primary_iso3, type, year);
CREATE INDEX incidents_sev_order   ON incidents (severity DESC, magnitude DESC NULLS LAST, id DESC);
CREATE INDEX incidents_search_trgm ON incidents USING gin (search_text gin_trgm_ops);
CREATE INDEX incidents_source_id   ON incidents (source_id);
CREATE INDEX incidents_unassigned  ON incidents (id) WHERE country_method IS NULL;

CREATE TABLE incident_countries (
  incident_id bigint NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
  iso3        text   NOT NULL REFERENCES countries(iso3),
  PRIMARY KEY (incident_id, iso3)
);
CREATE INDEX incident_countries_iso ON incident_countries (iso3);

-- +goose Down
DROP TABLE IF EXISTS incident_countries;
DROP TABLE IF EXISTS incidents;
