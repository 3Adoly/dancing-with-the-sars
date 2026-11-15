package service

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dwts/backend/internal/domain"
	"github.com/dwts/backend/internal/store"
)

// IncidentService serves E7–E9 (A4).
type IncidentService struct {
	r       store.IncidentReader // nil = no incident source → ErrNotReady
	stories StoryLinker          // may be nil
	avail   func(time.Time) string
}

// NewIncidentService wires the reader; stories and sar may be nil.
func NewIncidentService(r store.IncidentReader, stories StoryLinker, sar *SARService) *IncidentService {
	s := &IncidentService{r: r, stories: stories}
	if sar != nil {
		s.avail = sar.Availability
	}
	return s
}

func (s *IncidentService) ready() error {
	if s.r == nil {
		return fmt.Errorf("incidents: no data source: %w", domain.ErrNotReady)
	}
	return nil
}

// Get returns one incident enriched with sarAvailability and storyId (E8).
func (s *IncidentService) Get(ctx context.Context, id int64) (domain.Incident, error) {
	if err := s.ready(); err != nil {
		return domain.Incident{}, err
	}
	inc, err := s.r.GetIncident(ctx, id)
	if err != nil {
		return domain.Incident{}, err
	}
	if inc.Countries == nil {
		inc.Countries = []string{}
	}
	if s.avail != nil {
		inc.SARAvailability = s.avail(inc.StartTime)
	}
	if s.stories != nil {
		if sid, ok := s.stories.ForIncident(inc.ID); ok {
			inc.StoryID = sid
		}
	}
	return inc, nil
}

// List returns a keyset page (E7). Limit 1..5000 (default 500).
func (s *IncidentService) List(ctx context.Context, f domain.IncidentFilter) ([]domain.IncidentSummary, string, error) {
	if err := s.ready(); err != nil {
		return nil, "", err
	}
	if f.Limit == 0 {
		f.Limit = 500
	}
	if f.Limit < 1 || f.Limit > 5000 {
		return nil, "", fmt.Errorf("limit must be 1..5000: %w", domain.ErrInvalid)
	}
	if f.Sort == "" {
		f.Sort = "severity"
	}
	items, next, err := s.r.ListIncidents(ctx, f)
	if items == nil {
		items = []domain.IncidentSummary{}
	}
	return items, next, err
}

// Search runs the typo-tolerant search (E9). q must be 2..100 characters; limit 1..50 (default 20).
func (s *IncidentService) Search(ctx context.Context, q domain.SearchQuery) ([]domain.IncidentSummary, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	q.Q = strings.TrimSpace(q.Q)
	if n := utf8.RuneCountInString(q.Q); n < 2 || n > 100 {
		return nil, fmt.Errorf("q must be 2..100 characters: %w", domain.ErrInvalid)
	}
	if q.Limit == 0 {
		q.Limit = 20
	}
	if q.Limit < 1 || q.Limit > 50 {
		return nil, fmt.Errorf("limit must be 1..50: %w", domain.ErrInvalid)
	}
	items, err := s.r.Search(ctx, q)
	if items == nil {
		items = []domain.IncidentSummary{}
	}
	return items, err
}
