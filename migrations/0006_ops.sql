-- +goose Up
-- 0006_ops.sql
CREATE TABLE ingest_runs (
  id bigserial PRIMARY KEY, source text NOT NULL,
  started_at timestamptz NOT NULL DEFAULT now(), finished_at timestamptz,
  status text NOT NULL DEFAULT 'running' CHECK (status IN ('running','ok','failed')),
  rows integer, error text
);
CREATE TABLE dataset_meta (
  id boolean PRIMARY KEY DEFAULT true CHECK (id),
  version bigint NOT NULL DEFAULT 0, updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO dataset_meta DEFAULT VALUES;
CREATE TABLE source_coverage (
  type text NOT NULL, source text NOT NULL, from_year smallint, to_year smallint, n integer, note text,
  PRIMARY KEY (type, source)
);
CREATE TABLE sar_cache (
  incident_id bigint PRIMARY KEY REFERENCES incidents(id) ON DELETE CASCADE,
  fetched_at  timestamptz NOT NULL DEFAULT now(),
  payload     jsonb NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS sar_cache;
DROP TABLE IF EXISTS source_coverage;
DROP TABLE IF EXISTS dataset_meta;
DROP TABLE IF EXISTS ingest_runs;
