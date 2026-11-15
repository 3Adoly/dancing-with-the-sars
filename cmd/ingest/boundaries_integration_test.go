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

// TestBoundariesIntegration runs `ingest boundaries` end to end against the
// test database with the three-country fixture as the (already present) file,
// so nothing is downloaded.
func TestBoundariesIntegration(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set (run `make test-db` then `make test-int`)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var dbName string
	if err := pool.QueryRow(ctx, "SELECT current_database()").Scan(&dbName); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(dbName, "_test") {
		t.Fatalf("refusing to load countries into %q: integration tests must use the separate dwts_test database", dbName)
	}
	if _, err := pool.Exec(ctx, "TRUNCATE countries CASCADE"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), "TRUNCATE countries CASCADE") })

	cfg := config.Config{
		DatabaseURL:    url,
		DBMaxConns:     2,
		BoundariesPath: "../../internal/store/postgres/testdata/countries_fixture.geojson",
	}
	for i := 1; i <= 2; i++ { // the second run is a no-op reload
		if err := cmdBoundaries(ctx, cfg); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		var n int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM countries WHERE iso3 IN ('FRA','JPN','BOW')").Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 3 {
			t.Fatalf("run %d: %d fixture countries loaded, want 3", i, n)
		}
	}
}
