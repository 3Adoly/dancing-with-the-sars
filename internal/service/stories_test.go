package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/dwts/backend/internal/domain"
)

// storyDir creates <tmp>/stories/<id>/{a.png,b.json,c.txt,sub/} and <tmp>/secret.png outside the root.
func storyDir(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "stories")
	for _, p := range []string{"demo-one/a.png", "demo-one/b.json", "demo-one/c.txt", "demo-two/x.jpg"} {
		fp := filepath.Join(root, filepath.FromSlash(p))
		_ = os.MkdirAll(filepath.Dir(fp), 0o755)
		if err := os.WriteFile(fp, []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.MkdirAll(filepath.Join(root, "demo-one", "sub.png"), 0o755) // a directory with an allowed extension
	_ = os.WriteFile(filepath.Join(base, "secret.png"), []byte("secret"), 0o644)
	return root
}

func testStories() []domain.Story {
	id := int64(42)
	return []domain.Story{
		{ID: "demo-two", Title: "two", Order: 5, Type: domain.Flood},
		{ID: "demo-one", Title: "one", Order: 1, Type: domain.Earthquake, IncidentSource: "usgs", IncidentSourceID: "us6000jllz"},
		{ID: "demo-linked", Title: "explicit", Order: 3, Type: domain.Volcano, IncidentID: &id},
	}
}

type findStub struct{ incStub }

func (findStub) FindBySource(_ context.Context, src, sid string) (int64, error) {
	if src == "usgs" && sid == "us6000jllz" {
		return 500812, nil
	}
	return 0, domain.ErrNotFound
}

func newStorySvc(t *testing.T, root string) *StoryService {
	t.Helper()
	s, err := NewStoryService(context.Background(), func() ([]domain.Story, error) { return testStories(), nil }, root, findStub{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStoryServiceListGetLink(t *testing.T) {
	s := newStorySvc(t, storyDir(t))
	l := s.List()
	if len(l) != 3 || l[0].ID != "demo-one" || l[1].ID != "demo-linked" || l[2].ID != "demo-two" {
		t.Fatalf("order: %+v", l)
	}
	if st, err := s.Get("demo-one"); err != nil || st.IncidentID == nil || *st.IncidentID != 500812 {
		t.Errorf("FindBySource link: %+v %v", st.IncidentID, err)
	}
	if _, err := s.Get("nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get unknown: %v", err)
	}
	if id, ok := s.ForIncident(500812); !ok || id != "demo-one" {
		t.Errorf("ForIncident 500812: %q %v", id, ok)
	}
	if id, ok := s.ForIncident(42); !ok || id != "demo-linked" {
		t.Errorf("ForIncident 42: %q %v", id, ok)
	}
	if _, ok := s.ForIncident(7); ok {
		t.Error("unlinked incident")
	}
	if s.ETag() == "" || s.Len() != 3 {
		t.Error("etag/len")
	}
}

func TestStoryServiceLoaderErrorStillUsable(t *testing.T) {
	s, err := NewStoryService(context.Background(), func() ([]domain.Story, error) {
		return testStories()[:1], errors.New("one story invalid")
	}, t.TempDir(), nil, nil)
	if err == nil || s == nil || s.Len() != 1 {
		t.Fatalf("partial load: %v %v", s, err)
	}
	if st, _ := s.Get("demo-two"); st.IncidentID != nil {
		t.Error("no reader → no link")
	}
}

func TestMediaPath(t *testing.T) {
	root := storyDir(t)
	s := newStorySvc(t, root)
	if p, ct, err := s.MediaPath("demo-one", "a.png"); err != nil || ct != "image/png" || filepath.Base(p) != "a.png" {
		t.Fatalf("good: %q %q %v", p, ct, err)
	}
	if _, ct, err := s.MediaPath("demo-one", "b.json"); err != nil || ct != "application/json" {
		t.Errorf("json: %q %v", ct, err)
	}
	bad := [][2]string{
		{"demo-one", "c.txt"},              // extension not allowed
		{"demo-one", "missing.png"},        // not found
		{"demo-one", "sub.png"},            // directory
		{"demo-one", "../secret.png"},      // traversal
		{"demo-one", `..\secret.png`},      // windows traversal
		{"demo-one", "..%2fsecret.png"},    // still-encoded attempt
		{"demo-one", "/etc/passwd.png"},    // absolute
		{"demo-one", `C:\Windows\win.png`}, // drive letter
		{"demo-one", ".hidden.png"},        // dotfile
		{"demo-one", ""},                   // empty
		{"..", "secret.png"},               // id traversal
		{"demo-xyz", "a.png"},              // unknown story (dir may exist or not)
		{"demo-two", "../demo-one/a.png"},  // cross-story
		{"DEMO-ONE", "a.png"},              // id must be lower-case
	}
	for _, b := range bad {
		if _, _, err := s.MediaPath(b[0], b[1]); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("MediaPath(%q,%q) must be not found, got %v", b[0], b[1], err)
		}
	}
	// Symlink escaping the root (skipped where symlinks need privileges, e.g. Windows without dev mode).
	link := filepath.Join(root, "demo-one", "escape.png")
	if err := os.Symlink(filepath.Join(filepath.Dir(root), "secret.png"), link); err == nil {
		if _, _, err := s.MediaPath("demo-one", "escape.png"); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("symlink escape must be refused, got %v", err)
		}
	} else {
		t.Logf("symlink test skipped: %v", err)
	}
}

func TestSARResultCarriesStoryID(t *testing.T) {
	stories := newStorySvc(t, storyDir(t)) // links demo-one → 500812 (Türkiye)
	s := NewSARService(fixtureIncidents, nil, realASF(t), nil, stories, SARConfig{})
	s.missions = nil // no missions → era path, no ASF call
	r, err := s.ForIncident(context.Background(), 500812, false)
	if err != nil || r.StoryID != "demo-one" {
		t.Errorf("storyId: %q %v", r.StoryID, err)
	}
	s2 := newSAR(t, realASF(t), nil)
	s2.stories = stories
	r2, err := s2.ForIncident(context.Background(), 500812, false)
	if err != nil || r2.StoryID != "demo-one" || r2.Coverage != domain.SARCoverageOK {
		t.Errorf("storyId with scenes: %q %s %v", r2.StoryID, r2.Coverage, err)
	}
}
