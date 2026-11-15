package fixture

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/dwts/backend/internal/domain"
)

func TestLoadCommittedFixture(t *testing.T) {
	r, err := Load(filepath.Join("..", "..", "..", "testdata", "stats_fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	v, _, _ := r.DatasetVersion(ctx)
	counts, _ := r.LoadCounts(ctx)
	totals, _ := r.LoadTotals(ctx)
	cov, _ := r.Coverage(ctx)
	if v != 1 || len(counts) < 1000 || len(totals) < 100 || len(cov) < 5 {
		t.Fatalf("v=%d counts=%d totals=%d cov=%d", v, len(counts), len(totals), len(cov))
	}
	var jpnEQ, ocean int64
	for _, c := range counts {
		if c.Type == domain.Earthquake && c.ISO3 == "JPN" {
			jpnEQ += int64(c.N)
		}
		if c.ISO3 == "" {
			ocean += int64(c.N)
		}
		if c.Year < 1950 || c.Year > 2025 {
			t.Fatalf("year out of range: %+v", c)
		}
	}
	if jpnEQ < 5000 || ocean == 0 {
		t.Errorf("implausible fixture: JPN quakes=%d ocean=%d", jpnEQ, ocean)
	}
}

func TestNewRejectsBadInput(t *testing.T) {
	if _, err := New(File{}); err == nil {
		t.Error("version 0 must fail")
	}
	if _, err := New(File{DatasetVersion: 1, Counts: []Series{{Type: "meteor", FromYear: 2000, N: []int32{1}}}}); err == nil {
		t.Error("unknown type must fail")
	}
}
