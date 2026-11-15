package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/dwts/backend/internal/domain"
)

// TestReadSideIntegration loads testdata/seed.sql, postprocesses it, then exercises the read side.
// Needs TEST_DATABASE_URL (a *_test database); skipped otherwise.
func TestReadSideIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	db := newTestDB(t, ctx)
	resetIncidents(t, ctx, db)
	seed, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, string(seed)); err != nil {
		t.Fatalf("load seed: %v", err)
	}
	if err := db.Postprocess(ctx); err != nil {
		t.Fatalf("postprocess: %v", err)
	}

	// Stats
	if v, _, err := db.DatasetVersion(ctx); err != nil || v < 1 {
		t.Errorf("version %d %v", v, err)
	}
	counts, err := db.LoadCounts(ctx)
	if err != nil || len(counts) == 0 {
		t.Errorf("counts %d %v", len(counts), err)
	}
	totals, err := db.LoadTotals(ctx)
	if err != nil || len(totals) == 0 {
		t.Errorf("totals %d %v", len(totals), err)
	}
	if cov, err := db.Coverage(ctx); err != nil || len(cov) == 0 {
		t.Errorf("coverage %d %v", len(cov), err)
	}
	if cs, err := db.Countries(ctx); err != nil || len(cs) == 0 {
		t.Errorf("countries %d %v", len(cs), err)
	}

	// FindBySource + Get
	id, err := db.FindBySource(ctx, "usgs", "us6000jllz")
	if err != nil {
		t.Fatalf("FindBySource: %v", err)
	}
	inc, err := db.GetIncident(ctx, id)
	if err != nil || inc.SourceID != "us6000jllz" || inc.Type != domain.Earthquake || inc.Countries == nil || inc.Year != 2023 {
		t.Errorf("GetIncident: %+v %v", inc, err)
	}
	kid, err := db.FindBySource(ctx, "ibtracs", "2005236N23285")
	if err == nil {
		k, _ := db.GetIncident(ctx, kid)
		if len(k.Track) == 0 {
			t.Error("Katrina has no track GeoJSON")
		}
	}
	if _, err := db.GetIncident(ctx, 1<<40); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing incident: %v", err)
	}

	// List: pages via cursor never overlap and follow the sort.
	seen := map[int64]bool{}
	cursor := ""
	pages := 0
	for {
		items, next, err := db.ListIncidents(ctx, domain.IncidentFilter{Limit: 25, Cursor: cursor, Sort: "severity"})
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range items {
			if seen[it.ID] {
				t.Fatalf("duplicate id %d across pages", it.ID)
			}
			seen[it.ID] = true
		}
		pages++
		if next == "" || pages > 100 {
			break
		}
		cursor = next
	}
	if pages < 2 || len(seen) < 100 {
		t.Errorf("paging: %d pages, %d rows", pages, len(seen))
	}
	timeItems, _, err := db.ListIncidents(ctx, domain.IncidentFilter{Limit: 50, Sort: "time"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(timeItems); i++ {
		if timeItems[i].StartTime.After(timeItems[i-1].StartTime) {
			t.Fatal("time sort not descending")
		}
	}
	// bbox around Japan + type filter
	jp, _, err := db.ListIncidents(ctx, domain.IncidentFilter{Types: []domain.IncidentType{domain.Earthquake},
		BBox: &domain.BBox{MinLon: 128, MinLat: 30, MaxLon: 146, MaxLat: 46}, Limit: 500})
	if err != nil || len(jp) == 0 {
		t.Errorf("bbox: %d %v", len(jp), err)
	}
	for _, it := range jp {
		if it.Lon < 128 || it.Lon > 146 || it.Lat < 30 || it.Lat > 46 || it.Type != domain.Earthquake {
			t.Errorf("outside filter: %+v", it)
		}
	}

	// Search: typo tolerant + exact source id.
	res, err := db.Search(ctx, domain.SearchQuery{Q: "katrna", Limit: 5})
	if err != nil || len(res) == 0 || res[0].Type != domain.Storm {
		t.Errorf("search katrna: %+v %v", res, err)
	}
	res, err = db.Search(ctx, domain.SearchQuery{Q: "us6000jllz", Limit: 5})
	if err != nil || len(res) == 0 || res[0].ID != id || res[0].Score != 2 {
		t.Errorf("search by source id: %+v %v", res, err)
	}

	// SAR cache round trip.
	r := domain.SARResult{IncidentID: id, Coverage: "ok", WindowDays: 45, FetchedAt: time.Now().UTC().Truncate(time.Second)}
	if err := db.PutSAR(ctx, r); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetSAR(ctx, id)
	if err != nil || got.Coverage != "ok" || !got.FetchedAt.Equal(r.FetchedAt) {
		t.Errorf("GetSAR: %+v %v", got, err)
	}
	if _, err := db.GetSAR(ctx, 1<<40); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetSAR miss: %v", err)
	}
}
