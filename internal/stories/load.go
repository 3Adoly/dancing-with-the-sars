// Package stories loads and validates data/stories/manifest.json, the
// contract between Ibrahim's story content, Mohammed's validator
// (`ingest stories-validate`) and Ahmed's story endpoints (Plan_Detailed_Level.md §10).
//
// The package imports only internal/domain and the standard library, and
// nothing in it writes to disk or reads the environment.
package stories

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dwts/backend/internal/domain"
)

const (
	// ManifestFile is the manifest's file name inside the stories directory.
	ManifestFile = "manifest.json"
	// ManifestVersion is the only manifest schema version this package understands.
	ManifestVersion = 1
	// mediaPrefix is the URL prefix under which the API serves story files.
	mediaPrefix = "/media/stories/"
)

// Manifest is the parsed dir/manifest.json. In it, Layer.URL and Story.Thumbnail
// are file names relative to dir/<story id>/.
type Manifest struct {
	Version int            `json:"version"`
	Stories []domain.Story `json:"stories"`
}

// LoadRaw reads dir/manifest.json as written, without rewriting any file name.
// It rejects fields the schema does not know (a typo such as "platfrom" would
// otherwise silently switch a validation rule off). Validate works on its result.
func LoadRaw(dir string) (Manifest, error) {
	return readManifest(dir, true)
}

// Load reads dir/manifest.json for serving: Layer.URL and Thumbnail become
// "/media/stories/<id>/<file>" and the stories are sorted by Order (ties by id).
// It never touches the story files and does not validate; use LoadRaw + Validate
// for that. Unlike LoadRaw it ignores unknown fields, so one stray field cannot
// take the whole stories feature down at API boot. The result is never nil.
func Load(dir string) ([]domain.Story, error) {
	m, err := readManifest(dir, false)
	if err != nil {
		return nil, err
	}
	out := m.Stories
	if out == nil {
		out = []domain.Story{}
	}
	for i := range out {
		s := &out[i]
		if s.Thumbnail != "" {
			s.Thumbnail = mediaURL(s.ID, s.Thumbnail)
		}
		for j := range s.Layers {
			if s.Layers[j].URL != "" {
				s.Layers[j].URL = mediaURL(s.ID, s.Layers[j].URL)
			}
		}
	}
	slices.SortStableFunc(out, func(a, b domain.Story) int {
		if a.Order != b.Order {
			return a.Order - b.Order
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

// mediaURL is the public URL of a story file. This is a URL, not a file
// system path, so it always uses "/" and never filepath.
func mediaURL(id, file string) string {
	return mediaPrefix + id + "/" + file
}

func readManifest(dir string, strict bool) (Manifest, error) {
	path := filepath.Join(dir, ManifestFile)
	f, err := os.Open(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("stories: read %s: %w", path, err)
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	if strict {
		dec.DisallowUnknownFields()
	}
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("stories: parse %s: %w", path, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Manifest{}, fmt.Errorf("stories: parse %s: unexpected trailing data after the manifest object", path)
	}
	return m, nil
}
