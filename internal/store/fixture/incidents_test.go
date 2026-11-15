package fixture

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/dwts/backend/internal/domain"
)

func TestLoadIncidentsFixture(t *testing.T) {
	r, err := LoadIncidents(filepath.Join("..", "..", "..", "testdata", "incidents_fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	inc, err := r.GetIncident(ctx, 500812)
	if err != nil || inc.SourceID != "us6000jllz" || inc.StartTime.Year() != 2023 || inc.Lon == 0 {
		t.Fatalf("turkiye: %+v %v", inc, err)
	}
	if _, err := r.GetIncident(ctx, 1); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
	if id, err := r.FindBySource(ctx, "usgs", "us6000rtdt"); err != nil || id != 700001 {
		t.Errorf("FindBySource: %d %v", id, err)
	}
	if _, _, err := r.ListIncidents(ctx, domain.IncidentFilter{}); !errors.Is(err, ErrNotImplemented) {
		t.Errorf("list: %v", err)
	}
	if _, err := NewIncidents([]domain.Incident{{ID: 1, Type: "earthquake"}}); err == nil {
		t.Error("zero time must fail")
	}
}
