package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dwts/backend/internal/config"
)

// storiesCfg loads a config with no database whose stories dir is dir.
func storiesCfg(dir string) func() (config.Config, error) {
	return func() (config.Config, error) {
		return config.Config{LogLevel: "info", LogFormat: "text", StoriesDir: dir}, nil
	}
}

// copyStories copies the committed data/stories (manifest and small images)
// into a temp dir so a test can break it.
func copyStories(t *testing.T) string {
	t.Helper()
	src := filepath.Join("..", "..", "data", "stories")
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

func TestStoriesValidate(t *testing.T) {
	t.Run("committed stories are valid without a database", func(t *testing.T) {
		var stderr bytes.Buffer
		code := run(context.Background(), []string{"stories-validate"}, &stderr,
			storiesCfg(filepath.Join("..", "..", "data", "stories")))
		if code != 0 {
			t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
		}
		if !strings.Contains(stderr.String(), "2 stories OK") {
			t.Errorf("stderr = %q, want it to contain %q", stderr.String(), "2 stories OK")
		}
	})

	t.Run("one story", func(t *testing.T) {
		dir := t.TempDir()
		const doc = `{"version":1,"stories":[{"id":"only-one","type":"flood","place":"p","bbox":[0,0,2,2],
			"eventDate":"2020-01-03","beforeDate":"2020-01-01","afterDate":"2020-01-05","thumbnail":"t.jpg",
			"layers":[{"kind":"change","url":"c.png","bounds":[0,0,1,1]}],
			"explanation":{"driver":"d","polarizationNote":"p","howToRead":"h"}}]}`
		write(t, filepath.Join(dir, "manifest.json"), doc)
		write(t, filepath.Join(dir, "only-one", "t.jpg"), "x")
		write(t, filepath.Join(dir, "only-one", "c.png"), "x")
		var stderr bytes.Buffer
		if code := run(context.Background(), []string{"stories-validate"}, &stderr, storiesCfg(dir)); code != 0 {
			t.Fatalf("exit code = %d (stderr: %s)", code, stderr.String())
		}
		if !strings.Contains(stderr.String(), "1 story OK") {
			t.Errorf("stderr = %q, want %q", stderr.String(), "1 story OK")
		}
	})

	t.Run("renamed file prints the error and exits 1", func(t *testing.T) {
		dir := copyStories(t)
		if err := os.Rename(filepath.Join(dir, "demo-placeholder", "after.png"), filepath.Join(dir, "demo-placeholder", "after.png.bak")); err != nil {
			t.Fatal(err)
		}
		var stderr bytes.Buffer
		code := run(context.Background(), []string{"stories-validate"}, &stderr, storiesCfg(dir))
		if code != 1 {
			t.Fatalf("exit code = %d, want 1 (stderr: %s)", code, stderr.String())
		}
		if !strings.Contains(stderr.String(), "demo-placeholder.layers[1].url") {
			t.Errorf("stderr = %q, want it to name demo-placeholder.layers[1].url", stderr.String())
		}
		if strings.Contains(stderr.String(), "stories OK") {
			t.Errorf("stderr reports success for an invalid manifest: %q", stderr.String())
		}
	})

	t.Run("every error is printed", func(t *testing.T) {
		dir := copyStories(t)
		for _, f := range []string{"before.png", "after.png"} {
			if err := os.Remove(filepath.Join(dir, "demo-placeholder", f)); err != nil {
				t.Fatal(err)
			}
		}
		var stderr bytes.Buffer
		if code := run(context.Background(), []string{"stories-validate"}, &stderr, storiesCfg(dir)); code != 1 {
			t.Fatalf("exit code = %d, want 1", code)
		}
		for _, want := range []string{"demo-placeholder.layers[0].url", "demo-placeholder.layers[1].url"} {
			if !strings.Contains(stderr.String(), want) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
			}
		}
	})

	t.Run("missing manifest exits 1", func(t *testing.T) {
		var stderr bytes.Buffer
		code := run(context.Background(), []string{"stories-validate"}, &stderr, storiesCfg(t.TempDir()))
		if code != 1 || !strings.Contains(stderr.String(), "manifest.json") {
			t.Fatalf("exit code = %d, stderr = %q; want 1 naming manifest.json", code, stderr.String())
		}
	})

	t.Run("unknown manifest field exits 1", func(t *testing.T) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "manifest.json"), `{"version":1,"stories":[{"id":"abc","platfrom":"x"}]}`)
		var stderr bytes.Buffer
		code := run(context.Background(), []string{"stories-validate"}, &stderr, storiesCfg(dir))
		if code != 1 || !strings.Contains(stderr.String(), "platfrom") {
			t.Fatalf("exit code = %d, stderr = %q; want 1 naming platfrom", code, stderr.String())
		}
	})
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
