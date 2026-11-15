package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/dwts/backend/internal/httpapi"
	"github.com/dwts/backend/internal/service"
	"github.com/dwts/backend/internal/store/fixture"
)

// TestFixtureEndToEnd wires the real StatsService to the committed dev fixture and checks that
// E2–E5 answer 200 with exactly the top-level keys of api/examples.
func TestFixtureEndToEnd(t *testing.T) {
	r, err := fixture.Load(filepath.Join("..", "..", "testdata", "stats_fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	stats := service.NewStatsService(r, nil, 1900, map[string]bool{"chat": false}, log)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := stats.Start(ctx, 0); err != nil {
		t.Fatal(err)
	}
	h := httpapi.NewRouter(httpapi.Deps{Log: log, Stats: stats})

	cases := []struct{ path, example string }{
		{"/api/v1/meta", "meta.json"},
		{"/api/v1/incident-types", "incident-types.json"},
		{"/api/v1/stats/choropleth?types=earthquake,flood&from=1950&to=2000", "choropleth.json"},
		{"/api/v1/stats/timeseries?types=storm&from=1980&to=1983&iso3=PHL", "timeseries.json"},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.path, nil))
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", c.path, rec.Code, rec.Body)
		}
		ex, err := os.ReadFile(filepath.Join("..", "..", "api", "examples", c.example))
		if err != nil {
			t.Fatal(err)
		}
		if got, want := keys(t, rec.Body.Bytes()), keys(t, ex); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: keys %v want %v", c.path, got, want)
		}
	}

	// Plausibility: Japan and Indonesia lead earthquakes, the Philippines storms.
	var ch struct {
		Counts map[string]int64 `json:"counts"`
		Ocean  int64            `json:"ocean"`
		Total  int64            `json:"total"`
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/stats/choropleth?types=earthquake&from=1950&to=2025", nil))
	_ = json.Unmarshal(rec.Body.Bytes(), &ch)
	if ch.Counts["IDN"] < ch.Counts["USA"] || ch.Counts["JPN"] < ch.Counts["USA"] || ch.Ocean == 0 || ch.Total == 0 {
		t.Errorf("implausible earthquake choropleth: %+v", ch)
	}
}

func keys(t *testing.T, b []byte) []string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	var k []string
	for x := range m {
		k = append(k, x)
	}
	slices.Sort(k)
	return k
}
