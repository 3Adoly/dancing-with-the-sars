package fixture

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/dwts/backend/internal/domain"
	"github.com/dwts/backend/internal/store"
)

// ErrNotImplemented is returned by the fixture for queries that need the real DB (A2/A4).
var ErrNotImplemented = fmt.Errorf("fixture: needs the database: %w", domain.ErrNotImplemented)

// IncidentsFile is the JSON layout of testdata/incidents_fixture.json.
type IncidentsFile struct {
	Incidents []domain.Incident `json:"incidents"`
}

// Incidents is a DEV-ONLY store.IncidentReader over a handful of incidents (env INCIDENTS_FIXTURE).
// Only GetIncident and FindBySource work; list/search/grid need the DB.
type Incidents struct {
	byID     map[int64]domain.Incident
	bySource map[string]int64
}

var _ store.IncidentReader = (*Incidents)(nil)

// LoadIncidents reads an incidents fixture file.
func LoadIncidents(path string) (*Incidents, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("incidents fixture: %w", err)
	}
	var f IncidentsFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("incidents fixture %s: %w", path, err)
	}
	return NewIncidents(f.Incidents)
}

// NewIncidents builds the reader from a slice (used by tests).
func NewIncidents(list []domain.Incident) (*Incidents, error) {
	r := &Incidents{byID: map[int64]domain.Incident{}, bySource: map[string]int64{}}
	for _, inc := range list {
		if inc.ID <= 0 || !inc.Type.Valid() || inc.StartTime.IsZero() {
			return nil, fmt.Errorf("incidents fixture: invalid incident %d (%s)", inc.ID, inc.Title)
		}
		if _, dup := r.byID[inc.ID]; dup {
			return nil, fmt.Errorf("incidents fixture: duplicate id %d", inc.ID)
		}
		if inc.Countries == nil {
			inc.Countries = []string{}
		}
		r.byID[inc.ID] = inc
		r.bySource[inc.Source+"/"+inc.SourceID] = inc.ID
	}
	return r, nil
}

// GetIncident returns one incident or domain.ErrNotFound.
func (r *Incidents) GetIncident(_ context.Context, id int64) (domain.Incident, error) {
	inc, ok := r.byID[id]
	if !ok {
		return domain.Incident{}, fmt.Errorf("incident %d: %w", id, domain.ErrNotFound)
	}
	return inc, nil
}

// FindBySource maps (source, sourceId) to our id.
func (r *Incidents) FindBySource(_ context.Context, source, sourceID string) (int64, error) {
	if id, ok := r.bySource[source+"/"+sourceID]; ok {
		return id, nil
	}
	return 0, fmt.Errorf("%s/%s: %w", source, sourceID, domain.ErrNotFound)
}

// ListIncidents needs the DB (A4).
func (r *Incidents) ListIncidents(context.Context, domain.IncidentFilter) ([]domain.IncidentSummary, string, error) {
	return nil, "", ErrNotImplemented
}

// Search needs the DB (A4).
func (r *Incidents) Search(context.Context, domain.SearchQuery) ([]domain.IncidentSummary, error) {
	return nil, ErrNotImplemented
}

// Grid needs the DB (A8).
func (r *Incidents) Grid(context.Context, domain.GridQuery) ([]domain.GridCell, error) {
	return nil, ErrNotImplemented
}

// Len returns the number of incidents.
func (r *Incidents) Len() int { return len(r.byID) }
