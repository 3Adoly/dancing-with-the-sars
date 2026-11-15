package postgres

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dwts/backend/internal/domain"
)

func TestBuildListQuery(t *testing.T) {
	mag := float32(6)
	sql, args, err := buildListQuery(domain.IncidentFilter{
		Types: []domain.IncidentType{domain.Earthquake, domain.FireActivity}, Years: domain.YearRange{From: 1950, To: 2000},
		BBox: &domain.BBox{MinLon: 120, MinLat: 20, MaxLon: 150, MaxLat: 50}, ISO3: "jpn", MinMagnitude: &mag,
		MinSeverity: 2, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"i.dup_of IS NULL", "i.type = ANY($1)", "i.year BETWEEN $2 AND $3", "ST_MakeEnvelope($4, $5, $6, $7, 4326)",
		"i.primary_iso3 = $8", "ic.iso3 = $8", "i.magnitude >= $9", "i.severity >= $10",
		"ORDER BY i.severity DESC, coalesce(i.magnitude, -1)::float8 DESC, i.id DESC", "LIMIT $11"} {
		if !strings.Contains(sql, want) {
			t.Errorf("missing %q in\n%s", want, sql)
		}
	}
	if ts := args[0].([]string); len(ts) != 1 || ts[0] != "earthquake" || args[7] != "JPN" || args[10] != 11 {
		t.Errorf("args %v", args)
	}
}

func TestBuildListQueryAntimeridianAndCursor(t *testing.T) {
	sql, _, _ := buildListQuery(domain.IncidentFilter{BBox: &domain.BBox{MinLon: 170, MinLat: -10, MaxLon: -170, MaxLat: 10}})
	if strings.Count(sql, "ST_MakeEnvelope") != 2 || !strings.Contains(sql, " OR ") {
		t.Errorf("antimeridian: %s", sql)
	}
	s, m := int16(3), 6.0
	sql, args, err := buildListQuery(domain.IncidentFilter{Cursor: domain.EncodeCursor(domain.Cursor{S: &s, M: &m, I: 99})})
	if err != nil || !strings.Contains(sql, "(i.severity, coalesce(i.magnitude, -1)::float8, i.id) < ($1, $2, $3)") || args[2] != int64(99) {
		t.Errorf("severity cursor: %v %s %v", err, sql, args)
	}
	ts := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	sql, _, err = buildListQuery(domain.IncidentFilter{Sort: "time", Cursor: domain.EncodeCursor(domain.Cursor{T: &ts, I: 5})})
	if err != nil || !strings.Contains(sql, "(i.start_time, i.id) < ($1, $2)") || !strings.Contains(sql, "ORDER BY i.start_time DESC, i.id DESC") {
		t.Errorf("time cursor: %v %s", err, sql)
	}
	// A severity cursor used with sort=time is rejected.
	if _, _, err := buildListQuery(domain.IncidentFilter{Sort: "time", Cursor: domain.EncodeCursor(domain.Cursor{S: &s, M: &m, I: 1})}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("mismatched cursor: %v", err)
	}
	sql, _, _ = buildListQuery(domain.IncidentFilter{Types: []domain.IncidentType{domain.FireActivity}})
	if !strings.Contains(sql, "false") {
		t.Errorf("fire_activity only must match nothing: %s", sql)
	}
}

func TestBuildSearchQuery(t *testing.T) {
	sql, args := buildSearchQuery(domain.SearchQuery{Q: "  Tōhoku_50% ", Types: []domain.IncidentType{domain.Earthquake},
		Years: &domain.YearRange{From: 2000, To: 2020}, Limit: 5})
	if args[0] != "Tōhoku_50%" || args[1] != "tohoku_50%" || args[2] != `%tohoku\_50\%%` {
		t.Errorf("args %q", args[:3])
	}
	for _, want := range []string{"i.source_id = $1", "$2 <% i.search_text", "i.search_text LIKE $3", "i.type = ANY($4)",
		"i.year BETWEEN $5 AND $6", "word_similarity($2, i.search_text)", "LIMIT $7", "i.dup_of IS NULL"} {
		if !strings.Contains(sql, want) {
			t.Errorf("missing %q in\n%s", want, sql)
		}
	}
}
