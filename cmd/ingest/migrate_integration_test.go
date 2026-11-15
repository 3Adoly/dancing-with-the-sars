package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dwts/backend/internal/config"
)

// TestMigrateIntegration migrates the separate test database (TEST_DATABASE_URL,
// `make test-db`), checks the schema exists and that a second run is a no-op.
func TestMigrateIntegration(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set (run `make test-db` then `make test-int`)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	var dbName string
	if err := pool.QueryRow(ctx, "SELECT current_database()").Scan(&dbName); err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	if !strings.HasSuffix(dbName, "_test") {
		t.Fatalf("refusing to migrate database %q: integration tests must use the separate dwts_test database", dbName)
	}

	cfg := config.Config{DatabaseURL: url, DBMaxConns: 2, LogLevel: "warn", LogFormat: "text"}
	if err := cmdMigrate(ctx, cfg); err != nil {
		t.Fatalf("first migrate: %v", err)
	}

	names := func(query string) map[string]bool {
		t.Helper()
		rows, err := pool.Query(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		got := map[string]bool{}
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				t.Fatal(err)
			}
			got[n] = true
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return got
	}

	tables := names("SELECT tablename FROM pg_tables WHERE schemaname = 'public'")
	for _, want := range []string{
		"countries", "incidents", "incident_countries", "fire_cells", "ingest_runs",
		"dataset_meta", "source_coverage", "sar_cache", "goose_db_version",
	} {
		if !tables[want] {
			t.Errorf("table %q missing after migrate", want)
		}
	}
	views := names("SELECT matviewname FROM pg_matviews WHERE schemaname = 'public'")
	for _, want := range []string{"mv_counts", "mv_totals", "mv_grid_1deg"} {
		if !views[want] {
			t.Errorf("materialized view %q missing after migrate", want)
		}
	}
	exts := names("SELECT extname FROM pg_extension")
	for _, want := range []string{"postgis", "pg_trgm"} {
		if !exts[want] {
			t.Errorf("extension %q missing after migrate", want)
		}
	}

	var metaRows int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM dataset_meta").Scan(&metaRows); err != nil {
		t.Fatal(err)
	}
	if metaRows != 1 {
		t.Errorf("dataset_meta has %d rows, want exactly 1", metaRows)
	}

	// Second run: nothing pending, so the goose bookkeeping must not change.
	versions := func() (n, maxID int) {
		t.Helper()
		err := pool.QueryRow(ctx, "SELECT count(*), COALESCE(max(id), 0) FROM goose_db_version").Scan(&n, &maxID)
		if err != nil {
			t.Fatal(err)
		}
		return n, maxID
	}
	n1, id1 := versions()
	if err := cmdMigrate(ctx, cfg); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	n2, id2 := versions()
	if n1 != n2 || id1 != id2 {
		t.Errorf("second migrate changed goose_db_version: (%d rows, max id %d) -> (%d rows, max id %d)", n1, id1, n2, id2)
	}
}
