package service

import (
	"context"
	"errors"
	"testing"

	"github.com/dwts/backend/internal/domain"
)

func TestIncidentServiceValidationAndEnrichment(t *testing.T) {
	ctx := context.Background()
	sar := newSAR(t, realASF(t), nil)
	s := NewIncidentService(fixtureIncidents, nil, sar)
	inc, err := s.Get(ctx, 118233)
	if err != nil || inc.SARAvailability != "pre_sar_era" || inc.Countries == nil {
		t.Errorf("get: %+v %v", inc, err)
	}
	if inc, _ := s.Get(ctx, 500812); inc.SARAvailability != "searchable" {
		t.Errorf("availability %q", inc.SARAvailability)
	}
	if _, _, err := s.List(ctx, domain.IncidentFilter{Limit: 6000}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("limit: %v", err)
	}
	if items, _, err := s.List(ctx, domain.IncidentFilter{}); err != nil || items == nil {
		t.Errorf("list default: %v %v", items, err)
	}
	for _, q := range []string{"k", " ", string(make([]rune, 101))} {
		if _, err := s.Search(ctx, domain.SearchQuery{Q: q}); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("q %q: %v", q, err)
		}
	}
	none := NewIncidentService(nil, nil, nil)
	if _, err := none.Get(ctx, 1); !errors.Is(err, domain.ErrNotReady) {
		t.Errorf("no reader: %v", err)
	}
}
