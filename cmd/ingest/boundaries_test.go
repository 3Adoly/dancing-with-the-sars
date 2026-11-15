package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dwts/backend/internal/config"
	"github.com/dwts/backend/internal/platform"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestEnsureBoundaries(t *testing.T) {
	const body = `{"type":"FeatureCollection","features":[]}`
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/ok":
			if ua := r.Header.Get("User-Agent"); ua != platform.UserAgent {
				t.Errorf("User-Agent = %q, want %q", ua, platform.UserAgent)
			}
			io.WriteString(w, body)
		case "/empty":
		case "/short":
			w.Header().Set("Content-Length", "100")
			io.WriteString(w, "partial")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	client := platform.NewHTTPClient(10 * time.Second)
	ctx := context.Background()

	t.Run("downloads into a missing directory", func(t *testing.T) {
		hits.Store(0)
		path := filepath.Join(t.TempDir(), "nested", "dir", "ne.geojson")
		if err := ensureBoundaries(ctx, client, srv.URL+"/ok", path, quiet); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != body {
			t.Fatalf("file = %q, %v, want the response body", got, err)
		}
		if st, _ := os.Stat(path); st.Mode().Perm() != 0o644 {
			t.Errorf("mode = %v, want 0644", st.Mode().Perm())
		}
		if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
			t.Errorf("directory has %d entries, want only the final file", len(entries))
		}
	})

	t.Run("keeps an existing file", func(t *testing.T) {
		hits.Store(0)
		path := filepath.Join(t.TempDir(), "ne.geojson")
		if err := os.WriteFile(path, []byte("mine"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := ensureBoundaries(ctx, client, srv.URL+"/ok", path, quiet); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(path); string(got) != "mine" {
			t.Errorf("existing file was overwritten: %q", got)
		}
		if hits.Load() != 0 {
			t.Errorf("made %d requests, want none when the file exists", hits.Load())
		}
	})

	t.Run("replaces an empty file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "ne.geojson")
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := ensureBoundaries(ctx, client, srv.URL+"/ok", path, quiet); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(path); string(got) != body {
			t.Errorf("file = %q, want the downloaded body", got)
		}
	})

	for _, tc := range []struct{ name, path, want string }{
		{"http error", "/missing", "404"},
		{"empty response", "/empty", "empty response"},
		{"truncated body", "/short", "unexpected EOF"},
	} {
		t.Run("leaves nothing behind on "+tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "ne.geojson")
			err := ensureBoundaries(ctx, client, srv.URL+tc.path, path, quiet)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 0 {
				t.Errorf("directory still has %d entries after a failed download", len(entries))
			}
		})
	}
}

func TestCmdBoundariesChecksConfigBeforeDownloading(t *testing.T) {
	// A missing DATABASE_URL must fail before any network or disk work.
	path := filepath.Join(t.TempDir(), "never.geojson")
	err := cmdBoundaries(context.Background(), config.Config{BoundariesPath: path})
	if err != errNoDatabase {
		t.Fatalf("err = %v, want errNoDatabase", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("a file was created at %s", path)
	}
}
