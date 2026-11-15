package main

// TEMPORARY story loader — replace with stories.Load from M14 (Mohammed, internal/stories).
//
// When M14 lands, in main.go change
//
//	storyLoader := tempStoryLoader(cfg.StoriesDir)
//
// to
//
//	storyLoader := func() ([]domain.Story, error) { return stories.Load(cfg.StoriesDir) }
//
// and delete this file and stories_temp_test.go. The manifest contract is Plan_Detailed §10.1;
// this loader only does the minimal checks the API needs (the full validator is M14).

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/dwts/backend/internal/domain"
)

var tempStoryIDRe = regexp.MustCompile(`^[a-z0-9-]{3,64}$`)

type tempManifest struct {
	Version int            `json:"version"`
	Stories []domain.Story `json:"stories"`
}

// tempStoryLoader reads <dir>/manifest.json, checks ids are unique and valid and referenced files are
// safe names that exist, rewrites file names to /media/stories/<id>/<file> and sorts by order.
// Invalid stories are skipped (the error lists them); valid ones are still returned.
func tempStoryLoader(dir string) func() ([]domain.Story, error) {
	return func() ([]domain.Story, error) {
		b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
		if err != nil {
			return nil, fmt.Errorf("stories manifest: %w", err)
		}
		var m tempManifest
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, fmt.Errorf("stories manifest: %w", err)
		}
		var out []domain.Story
		var errs []error
		seen := map[string]bool{}
		for _, st := range m.Stories {
			if err := checkTempStory(dir, st, seen); err != nil {
				errs = append(errs, err)
				continue
			}
			seen[st.ID] = true
			st.Thumbnail = mediaURL(st.ID, st.Thumbnail)
			st.Layers = slices.Clone(st.Layers)
			for i := range st.Layers {
				st.Layers[i].URL = mediaURL(st.ID, st.Layers[i].URL)
			}
			out = append(out, st)
		}
		slices.SortStableFunc(out, func(a, b domain.Story) int { return a.Order - b.Order })
		return out, errors.Join(errs...)
	}
}

func checkTempStory(dir string, st domain.Story, seen map[string]bool) error {
	if !tempStoryIDRe.MatchString(st.ID) {
		return fmt.Errorf("story %q: invalid id", st.ID)
	}
	if seen[st.ID] {
		return fmt.Errorf("story %q: duplicate id", st.ID)
	}
	if !st.Type.Valid() {
		return fmt.Errorf("story %q: invalid type %q", st.ID, st.Type)
	}
	if len(st.Layers) == 0 {
		return fmt.Errorf("story %q: no layers", st.ID)
	}
	files := []string{st.Thumbnail}
	for _, l := range st.Layers {
		files = append(files, l.URL)
	}
	for _, f := range files {
		if f == "" || f != filepath.Base(f) || strings.ContainsAny(f, `/\:`) || strings.HasPrefix(f, ".") {
			return fmt.Errorf("story %q: unsafe file name %q", st.ID, f)
		}
		fi, err := os.Stat(filepath.Join(dir, st.ID, f))
		if err != nil || !fi.Mode().IsRegular() {
			return fmt.Errorf("story %q: missing file %q", st.ID, f)
		}
	}
	return nil
}

func mediaURL(id, file string) string { return "/media/stories/" + id + "/" + file }
