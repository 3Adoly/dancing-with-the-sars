package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/dwts/backend/internal/domain"
)

func TestUpsertFireCellsIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	db := newTestDB(t, ctx)
	resetIncidents(t, ctx, db) // countries FRA, JPN, BOW; truncating countries clears fire_cells too
	if _, err := db.Pool.Exec(ctx, "TRUNCATE fire_cells"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Pool.Exec(context.Background(), "TRUNCATE fire_cells") })

	batch := []domain.FireCell{
		{Product: "MODIS_SP", Year: 2019, Month: 3, LatIdx: 180, LonIdx: 8, ISO3: "FRA", Detections: 2, FRPSum: 15.5},
		{Product: "MODIS_SP", Year: 2019, Month: 3, LatIdx: -1, LonIdx: -1, ISO3: "", Detections: 1, FRPSum: 2},
		{Product: "MODIS_SP", Year: 2019, Month: 4, LatIdx: 180, LonIdx: 8, ISO3: "JPN", Detections: 1},
	}
	n, err := db.UpsertFireCells(ctx, batch)
	if err != nil || n != 3 {
		t.Fatalf("first upsert: n=%d err=%v", n, err)
	}
	type row struct {
		iso *string
		det int
		frp float32
	}
	get := func(month, lat int) row {
		t.Helper()
		var r row
		err := db.Pool.QueryRow(ctx, `SELECT iso3, detections, frp_sum FROM fire_cells
 WHERE product='MODIS_SP' AND year=2019 AND month=$1 AND lat_idx=$2`, month, lat).Scan(&r.iso, &r.det, &r.frp)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := get(3, 180); r.iso == nil || *r.iso != "FRA" || r.det != 2 || r.frp != 15.5 {
		t.Errorf("FRA cell = %+v", r)
	}
	if r := get(3, -1); r.iso != nil || r.det != 1 {
		t.Errorf("ocean cell must have NULL iso3: %+v", r)
	}

	// Replay: one changed cell and a duplicate key inside the batch (last wins).
	batch[0].Detections = 5
	batch = append(batch, domain.FireCell{Product: "MODIS_SP", Year: 2019, Month: 4, LatIdx: 180, LonIdx: 8, ISO3: "JPN", Detections: 7})
	if _, err = db.UpsertFireCells(ctx, batch); err != nil {
		t.Fatal(err)
	}
	if r := get(3, 180); r.det != 5 {
		t.Errorf("update not applied: %+v", r)
	}
	if r := get(4, 180); r.det != 7 {
		t.Errorf("duplicate key: last must win: %+v", r)
	}
	var total int
	if err := db.Pool.QueryRow(ctx, "SELECT count(*) FROM fire_cells").Scan(&total); err != nil || total != 3 {
		t.Errorf("rows = %d, err %v, want 3", total, err)
	}
	if n, err = db.UpsertFireCells(ctx, nil); err != nil || n != 0 {
		t.Errorf("empty batch: n=%d err=%v", n, err)
	}
	bad := []domain.FireCell{{Product: "MODIS_SP", Year: 2019, Month: 5, ISO3: "ZZZ", Detections: 1}}
	if _, err := db.UpsertFireCells(ctx, bad); err == nil {
		t.Error("unknown iso3 accepted")
	}
}
