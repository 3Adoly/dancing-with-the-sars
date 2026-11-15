package stories

import (
	"bytes"
	"encoding/json"
	"errors"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dwts/backend/internal/domain"
)

const (
	baseID = "demo-test"
	// repoStories is the committed placeholder stories directory (real stories in data/stories are checked by `ingest stories-validate`), relative to this package.
	repoStories = "../../testdata/stories_validate"
)

var bboxAll = [4]float64{30, 36, 38, 38}

// baseStory is a story that passes every rule when its files exist (see writeStoryFiles).
func baseStory() domain.Story {
	return domain.Story{
		ID:         baseID,
		Order:      1,
		Title:      "Test story",
		Summary:    "A story used by the tests.",
		Type:       domain.Flood,
		Place:      "Somewhere",
		BBox:       bboxAll,
		Center:     [2]float64{34, 37},
		Zoom:       6,
		EventDate:  "2023-02-06",
		BeforeDate: "2023-01-25",
		AfterDate:  "2023-02-18",
		Platform:   "Sentinel-1", Band: "C", Polarization: "VV",
		Product:   "Sentinel-1 RTC (ASF HyP3)",
		Thumbnail: "thumb.jpg",
		Layers: []domain.StoryLayer{
			{Kind: "before", URL: "before.png", Bounds: bboxAll, Date: "2023-01-25"},
			{Kind: "after", URL: "after.png", Bounds: bboxAll, Date: "2023-02-18"},
			{Kind: "change", URL: "change.png", Bounds: bboxAll},
			{Kind: "frame", URL: "frame.png", Bounds: bboxAll, Date: "2023-02-01", Label: "Feb"},
		},
		Explanation: domain.Explanation{
			Driver:           "d",
			PolarizationNote: "p",
			HowToRead:        "h",
		},
		Credits: []string{"Test credit"},
	}
}

// writeStoryFiles creates a tiny file for the thumbnail and every layer of s
// under dir/<s.ID>/. Validate never decodes the files, only stats them.
func writeStoryFiles(t *testing.T, dir string, s domain.Story) {
	t.Helper()
	sd := filepath.Join(dir, s.ID)
	if err := os.MkdirAll(sd, 0o755); err != nil {
		t.Fatal(err)
	}
	names := []string{s.Thumbnail}
	for _, l := range s.Layers {
		names = append(names, l.URL)
	}
	for _, n := range names {
		if n == "" || n == "." || strings.ContainsAny(n, "/\\\x00") || strings.Contains(n, "..") {
			continue // unsafe names are rejected by Validate before any lookup
		}
		if err := os.WriteFile(filepath.Join(sd, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func baseManifest(t *testing.T) (string, Manifest) {
	t.Helper()
	dir := t.TempDir()
	s := baseStory()
	writeStoryFiles(t, dir, s)
	return dir, Manifest{Version: 1, Stories: []domain.Story{s}}
}

// errPaths returns the "<story>.<field>" of every ValidationError, sorted.
func errPaths(t *testing.T, errs []error) []string {
	t.Helper()
	var out []string
	for _, err := range errs {
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("error %q is not a *ValidationError", err)
		}
		switch {
		case ve.Story == "":
			out = append(out, ve.Field)
		case ve.Field == "":
			out = append(out, ve.Story)
		default:
			out = append(out, ve.Story+"."+ve.Field)
		}
	}
	slices.Sort(out)
	return out
}

func TestValidateBaseIsValid(t *testing.T) {
	dir, m := baseManifest(t)
	if errs := Validate(dir, m); len(errs) != 0 {
		t.Fatalf("base story should be valid, got %v", errs)
	}
}

type ruleCase struct {
	name   string
	mutate func(m *Manifest)              // edits the manifest (Stories[0] is the base story)
	fs     func(t *testing.T, dir string) // optional file system edits, run after the files are written
	want   []string                       // exact set of expected error paths; nil = valid
	msg    string                         // optional substring one error must contain
}

func runRuleCases(t *testing.T, cases []ruleCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, m := baseManifest(t)
			if tc.mutate != nil {
				tc.mutate(&m)
			}
			// Files follow the mutated manifest so a rule is tested alone.
			for _, s := range m.Stories {
				writeStoryFiles(t, dir, s)
			}
			if tc.fs != nil {
				tc.fs(t, dir)
			}
			errs := Validate(dir, m)
			got := errPaths(t, errs)
			want := slices.Clone(tc.want)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Fatalf("error paths = %q, want %q\nerrors: %v", got, want, errs)
			}
			if tc.msg != "" {
				found := false
				for _, e := range errs {
					found = found || strings.Contains(e.Error(), tc.msg)
				}
				if !found {
					t.Errorf("no error contains %q: %v", tc.msg, errs)
				}
			}
		})
	}
}

func story0(f func(s *domain.Story)) func(m *Manifest) {
	return func(m *Manifest) { f(&m.Stories[0]) }
}

func TestValidateVersion(t *testing.T) {
	runRuleCases(t, []ruleCase{
		{"version 1", nil, nil, nil, ""},
		{"version 0", func(m *Manifest) { m.Version = 0 }, nil, []string{"manifest.version"}, "version 1"},
		{"version 2", func(m *Manifest) { m.Version = 2 }, nil, []string{"manifest.version"}, ""},
	})
}

func TestValidateID(t *testing.T) {
	long := strings.Repeat("a", 65)
	runRuleCases(t, []ruleCase{
		{"minimum length 3", story0(func(s *domain.Story) { s.ID = "abc" }), nil, nil, ""},
		{"maximum length 64", story0(func(s *domain.Story) { s.ID = strings.Repeat("a", 64) }), nil, nil, ""},
		{"digits and dashes", story0(func(s *domain.Story) { s.ID = "2022-pakistan-flood-1" }), nil, nil, ""},
		{"too short", story0(func(s *domain.Story) { s.ID = "ab" }), nil, []string{"stories[0].id"}, `"ab"`},
		{"too long", story0(func(s *domain.Story) { s.ID = long }), nil, []string{"stories[0].id"}, ""},
		{"empty", story0(func(s *domain.Story) { s.ID = "" }), nil, []string{"stories[0].id"}, ""},
		{"upper case", story0(func(s *domain.Story) { s.ID = "Demo-Test" }), nil, []string{"stories[0].id"}, ""},
		{"underscore", story0(func(s *domain.Story) { s.ID = "demo_test" }), nil, []string{"stories[0].id"}, ""},
		{"slash", story0(func(s *domain.Story) { s.ID = "a/b" }), nil, []string{"stories[0].id"}, ""},
		{"dot dot", story0(func(s *domain.Story) { s.ID = "../x" }), nil, []string{"stories[0].id"}, ""},
		{"newline suffix", story0(func(s *domain.Story) { s.ID = "demo-test\n" }), nil, []string{"stories[0].id"}, ""},
		{"duplicate", func(m *Manifest) {
			m.Stories = append(m.Stories, baseStory())
		}, nil, []string{baseID + ".id"}, "stories[0]"},
		{"two different ids", func(m *Manifest) {
			b := baseStory()
			b.ID = "demo-other"
			m.Stories = append(m.Stories, b)
		}, nil, nil, ""},
	})
}

func TestValidateType(t *testing.T) {
	runRuleCases(t, []ruleCase{
		{"flood", nil, nil, nil, ""},
		{"earthquake", story0(func(s *domain.Story) { s.Type = domain.Earthquake }), nil, nil, ""},
		{"unknown", story0(func(s *domain.Story) { s.Type = "meteor" }), nil, []string{baseID + ".type"}, `"meteor"`},
		{"empty", story0(func(s *domain.Story) { s.Type = "" }), nil, []string{baseID + ".type"}, ""},
		{"wrong case", story0(func(s *domain.Story) { s.Type = "Flood" }), nil, []string{baseID + ".type"}, ""},
	})
}

func TestValidateLayerKinds(t *testing.T) {
	layer := func(kind, url string) domain.StoryLayer {
		return domain.StoryLayer{Kind: kind, URL: url, Bounds: bboxAll}
	}
	runRuleCases(t, []ruleCase{
		{"before after only", story0(func(s *domain.Story) {
			s.Layers = []domain.StoryLayer{layer("before", "before.png"), layer("after", "after.png")}
		}), nil, nil, ""},
		{"change only", story0(func(s *domain.Story) {
			s.Layers = []domain.StoryLayer{layer("change", "change.png")}
		}), nil, nil, ""},
		{"interferogram only", story0(func(s *domain.Story) {
			s.Layers = []domain.StoryLayer{layer("interferogram", "ifg.png")}
		}), nil, nil, ""},
		{"unknown kind", story0(func(s *domain.Story) { s.Layers[2].Kind = "overlay" }), nil, []string{baseID + ".layers[2].kind"}, `"overlay"`},
		{"empty kind", story0(func(s *domain.Story) { s.Layers[0].Kind = "" }), nil, []string{baseID + ".layers[0].kind"}, ""},
		{"no layers", story0(func(s *domain.Story) { s.Layers = nil }), nil, []string{baseID + ".layers"}, "at least"},
		{"before without after", story0(func(s *domain.Story) {
			s.Layers = []domain.StoryLayer{layer("before", "before.png")}
		}), nil, []string{baseID + ".layers"}, ""},
		{"after without before", story0(func(s *domain.Story) {
			s.Layers = []domain.StoryLayer{layer("after", "after.png")}
		}), nil, []string{baseID + ".layers"}, ""},
		{"frames only", story0(func(s *domain.Story) {
			s.Layers = []domain.StoryLayer{{Kind: "frame", URL: "frame.png", Bounds: bboxAll, Date: "2023-02-01"}}
		}), nil, []string{baseID + ".layers"}, ""},
	})
}

func TestValidateFiles(t *testing.T) {
	rm := func(name string) func(t *testing.T, dir string) {
		return func(t *testing.T, dir string) {
			t.Helper()
			if err := os.Remove(filepath.Join(dir, baseID, name)); err != nil {
				t.Fatal(err)
			}
		}
	}
	write := func(name string, size int64) func(t *testing.T, dir string) {
		return func(t *testing.T, dir string) {
			t.Helper()
			f, err := os.Create(filepath.Join(dir, baseID, name))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := f.Truncate(size); err != nil { // sparse: costs no disk
				t.Fatal(err)
			}
		}
	}
	runRuleCases(t, []ruleCase{
		{"missing layer file", nil, rm("after.png"), []string{baseID + ".layers[1].url"}, "not found"},
		{"missing thumbnail file", nil, rm("thumb.jpg"), []string{baseID + ".thumbnail"}, "not found"},
		{"two missing files", nil, func(t *testing.T, dir string) {
			rm("before.png")(t, dir)
			rm("frame.png")(t, dir)
		}, []string{baseID + ".layers[0].url", baseID + ".layers[3].url"}, ""},
		{"empty layer url", story0(func(s *domain.Story) { s.Layers[0].URL = "" }), nil, []string{baseID + ".layers[0].url"}, "required"},
		{"empty thumbnail", story0(func(s *domain.Story) { s.Thumbnail = "" }), nil, []string{baseID + ".thumbnail"}, "required"},
		{"ext gif", story0(func(s *domain.Story) { s.Layers[0].URL = "before.gif" }), nil, []string{baseID + ".layers[0].url"}, ".gif"},
		{"ext upper case", story0(func(s *domain.Story) { s.Layers[0].URL = "before.PNG" }), nil, []string{baseID + ".layers[0].url"}, ".PNG"},
		{"no ext", story0(func(s *domain.Story) { s.Layers[0].URL = "before" }), nil, []string{baseID + ".layers[0].url"}, ""},
		{"thumbnail ext", story0(func(s *domain.Story) { s.Thumbnail = "thumb.bmp" }), nil, []string{baseID + ".thumbnail"}, ".bmp"},
		{"ext jpeg webp json tif", story0(func(s *domain.Story) {
			s.Layers[0].URL = "a.jpeg"
			s.Layers[1].URL = "b.webp"
			s.Layers[2].URL = "c.json"
			s.Layers[3].URL = "d.tif"
		}), nil, nil, ""},
		{"exactly 8 MB", nil, write("before.png", 8<<20), nil, ""},
		{"over 8 MB", nil, write("before.png", 8<<20+1), []string{baseID + ".layers[0].url"}, "8 MB"},
		{"thumbnail over 8 MB", nil, write("thumb.jpg", 8<<20+1), []string{baseID + ".thumbnail"}, "8 MB"},
		{"directory instead of file", nil, func(t *testing.T, dir string) {
			p := filepath.Join(dir, baseID, "before.png")
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(p, 0o755); err != nil {
				t.Fatal(err)
			}
		}, []string{baseID + ".layers[0].url"}, "regular file"},
		{"missing story dir", nil, func(t *testing.T, dir string) {
			if err := os.RemoveAll(filepath.Join(dir, baseID)); err != nil {
				t.Fatal(err)
			}
		}, []string{
			baseID + ".thumbnail",
			baseID + ".layers[0].url", baseID + ".layers[1].url", baseID + ".layers[2].url", baseID + ".layers[3].url",
		}, ""},
	})
}

func TestValidatePathTraversal(t *testing.T) {
	bad := []string{
		"../x.png", "..", "../../etc/passwd", "/etc/passwd", "/abs.png", "sub/x.png", "sub/../x.png",
		`sub\x.png`, `..\x.png`, `C:\x.png`, "a..b.png", ".", "./x.png", "x.png/", "x\x00.png",
	}
	var cases []ruleCase
	for _, name := range bad {
		cases = append(cases,
			ruleCase{"layer " + name, story0(func(s *domain.Story) { s.Layers[1].URL = name }), nil, []string{baseID + ".layers[1].url"}, "must not contain"},
			ruleCase{"thumbnail " + name, story0(func(s *domain.Story) { s.Thumbnail = name }), nil, []string{baseID + ".thumbnail"}, "must not contain"},
		)
	}
	cases = append(cases, ruleCase{
		"message names the rule", story0(func(s *domain.Story) { s.Layers[1].URL = "../x.png" }), nil,
		[]string{baseID + ".layers[1].url"}, `must not contain "/", "\" or ".."`,
	})
	runRuleCases(t, cases)
}

// A file that exists next to the manifest but outside the story dir must be
// rejected even when the name is smuggled in through a path.
func TestValidateTraversalNeverTouchesOutsideFiles(t *testing.T) {
	dir, m := baseManifest(t)
	if err := os.WriteFile(filepath.Join(dir, "secret.png"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.Stories[0].Layers[0].URL = "../secret.png"
	errs := Validate(dir, m)
	if got := errPaths(t, errs); !slices.Equal(got, []string{baseID + ".layers[0].url"}) {
		t.Fatalf("error paths = %q (%v)", got, errs)
	}
	if strings.Contains(errs[0].Error(), "not found") {
		t.Errorf("a traversal name must be rejected by name, not looked up: %v", errs[0])
	}
}

func TestValidateSymlinkEscape(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "evil.png"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(outside, "evildir"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := func(newname string, target string) func(t *testing.T, dir string) {
		return func(t *testing.T, dir string) {
			t.Helper()
			p := filepath.Join(dir, baseID, newname)
			_ = os.Remove(p)
			if err := os.Symlink(target, p); err != nil {
				t.Skipf("symlinks not supported: %v", err)
			}
		}
	}
	runRuleCases(t, []ruleCase{
		{"file symlink outside", nil, link("before.png", filepath.Join(outside, "evil.png")),
			[]string{baseID + ".layers[0].url"}, "outside"},
		{"thumbnail symlink outside", nil, link("thumb.jpg", filepath.Join(outside, "evil.png")),
			[]string{baseID + ".thumbnail"}, "outside"},
		{"relative symlink outside", nil, link("before.png", "../../../../../../../../etc/hostname"),
			[]string{baseID + ".layers[0].url"}, ""},
		{"story dir symlinked outside", nil, func(t *testing.T, dir string) {
			if err := os.RemoveAll(filepath.Join(dir, baseID)); err != nil {
				t.Fatal(err)
			}
			for _, n := range []string{"before.png", "after.png", "change.png", "frame.png", "thumb.jpg"} {
				if err := os.WriteFile(filepath.Join(outside, n), []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(outside, filepath.Join(dir, baseID)); err != nil {
				t.Skipf("symlinks not supported: %v", err)
			}
		}, []string{
			baseID + ".thumbnail",
			baseID + ".layers[0].url", baseID + ".layers[1].url", baseID + ".layers[2].url", baseID + ".layers[3].url",
		}, "outside"},
		{"symlink to a file inside the stories dir", nil, func(t *testing.T, dir string) {
			shared := filepath.Join(dir, "shared.png")
			if err := os.WriteFile(shared, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			link("before.png", shared)(t, dir)
		}, nil, ""},
		{"dangling symlink", nil, link("before.png", filepath.Join(outside, "nope.png")),
			[]string{baseID + ".layers[0].url"}, "not found"},
	})
}

func TestValidateBounds(t *testing.T) {
	setBBox := func(b [4]float64) func(m *Manifest) {
		return story0(func(s *domain.Story) { s.BBox = b })
	}
	setLayerBounds := func(b [4]float64) func(m *Manifest) {
		return story0(func(s *domain.Story) { s.Layers[1].Bounds = b })
	}
	runRuleCases(t, []ruleCase{
		{"whole world", func(m *Manifest) {
			m.Stories[0].BBox = [4]float64{-180, -90, 180, 90}
			for i := range m.Stories[0].Layers {
				m.Stories[0].Layers[i].Bounds = [4]float64{-180, -90, 180, 90}
			}
		}, nil, nil, ""},
		{"bbox west equals east", setBBox([4]float64{34, 36, 34, 38}), nil, []string{baseID + ".bbox"}, "west < east"},
		{"bbox west greater than east", setBBox([4]float64{38, 36, 30, 38}), nil, []string{baseID + ".bbox"}, ""},
		{"bbox south equals north", setBBox([4]float64{30, 37, 38, 37}), nil, []string{baseID + ".bbox"}, "south < north"},
		{"bbox south greater than north", setBBox([4]float64{30, 38, 38, 36}), nil, []string{baseID + ".bbox"}, ""},
		{"bbox west below -180", setBBox([4]float64{-181, 36, 38, 38}), nil, []string{baseID + ".bbox"}, ""},
		{"bbox east above 180", setBBox([4]float64{30, 36, 181, 38}), nil, []string{baseID + ".bbox"}, ""},
		{"bbox south below -90", setBBox([4]float64{30, -91, 38, 38}), nil, []string{baseID + ".bbox"}, ""},
		{"bbox north above 90", setBBox([4]float64{30, 36, 38, 91}), nil, []string{baseID + ".bbox"}, ""},
		{"bbox all zero", setBBox([4]float64{}), nil, []string{baseID + ".bbox"}, ""},
		{"bbox NaN", setBBox([4]float64{nan(), 36, 38, 38}), nil, []string{baseID + ".bbox"}, ""},
		{"layer bounds invalid", setLayerBounds([4]float64{38, 36, 30, 38}), nil, []string{baseID + ".layers[1].bounds"}, ""},
		{"layer bounds out of range", setLayerBounds([4]float64{30, 36, 38, 95}), nil, []string{baseID + ".layers[1].bounds"}, ""},
		{"layer inside bbox", setLayerBounds([4]float64{32, 36.5, 36, 37.5}), nil, nil, ""},
		{"layer partly overlaps bbox", setLayerBounds([4]float64{36, 37, 42, 40}), nil, nil, ""},
		{"layer bigger than bbox", setLayerBounds([4]float64{20, 30, 50, 50}), nil, nil, ""},
		{"layer far from bbox", setLayerBounds([4]float64{-70, -34, -68, -32}), nil, []string{baseID + ".layers[1].bounds"}, "overlap"},
		{"layer only touches bbox edge", setLayerBounds([4]float64{38, 36, 40, 38}), nil, []string{baseID + ".layers[1].bounds"}, ""},
		{"invalid bbox does not also flag layers", func(m *Manifest) {
			m.Stories[0].BBox = [4]float64{38, 36, 30, 38}
		}, nil, []string{baseID + ".bbox"}, ""},
		{"layer zero bounds", setLayerBounds([4]float64{}), nil, []string{baseID + ".layers[1].bounds"}, ""},
	})
}

func TestValidateDates(t *testing.T) {
	runRuleCases(t, []ruleCase{
		{"before after one day apart", story0(func(s *domain.Story) { s.BeforeDate, s.AfterDate = "2023-02-05", "2023-02-06" }), nil, nil, ""},
		{"leap day", story0(func(s *domain.Story) { s.BeforeDate = "2024-02-29"; s.AfterDate = "2024-03-01" }), nil, nil, ""},
		{"before equals after", story0(func(s *domain.Story) { s.AfterDate = s.BeforeDate }), nil, []string{baseID + ".beforeDate"}, "before"},
		{"before after afterDate", story0(func(s *domain.Story) { s.BeforeDate, s.AfterDate = "2023-02-18", "2023-01-25" }), nil, []string{baseID + ".beforeDate"}, ""},
		{"beforeDate empty", story0(func(s *domain.Story) { s.BeforeDate = "" }), nil, []string{baseID + ".beforeDate"}, "required"},
		{"afterDate empty", story0(func(s *domain.Story) { s.AfterDate = "" }), nil, []string{baseID + ".afterDate"}, "required"},
		{"eventDate empty", story0(func(s *domain.Story) { s.EventDate = "" }), nil, []string{baseID + ".eventDate"}, "required"},
		{"beforeDate one digit month", story0(func(s *domain.Story) { s.BeforeDate = "2023-1-25" }), nil, []string{baseID + ".beforeDate"}, "YYYY-MM-DD"},
		{"afterDate wrong layout", story0(func(s *domain.Story) { s.AfterDate = "18/02/2023" }), nil, []string{baseID + ".afterDate"}, ""},
		{"eventDate with time", story0(func(s *domain.Story) { s.EventDate = "2023-02-06T01:17:34Z" }), nil, []string{baseID + ".eventDate"}, ""},
		{"eventDate impossible day", story0(func(s *domain.Story) { s.EventDate = "2023-02-30" }), nil, []string{baseID + ".eventDate"}, ""},
		{"both unparsable reports both once", story0(func(s *domain.Story) { s.BeforeDate, s.AfterDate = "x", "y" }), nil,
			[]string{baseID + ".afterDate", baseID + ".beforeDate"}, ""},
		{"frame without date", story0(func(s *domain.Story) { s.Layers[3].Date = "" }), nil, []string{baseID + ".layers[3].date"}, "frame"},
		{"frame with bad date", story0(func(s *domain.Story) { s.Layers[3].Date = "2023-13-01" }), nil, []string{baseID + ".layers[3].date"}, ""},
		{"before layer without date is fine", story0(func(s *domain.Story) { s.Layers[0].Date = "" }), nil, nil, ""},
		{"before layer with bad date", story0(func(s *domain.Story) { s.Layers[0].Date = "yesterday" }), nil, []string{baseID + ".layers[0].date"}, ""},
	})
}

func TestValidateExplanation(t *testing.T) {
	runRuleCases(t, []ruleCase{
		{"caveats may be empty", story0(func(s *domain.Story) { s.Explanation.Caveats = "" }), nil, nil, ""},
		{"driver empty", story0(func(s *domain.Story) { s.Explanation.Driver = "" }), nil, []string{baseID + ".explanation.driver"}, "non-empty"},
		{"polarizationNote empty", story0(func(s *domain.Story) { s.Explanation.PolarizationNote = "" }), nil, []string{baseID + ".explanation.polarizationNote"}, ""},
		{"howToRead empty", story0(func(s *domain.Story) { s.Explanation.HowToRead = "" }), nil, []string{baseID + ".explanation.howToRead"}, ""},
		{"whitespace only", story0(func(s *domain.Story) { s.Explanation.Driver = " \t\n" }), nil, []string{baseID + ".explanation.driver"}, ""},
		{"all three empty", story0(func(s *domain.Story) { s.Explanation = domain.Explanation{} }), nil,
			[]string{baseID + ".explanation.driver", baseID + ".explanation.polarizationNote", baseID + ".explanation.howToRead"}, ""},
	})
}

func TestValidateMultiSensor(t *testing.T) {
	saocomCredit := CONAECreditPrefix + " - 2026. Todos los derechos reservados"
	// twoSensors turns the base story into a SAOCOM-before / NISAR-after swipe
	// without a change layer.
	twoSensors := func(s *domain.Story) {
		s.Platform = "SAOCOM-1A"
		s.Layers = []domain.StoryLayer{
			{Kind: "before", URL: "before.png", Bounds: bboxAll, Platform: "SAOCOM-1A"},
			{Kind: "after", URL: "after.png", Bounds: bboxAll, Platform: "NISAR", Polarization: "HV"},
		}
		s.Credits = []string{saocomCredit}
	}
	with := func(edits ...func(s *domain.Story)) func(m *Manifest) {
		return story0(func(s *domain.Story) {
			for _, e := range edits {
				e(s)
			}
		})
	}
	withChange := func(s *domain.Story) {
		s.Layers = append(s.Layers, domain.StoryLayer{Kind: "change", URL: "change.png", Bounds: bboxAll})
	}
	credits := func(c ...string) func(s *domain.Story) { return func(s *domain.Story) { s.Credits = c } }

	runRuleCases(t, []ruleCase{
		// cross-sensor change layers
		{"two sensors without change layer", with(twoSensors), nil, nil, ""},
		{"two sensors with change layer: the only pair is cross-sensor", with(twoSensors, withChange), nil,
			[]string{baseID + ".layers[2].platform"}, "no after layer on that platform"},
		{"change layer on NISAR, only before is SAOCOM", with(twoSensors, withChange, func(s *domain.Story) {
			s.Layers[2].Platform = "NISAR"
		}), nil, []string{baseID + ".layers[2].platform"}, "no before layer on that platform"},
		{"change layer on a third platform", with(twoSensors, withChange, func(s *domain.Story) {
			s.Layers[2].Platform = "Sentinel-1"
		}), nil, []string{baseID + ".layers[2].platform"}, "no before and after layer"},
		{"story 5: SAOCOM change layer plus a SAOCOM/NISAR swipe", story0(func(s *domain.Story) {
			s.Platform = "SAOCOM-1A + NISAR"
			s.Layers = []domain.StoryLayer{
				{Kind: "before", URL: "before.png", Bounds: bboxAll, Platform: "SAOCOM-1A"},
				{Kind: "after", URL: "after.png", Bounds: bboxAll, Platform: "SAOCOM-1A"},
				{Kind: "change", URL: "change.png", Bounds: bboxAll, Platform: "SAOCOM-1A"},
				{Kind: "after", URL: "after_nisar.png", Bounds: bboxAll, Platform: "NISAR"},
			}
			s.Credits = []string{saocomCredit}
		}), nil, nil, ""},
		{"story 5 without the CONAE credit", story0(func(s *domain.Story) {
			s.Platform = "SAOCOM-1A + NISAR"
			s.Layers = []domain.StoryLayer{
				{Kind: "before", URL: "before.png", Bounds: bboxAll, Platform: "SAOCOM-1A"},
				{Kind: "after", URL: "after.png", Bounds: bboxAll, Platform: "SAOCOM-1A"},
				{Kind: "change", URL: "change.png", Bounds: bboxAll, Platform: "SAOCOM-1A"},
				{Kind: "after", URL: "after_nisar.png", Bounds: bboxAll, Platform: "NISAR"},
			}
		}), nil, []string{baseID + ".credits"}, ""},
		{"story 5 with the NISAR swipe on both sides", story0(func(s *domain.Story) {
			s.Platform = "SAOCOM-1A + NISAR"
			s.Layers = []domain.StoryLayer{
				{Kind: "before", URL: "before.png", Bounds: bboxAll, Platform: "SAOCOM-1A"},
				{Kind: "after", URL: "after.png", Bounds: bboxAll, Platform: "SAOCOM-1A"},
				{Kind: "change", URL: "change.png", Bounds: bboxAll, Platform: "SAOCOM-1A"},
				{Kind: "before", URL: "before_nisar.png", Bounds: bboxAll, Platform: "NISAR"},
				{Kind: "after", URL: "after_nisar.png", Bounds: bboxAll, Platform: "NISAR"},
			}
			s.Credits = []string{saocomCredit}
		}), nil, nil, ""},
		{"two change layers, one has no matching pair", story0(func(s *domain.Story) {
			s.Layers = append(s.Layers,
				domain.StoryLayer{Kind: "change", URL: "change2.png", Bounds: bboxAll, Platform: "NISAR"})
		}), nil, []string{baseID + ".layers[4].platform"}, `"NISAR"`},
		{"change layer with a before layer but no after layer: nothing to compare", story0(func(s *domain.Story) {
			s.Layers = []domain.StoryLayer{
				{Kind: "before", URL: "before.png", Bounds: bboxAll, Platform: "NISAR"},
				{Kind: "change", URL: "change.png", Bounds: bboxAll},
			}
		}), nil, nil, ""},
		{"change layer, same platform on layers", story0(func(s *domain.Story) {
			s.Layers[0].Platform, s.Layers[1].Platform = "Sentinel-1", "Sentinel-1"
		}), nil, nil, ""},
		{"change layer, platform inherited from story", nil, nil, nil, ""},
		{"change layer, after layer overrides platform", story0(func(s *domain.Story) {
			s.Layers[1].Platform = "NISAR"
		}), nil, []string{baseID + ".layers[2].platform"}, "after"},
		{"change layer, before layer overrides platform", story0(func(s *domain.Story) {
			s.Layers[0].Platform = "NISAR"
		}), nil, []string{baseID + ".layers[2].platform"}, "before"},
		{"change layer inherits a platform no source layer has", story0(func(s *domain.Story) {
			s.Layers[0].Platform, s.Layers[1].Platform = "NISAR", "NISAR"
		}), nil, []string{baseID + ".layers[2].platform"}, ""},
		{"change layer, all three layers override to the same", story0(func(s *domain.Story) {
			s.Layers[0].Platform, s.Layers[1].Platform, s.Layers[2].Platform = "NISAR", "NISAR", "NISAR"
		}), nil, nil, ""},
		{"change layer, platform differs only by case", story0(func(s *domain.Story) {
			s.Layers[0].Platform, s.Layers[1].Platform = "Sentinel-1", "sentinel-1"
		}), nil, nil, ""},
		{"change layer, both platforms empty", story0(func(s *domain.Story) {
			s.Platform = ""
		}), nil, nil, ""},
		{"change layer, story platform empty and one layer set", story0(func(s *domain.Story) {
			s.Platform = ""
			s.Layers[1].Platform = "NISAR"
		}), nil, []string{baseID + ".layers[2].platform"}, "(none)"},
		{"whitespace-only layer platform inherits the story platform", story0(func(s *domain.Story) {
			s.Layers[0].Platform = "   "
		}), nil, nil, ""},
		{"frame on another platform is ignored", story0(func(s *domain.Story) {
			s.Layers[3].Platform = "NISAR"
		}), nil, nil, ""},
		{"change layer, extra after layer on another platform is a swipe only", story0(func(s *domain.Story) {
			s.Layers = append(s.Layers, domain.StoryLayer{Kind: "after", URL: "after2.png", Bounds: bboxAll, Platform: "NISAR"})
		}), nil, nil, ""},
		{"interferogram does not need matching platforms", with(twoSensors, func(s *domain.Story) {
			s.Layers = append(s.Layers, domain.StoryLayer{Kind: "interferogram", URL: "ifg.png", Bounds: bboxAll})
		}), nil, nil, ""},

		// CONAE credit
		{"SAOCOM layer with CONAE credit", with(twoSensors), nil, nil, ""},
		{"SAOCOM layer without credits", with(twoSensors, credits()), nil, []string{baseID + ".credits"}, "Producto SAOCOM® - ©CONAE"},
		{"SAOCOM layer with unrelated credit", with(twoSensors, credits("Contains modified Copernicus Sentinel data 2023")), nil,
			[]string{baseID + ".credits"}, "Todos los derechos reservados"},
		{"SAOCOM layer, credit with wrong symbols", with(twoSensors, credits("Producto SAOCOM (R) - (c)CONAE - 2026")), nil,
			[]string{baseID + ".credits"}, ""},
		{"SAOCOM layer, CONAE credit not first", with(twoSensors, credits("Other credit", saocomCredit)), nil, nil, ""},
		{"SAOCOM layer, CONAE credit with leading space", with(twoSensors, credits("  "+saocomCredit)), nil, nil, ""},
		{"SAOCOM layer, credit only contains prefix", with(twoSensors, credits("Data from Producto SAOCOM® - ©CONAE - 2026")), nil,
			[]string{baseID + ".credits"}, ""},
		{"SAOCOM story platform inherited by layers", story0(func(s *domain.Story) {
			s.Platform = "SAOCOM-1B"
			s.Credits = []string{"Test credit"}
		}), nil, []string{baseID + ".credits"}, ""},
		{"SAOCOM story platform inherited, with credit", story0(func(s *domain.Story) {
			s.Platform = "SAOCOM-1B"
			s.Credits = []string{saocomCredit}
		}), nil, nil, ""},
		{"SAOCOM lower case needs credit", with(twoSensors, func(s *domain.Story) {
			s.Platform = "x"
			s.Layers[0].Platform = "saocom-1a"
		}, credits("c")), nil, []string{baseID + ".credits"}, ""},
		{"story says SAOCOM but every layer overrides to NISAR", story0(func(s *domain.Story) {
			s.Platform = "SAOCOM-1A"
			for i := range s.Layers {
				s.Layers[i].Platform = "NISAR"
			}
		}), nil, nil, ""},
		{"NISAR only needs no CONAE credit", story0(func(s *domain.Story) {
			s.Platform = "NISAR"
			s.Credits = nil
		}), nil, nil, ""},
		{"frame layer on SAOCOM triggers the credit rule", story0(func(s *domain.Story) {
			s.Layers[3].Platform = "SAOCOM-1A"
		}), nil, []string{baseID + ".credits"}, ""},
		{"SAOCOM layer and mismatched change layer report both", with(twoSensors, withChange, credits()), nil,
			[]string{baseID + ".layers[2].platform", baseID + ".credits"}, ""},
	})
}

func nan() float64 {
	zero := 0.0
	return zero / zero
}

// Validate must return every problem, not just the first.
func TestValidateReturnsAllErrors(t *testing.T) {
	dir, m := baseManifest(t)
	s := &m.Stories[0]
	s.Type = "meteor"
	s.BeforeDate = "nope"
	s.Explanation.Driver = ""
	s.Layers[1].URL = "gone.png"
	s.Layers[3].Date = ""
	errs := Validate(dir, m)
	want := []string{
		baseID + ".type", baseID + ".beforeDate", baseID + ".explanation.driver",
		baseID + ".layers[1].url", baseID + ".layers[3].date",
	}
	slices.Sort(want)
	if got := errPaths(t, errs); !slices.Equal(got, want) {
		t.Fatalf("error paths = %q, want %q", got, want)
	}
}

func TestValidateErrorsAcrossStories(t *testing.T) {
	dir, m := baseManifest(t)
	second := baseStory()
	second.ID = "demo-second"
	second.Explanation.HowToRead = ""
	second.Layers[0].URL = "../escape.png"
	m.Stories = append(m.Stories, second)
	writeStoryFiles(t, dir, second)
	want := []string{"demo-second.explanation.howToRead", "demo-second.layers[0].url"}
	if got := errPaths(t, Validate(dir, m)); !slices.Equal(got, want) {
		t.Fatalf("error paths = %q, want %q", got, want)
	}
}

// A malformed id must not be used to build file paths.
func TestValidateBadIDSkipsFileChecks(t *testing.T) {
	dir, m := baseManifest(t)
	m.Stories[0].ID = "../.."
	errs := Validate(dir, m)
	if got := errPaths(t, errs); !slices.Equal(got, []string{"stories[0].id"}) {
		t.Fatalf("error paths = %q (%v)", got, errs)
	}
}

func TestValidationErrorFormat(t *testing.T) {
	tests := []struct {
		e    ValidationError
		want string
	}{
		{ValidationError{Story: "demo", Field: "layers[1].url", Msg: "boom"}, "demo.layers[1].url: boom"},
		{ValidationError{Story: "demo", Msg: "boom"}, "demo: boom"},
		{ValidationError{Field: "manifest.version", Msg: "boom"}, "manifest.version: boom"},
	}
	for _, tc := range tests {
		if got := tc.e.Error(); got != tc.want {
			t.Errorf("Error() = %q, want %q", got, tc.want)
		}
	}
}

func TestValidateDoesNotMutateManifest(t *testing.T) {
	dir, m := baseManifest(t)
	m.Stories[0].Layers[0].URL = "../bad.png"
	before, _ := json.Marshal(m)
	Validate(dir, m)
	after, _ := json.Marshal(m)
	if !bytes.Equal(before, after) {
		t.Fatal("Validate changed the manifest")
	}
}

// ---------------------------------------------------------------- Load

func writeManifest(t *testing.T, dir string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	a, b, c := baseStory(), baseStory(), baseStory()
	a.ID, a.Order = "story-a", 2
	b.ID, b.Order = "story-b", 1
	c.ID, c.Order = "story-c", 1 // same order as b: ties break by id
	writeManifest(t, dir, Manifest{Version: 1, Stories: []domain.Story{a, c, b}})

	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range got {
		ids = append(ids, s.ID)
	}
	if want := []string{"story-b", "story-c", "story-a"}; !slices.Equal(ids, want) {
		t.Fatalf("order = %v, want %v", ids, want)
	}
	s := got[2] // story-a
	if want := "/media/stories/story-a/thumb.jpg"; s.Thumbnail != want {
		t.Errorf("Thumbnail = %q, want %q", s.Thumbnail, want)
	}
	for i, l := range s.Layers {
		want := "/media/stories/story-a/" + baseStory().Layers[i].URL
		if l.URL != want {
			t.Errorf("layers[%d].URL = %q, want %q", i, l.URL, want)
		}
		if !strings.HasPrefix(l.URL, "/media/stories/") {
			t.Errorf("layers[%d].URL %q does not start with /media/stories/", i, l.URL)
		}
	}
	// Everything else is untouched.
	if s.Title != a.Title || s.BBox != a.BBox || s.Layers[0].Bounds != a.Layers[0].Bounds || s.Explanation != a.Explanation {
		t.Errorf("Load changed fields other than URLs: %+v", s)
	}
}

func TestLoadKeepsEmptyFilesEmpty(t *testing.T) {
	dir := t.TempDir()
	s := baseStory()
	s.Thumbnail = ""
	s.Layers[0].URL = ""
	writeManifest(t, dir, Manifest{Version: 1, Stories: []domain.Story{s}})
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Thumbnail != "" || got[0].Layers[0].URL != "" {
		t.Errorf("empty file names must stay empty, got %q and %q", got[0].Thumbnail, got[0].Layers[0].URL)
	}
}

func TestLoadRawDoesNotRewrite(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, Manifest{Version: 1, Stories: []domain.Story{baseStory()}})
	m, err := LoadRaw(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != 1 || len(m.Stories) != 1 {
		t.Fatalf("manifest = %+v", m)
	}
	if m.Stories[0].Thumbnail != "thumb.jpg" || m.Stories[0].Layers[1].URL != "after.png" {
		t.Errorf("LoadRaw must keep file names, got %q and %q", m.Stories[0].Thumbnail, m.Stories[0].Layers[1].URL)
	}
}

func TestLoadEmpty(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, Manifest{Version: 1})
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("Load = %#v, want an empty non-nil slice", got)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name     string
		manifest string // "" = no manifest file
		want     string
	}{
		{"missing manifest", "", "manifest.json"},
		{"not json", "this is not json", "parse"},
		{"empty file", " ", "parse"},
		{"wrong type", `{"version":"1","stories":[]}`, "parse"},
		{"trailing data", `{"version":1,"stories":[]} {"x":1}`, "trailing"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.manifest != "" {
				if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(tc.manifest), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Load error = %v, want it to contain %q", err, tc.want)
			}
			if _, err := LoadRaw(dir); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("LoadRaw error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

// LoadRaw (used by the validator) rejects unknown fields so a typo such as
// "platfrom" cannot silently disable a rule; Load (used at API boot) ignores
// them so one stray field never takes the whole stories feature down.
func TestUnknownFields(t *testing.T) {
	dir := t.TempDir()
	const doc = `{"version":1,"stories":[{"id":"demo-test","platfrom":"SAOCOM-1A"}]}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRaw(dir); err == nil || !strings.Contains(err.Error(), "platfrom") {
		t.Errorf("LoadRaw error = %v, want it to name the unknown field", err)
	}
	got, err := Load(dir)
	if err != nil || len(got) != 1 || got[0].ID != "demo-test" {
		t.Errorf("Load = %v, %v; want the story despite the unknown field", got, err)
	}
}

// ------------------------------------------------- committed demo stories

func TestCommittedStories(t *testing.T) {
	m, err := LoadRaw(repoStories)
	if err != nil {
		t.Fatal(err)
	}
	if errs := Validate(repoStories, m); len(errs) != 0 {
		t.Fatalf("data/stories is invalid: %v", errs)
	}
	byID := map[string]domain.Story{}
	for _, s := range m.Stories {
		byID[s.ID] = s
	}

	ph, ok := byID["demo-placeholder"]
	if !ok {
		t.Fatal("demo-placeholder missing")
	}
	if ph.BBox != [4]float64{30.0, 36.0, 38.0, 38.0} || ph.IncidentSource != "usgs" || ph.IncidentSourceID != "us6000jllz" {
		t.Errorf("demo-placeholder bbox/link = %v %q %q", ph.BBox, ph.IncidentSource, ph.IncidentSourceID)
	}
	if len(ph.Layers) < 3 || ph.Layers[0].Kind != "before" || ph.Layers[1].Kind != "after" || ph.Layers[1].URL != "after.png" {
		t.Errorf("demo-placeholder layers = %+v (layers[1] must be after.png)", ph.Layers)
	}

	two, ok := byID["demo-two-sensors"]
	if !ok {
		t.Fatal("demo-two-sensors missing")
	}
	var before, after *domain.StoryLayer
	for i := range two.Layers {
		switch two.Layers[i].Kind {
		case "before":
			before = &two.Layers[i]
		case "after":
			after = &two.Layers[i]
		case "change":
			t.Error("demo-two-sensors must not have a change layer")
		}
	}
	if before == nil || after == nil || before.Platform != "SAOCOM-1A" || after.Platform != "NISAR" {
		t.Fatalf("demo-two-sensors layers = %+v", two.Layers)
	}
	conae := false
	for _, c := range two.Credits {
		conae = conae || strings.HasPrefix(c, CONAECreditPrefix)
	}
	if !conae {
		t.Errorf("demo-two-sensors credits = %q, want a CONAE credit", two.Credits)
	}

	loaded, err := Load(repoStories)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != len(m.Stories) {
		t.Fatalf("Load returned %d stories, LoadRaw %d", len(loaded), len(m.Stories))
	}
	for _, s := range loaded {
		prefix := "/media/stories/" + s.ID + "/"
		if !strings.HasPrefix(s.Thumbnail, prefix) {
			t.Errorf("%s thumbnail %q", s.ID, s.Thumbnail)
		}
		for i, l := range s.Layers {
			if !strings.HasPrefix(l.URL, prefix) {
				t.Errorf("%s layers[%d].url %q", s.ID, i, l.URL)
			}
		}
	}
}

// The committed placeholder images are real PNG/JPEG files and stay tiny
// (the disk is nearly full and the repo budget for data/stories is 25 MB).
func TestCommittedAssetsDecode(t *testing.T) {
	m, err := LoadRaw(repoStories)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range m.Stories {
		files := []string{s.Thumbnail}
		for _, l := range s.Layers {
			files = append(files, l.URL)
		}
		for _, name := range files {
			p := filepath.Join(repoStories, s.ID, name)
			b, err := os.ReadFile(p)
			if err != nil {
				t.Errorf("%s: %v", p, err)
				continue
			}
			if len(b) > 16<<10 {
				t.Errorf("%s is %d bytes, want a few KB at most", p, len(b))
			}
			var format string
			switch filepath.Ext(name) {
			case ".png":
				_, err = png.Decode(bytes.NewReader(b))
				format = "png"
			case ".jpg":
				_, err = jpeg.Decode(bytes.NewReader(b))
				format = "jpeg"
			default:
				t.Errorf("%s: unexpected extension", p)
				continue
			}
			if err != nil {
				t.Errorf("%s does not decode as %s: %v", p, format, err)
			}
		}
	}
}
