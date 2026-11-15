package httpapi

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dwts/backend/internal/domain"
)

// TestExamplesMatchGoTypes decodes every api/examples/*.json into its Go type with
// DisallowUnknownFields, so the mocks Abbas uses can never drift from the real DTOs.
func TestExamplesMatchGoTypes(t *testing.T) {
	targets := map[string]func() any{
		"healthz.json":               func() any { return &HealthResponse{} },
		"meta.json":                  func() any { return &domain.Meta{} },
		"incident-types.json":        func() any { return &IncidentTypesResponse{} },
		"choropleth.json":            func() any { return &domain.Choropleth{} },
		"timeseries.json":            func() any { return &domain.Timeseries{} },
		"grid.json":                  func() any { return &GridResponse{} },
		"incidents.json":             func() any { return &IncidentsResponse{} },
		"incident.json":              func() any { return &domain.Incident{} },
		"search.json":                func() any { return &SearchResponse{} },
		"sar.json":                   func() any { return &domain.SARResult{} },
		"sar-pre-sar-era.json":       func() any { return &domain.SARResult{} },
		"sar-coverage-timeline.json": func() any { return &domain.SARCoverageTimeline{} },
		"stories.json":               func() any { return &StoriesResponse{} },
		"story.json":                 func() any { return &domain.Story{} },
		"story-saocom.json":          func() any { return &domain.Story{} },
		"chat-request.json":          func() any { return &ChatRequest{} },
		"chat.json":                  func() any { return &ChatResponse{} },
		"error.json":                 func() any { return &ErrorBody{} },
	}
	dir := filepath.Join("..", "..", "api", "examples")
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no examples found in %s: %v", dir, err)
	}
	for _, f := range files {
		name := filepath.Base(f)
		mk, ok := targets[name]
		if !ok {
			t.Errorf("%s has no Go type mapping in this test", name)
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields()
		v := mk()
		if err := dec.Decode(v); err != nil {
			t.Errorf("%s does not match its Go type: %v", name, err)
		}
	}
	for name := range targets {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("missing example %s", name)
		}
	}
}

// TestOpenAPIMentionsEveryRoute is a cheap guard that the spec lists every endpoint path.
func TestOpenAPIMentionsEveryRoute(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	spec := string(b)
	for _, p := range []string{"/healthz:", "/api/v1/meta:", "/api/v1/incident-types:", "/api/v1/stats/choropleth:",
		"/api/v1/stats/timeseries:", "/api/v1/stats/grid:", "/api/v1/incidents:", "/api/v1/incidents/{id}:",
		"/api/v1/search:", "/api/v1/incidents/{id}/sar:", "/api/v1/sar/coverage-timeline:", "/api/v1/stories:",
		"/api/v1/stories/{id}:", "/media/stories/{id}/{file}:", "/api/v1/chat:"} {
		if !strings.Contains(spec, "  "+p) {
			t.Errorf("openapi.yaml has no path %s", strings.TrimSuffix(p, ":"))
		}
	}
}
