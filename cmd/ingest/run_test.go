package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/dwts/backend/internal/config"
	"github.com/dwts/backend/internal/domain"
	"github.com/dwts/backend/internal/ingest"
	"github.com/dwts/backend/internal/ingest/fetch"
	"github.com/dwts/backend/internal/ingest/sources"
)

func TestParseRunFlags(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    runOpts
		wantErr string // "" = ok; flag.ErrHelp is reported as "help"
	}{
		{"one source", []string{"--source", "usgs"}, runOpts{sources: []string{"usgs"}}, ""},
		{"list with spaces and repeats", []string{"--source", " usgs , usgs,"}, runOpts{sources: []string{"usgs"}}, ""},
		{"all", []string{"--all"}, runOpts{all: true, sources: registryNames()}, ""},
		{"years", []string{"--source", "usgs", "--from", "2000", "--to", "2010"}, runOpts{sources: []string{"usgs"}, fromYear: 2000, toYear: 2010}, ""},
		{"since days", []string{"--all", "--since", "30d"}, runOpts{all: true, sources: registryNames(), since: 30 * 24 * time.Hour}, ""},
		{"since duration", []string{"--source=usgs", "--since=36h"}, runOpts{sources: []string{"usgs"}, since: 36 * time.Hour}, ""},
		{"refresh", []string{"--source", "usgs", "--refresh"}, runOpts{sources: []string{"usgs"}, refresh: true}, ""},
		{"help", []string{"-h"}, runOpts{}, "help"},
		{"neither", nil, runOpts{}, "one of --source or --all"},
		{"both", []string{"--all", "--source", "usgs"}, runOpts{}, "one of --source or --all"},
		{"empty source list", []string{"--source", " , "}, runOpts{}, "one of --source or --all"},
		{"unknown source", []string{"--source", "usgs,nope"}, runOpts{}, `unknown source "nope"`},
		{"from after to", []string{"--all", "--from", "2010", "--to", "2000"}, runOpts{}, "--from 2010 is after --to 2000"},
		{"year out of range", []string{"--all", "--from", "0"}, runOpts{}, "--from 0: out of range"},
		{"since with from", []string{"--all", "--since", "30d", "--from", "2000"}, runOpts{}, "--since cannot be combined"},
		{"bad since", []string{"--all", "--since", "soon"}, runOpts{}, `--since "soon"`},
		{"zero since", []string{"--all", "--since", "0d"}, runOpts{}, `--since "0d"`},
		{"extra argument", []string{"--all", "usgs"}, runOpts{}, `unexpected arguments ["usgs"]`},
		{"bogus flag", []string{"--bogus"}, runOpts{}, "flag provided but not defined"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			got, err := parseRunFlags(tc.args, &stderr)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("err = %v (stderr %s)", err, stderr.String())
				}
				if !sameOpts(got, tc.want) {
					t.Errorf("opts = %+v, want %+v", got, tc.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("no error, opts %+v", got)
			}
			if tc.wantErr == "help" {
				if !strings.Contains(stderr.String(), "usage: ingest run") {
					t.Errorf("help output = %q", stderr.String())
				}
				return
			}
			if !strings.Contains(stderr.String(), tc.wantErr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tc.wantErr)
			}
		})
	}
}

func sameOpts(a, b runOpts) bool {
	return strings.Join(a.sources, ",") == strings.Join(b.sources, ",") && a.all == b.all &&
		a.fromYear == b.fromYear && a.toYear == b.toYear && a.since == b.since && a.refresh == b.refresh
}

func TestRunWindow(t *testing.T) {
	now := time.Date(2026, 11, 15, 10, 30, 0, 0, time.UTC)
	tests := []struct {
		name     string
		opts     runOpts
		from, to time.Time
	}{
		{"default: MIN_YEAR to now", runOpts{}, time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC), now},
		{"years inclusive", runOpts{fromYear: 2000, toYear: 2010}, time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2011, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"to current year is capped at now", runOpts{fromYear: 2020, toYear: 2026}, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), now},
		{"only from", runOpts{fromYear: 2025}, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), now},
		{"only to", runOpts{toYear: 1950}, time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(1951, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"from before MIN_YEAR is raised", runOpts{fromYear: 1800}, time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC), now},
		{"since", runOpts{since: 30 * 24 * time.Hour}, now.Add(-30 * 24 * time.Hour), now},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := runWindow(tc.opts, 1900, now)
			if !w.From.Equal(tc.from) || !w.To.Equal(tc.to) {
				t.Errorf("window = %v..%v, want %v..%v", w.From, w.To, tc.from, tc.to)
			}
		})
	}
}

// fakeRunner records the sources it ran and fails the ones in fail.
type fakeRunner struct {
	ran  []string
	fail map[string]bool
}

func (r *fakeRunner) Run(_ context.Context, s ingest.Source, _ ingest.Window) (int, error) {
	r.ran = append(r.ran, s.Name())
	if r.fail[s.Name()] {
		return 0, errors.New(s.Name() + " exploded")
	}
	return 1, nil
}

type namedSource string

func (n namedSource) Name() string { return string(n) }
func (n namedSource) Fetch(context.Context, ingest.Window, func(domain.Incident) error) error {
	return nil
}

func buildNamed(name string) ingest.Source { return namedSource(name) }

func TestRunSourcesPolicy(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := ingest.Window{}
	names := []string{"a", "b", "c"}

	// --all: a failed source is skipped, the rest still run, no error.
	r := &fakeRunner{fail: map[string]bool{"b": true}}
	if err := runSources(context.Background(), names, true, buildNamed, r, w, log); err != nil {
		t.Errorf("--all with one failure: err = %v, want nil", err)
	}
	if strings.Join(r.ran, ",") != "a,b,c" {
		t.Errorf("--all ran %v", r.ran)
	}

	// --all: every source failed is an error.
	r = &fakeRunner{fail: map[string]bool{"a": true, "b": true, "c": true}}
	if err := runSources(context.Background(), names, true, buildNamed, r, w, log); err == nil || !strings.Contains(err.Error(), "all 3 sources failed") {
		t.Errorf("--all all failed: err = %v", err)
	}

	// --source: the first failure stops the run and is returned.
	r = &fakeRunner{fail: map[string]bool{"b": true}}
	err := runSources(context.Background(), names, false, buildNamed, r, w, log)
	if err == nil || !strings.Contains(err.Error(), "b exploded") {
		t.Errorf("--source failure: err = %v", err)
	}
	if strings.Join(r.ran, ",") != "a,b" {
		t.Errorf("--source ran %v, want a,b", r.ran)
	}

	// A cancelled context stops --all too, instead of logging every source as failed.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r = &fakeRunner{}
	if err := runSources(ctx, names, true, buildNamed, r, w, log); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: err = %v", err)
	}
	if len(r.ran) != 0 {
		t.Errorf("cancelled run still ran %v", r.ran)
	}
}

func TestRegistryNamesMatchSources(t *testing.T) {
	if len(registry) == 0 {
		t.Fatal("empty registry")
	}
	deps := sourceDeps{F: fetch.New(nil, t.TempDir(), nil), Cfg: config.Config{USGSMinMag: 4.5}, Log: slog.Default()}
	for name, build := range registry {
		if got := build(deps).Name(); got != name {
			t.Errorf("registry[%q] builds a source named %q", name, got)
		}
	}
	for _, n := range []string{"usgs", "noaa_eq", "noaa_ts", "noaa_vo", "ibtracs", "eonet", "glc", "dfo", "gdacs", "gvp"} {
		if _, ok := registry[n]; !ok {
			t.Errorf("%s is not registered", n)
		}
	}
}

// The glc entry hands the adapter the ISO2 map cmdRun loaded from countries.
func TestRegistryGLCGetsISO2Map(t *testing.T) {
	deps := sourceDeps{F: fetch.New(nil, t.TempDir(), nil), Log: slog.Default(), ISO2: map[string]string{"FR": "FRA"}}
	g, ok := registry["glc"](deps).(*sources.GLC)
	if !ok || g.ISO2["FR"] != "FRA" {
		t.Errorf("glc adapter = %T, ISO2 not passed through", registry["glc"](deps))
	}
}

// The dfo entry hands the adapter the name map cmdRun loaded from countries.
func TestRegistryDFOGetsNameMap(t *testing.T) {
	deps := sourceDeps{F: fetch.New(nil, t.TempDir(), nil), Log: slog.Default(), Names: map[string]string{"France": "FRA"}}
	g, ok := registry["dfo"](deps).(*sources.DFO)
	if !ok || g.Names["France"] != "FRA" {
		t.Errorf("dfo adapter = %T, Names not passed through", registry["dfo"](deps))
	}
}
