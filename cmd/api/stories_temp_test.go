package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTempStoryLoaderDevManifest(t *testing.T) {
	st, err := tempStoryLoader(filepath.Join("..", "..", "testdata", "stories"))()
	if err != nil {
		t.Fatal(err)
	}
	if len(st) != 2 || st[0].ID != "aomori-2025-nisar" || st[1].ID != "demo-two-sensors" {
		t.Fatalf("stories %+v", st)
	}
	if st[0].Thumbnail != "/media/stories/aomori-2025-nisar/thumb.jpg" || st[0].Layers[2].URL != "/media/stories/aomori-2025-nisar/change_logratio.png" {
		t.Errorf("urls not rewritten: %q %q", st[0].Thumbnail, st[0].Layers[2].URL)
	}
	if st[1].Layers[0].Platform != "SAOCOM-1A" || st[1].Layers[1].Platform != "NISAR" || !strings.HasPrefix(st[1].Credits[0], "Producto SAOCOM® - ©CONAE") {
		t.Errorf("two-sensor story: %+v", st[1].Layers)
	}
}

func writeManifest(t *testing.T, dir, body string, files map[string]string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	for p, c := range files {
		fp := filepath.Join(dir, filepath.FromSlash(p))
		_ = os.MkdirAll(filepath.Dir(fp), 0o755)
		if err := os.WriteFile(fp, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

const storyTmpl = `{"id":"%s","order":%s,"title":"t","type":"flood","thumbnail":"%s","layers":[{"kind":"change","url":"%s","bounds":[0,0,1,1]}]}`

func story(id, order, thumb, layer string) string {
	return fmt.Sprintf(storyTmpl, id, order, thumb, layer)
}

func TestTempStoryLoaderErrors(t *testing.T) {
	cases := []struct {
		name    string
		stories string
		files   map[string]string
		wantOK  int
		wantErr string
	}{
		{"ok sorted", story("bbb", "2", "t.jpg", "c.png") + "," + story("aaa", "1", "t.jpg", "c.png"),
			map[string]string{"aaa/t.jpg": "x", "aaa/c.png": "x", "bbb/t.jpg": "x", "bbb/c.png": "x"}, 2, ""},
		{"duplicate id", story("aaa", "1", "t.jpg", "c.png") + "," + story("aaa", "2", "t.jpg", "c.png"),
			map[string]string{"aaa/t.jpg": "x", "aaa/c.png": "x"}, 1, "duplicate id"},
		{"missing file", story("aaa", "1", "t.jpg", "missing.png"),
			map[string]string{"aaa/t.jpg": "x"}, 0, "missing file"},
		{"traversal name", story("aaa", "1", "t.jpg", "../../secret.png"),
			map[string]string{"aaa/t.jpg": "x"}, 0, "unsafe file name"},
		{"bad id", story("A B", "1", "t.jpg", "c.png"), nil, 0, "invalid id"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			writeManifest(t, dir, `{"version":1,"stories":[`+c.stories+`]}`, c.files)
			st, err := tempStoryLoader(dir)()
			if len(st) != c.wantOK {
				t.Errorf("got %d stories want %d", len(st), c.wantOK)
			}
			if c.wantErr == "" && err != nil || c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)) {
				t.Errorf("err %v want %q", err, c.wantErr)
			}
			if c.name == "ok sorted" && st[0].ID != "aaa" {
				t.Errorf("not sorted by order: %s", st[0].ID)
			}
		})
	}
	if _, err := tempStoryLoader(t.TempDir())(); err == nil {
		t.Error("missing manifest must fail")
	}
}
