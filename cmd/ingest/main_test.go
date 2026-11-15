package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dwts/backend/internal/config"
)

// noDB is a config loader that never reads the ambient environment.
func noDB() (config.Config, error) {
	return config.Config{LogLevel: "info", LogFormat: "text"}, nil
}

func TestRunDispatch(t *testing.T) {
	failLoad := func() (config.Config, error) {
		t.Error("config must not be loaded for usage errors or unimplemented commands")
		return config.Config{}, errors.New("unexpected load")
	}
	tests := []struct {
		name     string
		args     []string
		load     func() (config.Config, error)
		wantCode int
		wantOut  string
	}{
		{"no args", nil, failLoad, 2, "usage: ingest <command>"},
		{"help", []string{"help"}, failLoad, 0, "usage: ingest <command>"},
		{"unknown", []string{"frobnicate"}, failLoad, 2, `unknown command "frobnicate"`},
		{"run -h", []string{"run", "-h"}, failLoad, 0, "usage: ingest run"},
		{"run without --source or --all", []string{"run"}, failLoad, 2, "one of --source or --all"},
		{"run unknown source", []string{"run", "--source", "nope"}, failLoad, 2, `unknown source "nope"`},
		{"run without DATABASE_URL", []string{"run", "--source", "usgs"}, noDB, 1, "DATABASE_URL is not set"},
		{"postprocess -h", []string{"postprocess", "-h"}, failLoad, 0, "usage: ingest postprocess"},
		{"postprocess bogus flag", []string{"postprocess", "--bogus"}, failLoad, 2, "flag provided but not defined: -bogus"},
		{"postprocess extra argument", []string{"postprocess", "extra"}, failLoad, 2, `ingest postprocess: unexpected arguments ["extra"]`},
		{"postprocess without DATABASE_URL", []string{"postprocess"}, noDB, 1, "DATABASE_URL is not set"},
		{"stories-validate -h", []string{"stories-validate", "-h"}, failLoad, 0, "usage: ingest stories-validate"},
		{"stories-validate bogus flag", []string{"stories-validate", "--bogus"}, failLoad, 2, "flag provided but not defined: -bogus"},
		{"stories-validate extra argument", []string{"stories-validate", "extra"}, failLoad, 2, `ingest stories-validate: unexpected arguments ["extra"]`},
		{"schedule", []string{"schedule"}, failLoad, 2, "ingest schedule: not implemented"},
		{"boundaries -h", []string{"boundaries", "-h"}, failLoad, 0, "usage: ingest boundaries"},
		{"boundaries bogus flag", []string{"boundaries", "--bogus"}, failLoad, 2, "flag provided but not defined: -bogus"},
		{"boundaries extra argument", []string{"boundaries", "extra"}, failLoad, 2, `ingest boundaries: unexpected arguments ["extra"]`},
		{"boundaries without DATABASE_URL", []string{"boundaries"}, noDB, 1, "DATABASE_URL is not set"},
		{"migrate -h", []string{"migrate", "-h"}, failLoad, 0, "usage: ingest migrate"},
		{"migrate --help", []string{"migrate", "--help"}, failLoad, 0, "usage: ingest migrate"},
		{"migrate bogus flag", []string{"migrate", "--bogus"}, failLoad, 2, "flag provided but not defined: -bogus"},
		{"migrate extra argument", []string{"migrate", "extra"}, failLoad, 2, `ingest migrate: unexpected arguments ["extra"]`},
		{"migrate without DATABASE_URL", []string{"migrate"}, noDB, 1, "DATABASE_URL is not set"},
		{"config error", []string{"migrate"}, func() (config.Config, error) {
			return config.Config{}, errors.New("config: boom")
		}, 1, "config: boom"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			code := run(context.Background(), tc.args, &stderr, tc.load)
			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d (stderr: %s)", code, tc.wantCode, stderr.String())
			}
			if !strings.Contains(stderr.String(), tc.wantOut) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tc.wantOut)
			}
		})
	}
}

func TestCommandsListed(t *testing.T) {
	want := []string{"migrate", "boundaries", "run", "postprocess", "stories-validate", "schedule"}
	if len(commands) != len(want) {
		t.Fatalf("commands = %d entries, want %d", len(commands), len(want))
	}
	for i, name := range want {
		if commands[i].name != name {
			t.Errorf("commands[%d] = %q, want %q", i, commands[i].name, name)
		}
		if !strings.Contains(usage, name) {
			t.Errorf("usage does not mention %q", name)
		}
	}
}
