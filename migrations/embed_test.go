package migrations_test

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/dwts/backend/migrations"
)

// TestEmbeddedMigrations checks the shape goose relies on: six consecutively
// numbered files, each with an Up and a Down section (Down after Up).
func TestEmbeddedMigrations(t *testing.T) {
	want := []string{
		"0001_extensions.sql",
		"0002_countries.sql",
		"0003_incidents.sql",
		"0004_fire_cells.sql",
		"0005_aggregates.sql",
		"0006_ops.sql",
	}
	got, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("embedded migrations = %v, want %v", got, want)
	}
	for _, name := range want {
		b, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		up := strings.Index(s, "-- +goose Up")
		down := strings.Index(s, "-- +goose Down")
		if up != 0 {
			t.Errorf("%s: must start with '-- +goose Up' (index %d)", name, up)
		}
		if down <= up {
			t.Errorf("%s: missing '-- +goose Down' section after Up (index %d)", name, down)
		}
		if strings.Count(s, "-- +goose Up") != 1 || strings.Count(s, "-- +goose Down") != 1 {
			t.Errorf("%s: want exactly one Up and one Down marker", name)
		}
	}
}
