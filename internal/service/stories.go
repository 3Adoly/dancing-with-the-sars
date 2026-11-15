package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/dwts/backend/internal/domain"
	"github.com/dwts/backend/internal/store"
)

// StoryLoader loads the stories (manifest) with layer/thumbnail URLs already rewritten to
// /media/stories/<id>/<file>. Production: stories.Load (Mohammed, M14).
type StoryLoader func() ([]domain.Story, error)

// AllowedMediaExt is the extension allow-list for E14 media (lower case, with dot).
var AllowedMediaExt = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".webp": "image/webp",
	".json": "application/json", ".tif": "image/tiff",
}

var storyIDRe = regexp.MustCompile(`^[a-z0-9-]{3,64}$`)

// StoryService serves E12–E14 from an in-memory story list loaded once at startup.
type StoryService struct {
	stories    []domain.Story
	byID       map[string]int
	byIncident map[int64]string
	root       string // STORIES_DIR (absolute, symlinks resolved)
	etag       string
}

// NewStoryService loads stories, links them to incidents and prepares media serving from mediaRoot.
// inc may be nil; then only stories with an explicit incidentId are linked. A loader error yields
// an empty (but usable) service plus the error, so the API still starts.
func NewStoryService(ctx context.Context, load StoryLoader, mediaRoot string, inc store.IncidentReader, log *slog.Logger) (*StoryService, error) {
	if log == nil {
		log = slog.Default()
	}
	s := &StoryService{byID: map[string]int{}, byIncident: map[int64]string{}}
	if root, err := filepath.Abs(mediaRoot); err == nil {
		if real, err := filepath.EvalSymlinks(root); err == nil {
			s.root = real
		}
	}
	var stories []domain.Story
	var loadErr error
	if load != nil {
		stories, loadErr = load()
	}
	slices.SortStableFunc(stories, func(a, b domain.Story) int {
		if a.Order != b.Order {
			return a.Order - b.Order
		}
		return strings.Compare(a.ID, b.ID)
	})
	for _, st := range stories {
		if _, dup := s.byID[st.ID]; dup {
			log.Warn("duplicate story id skipped", "id", st.ID)
			continue
		}
		if st.IncidentID == nil && st.IncidentSource != "" && st.IncidentSourceID != "" && inc != nil {
			if id, err := inc.FindBySource(ctx, st.IncidentSource, st.IncidentSourceID); err == nil {
				st.IncidentID = &id
			} else if !errors.Is(err, domain.ErrNotFound) {
				log.Warn("story incident lookup failed", "story", st.ID, "err", err)
			}
		}
		if st.IncidentID != nil {
			s.byIncident[*st.IncidentID] = st.ID
		}
		s.byID[st.ID] = len(s.stories)
		s.stories = append(s.stories, st)
	}
	b, _ := json.Marshal(s.stories)
	h := fnv.New64a()
	_, _ = h.Write(b)
	s.etag = fmt.Sprintf("%x", h.Sum64())
	return s, loadErr
}

// Len returns the number of loaded stories.
func (s *StoryService) Len() int { return len(s.stories) }

// ETag returns a hash of the loaded stories (changes when the manifest changes).
func (s *StoryService) ETag() string { return s.etag }

// List returns the E12 summaries, ordered by `order` then id.
func (s *StoryService) List() []domain.StorySummary {
	out := make([]domain.StorySummary, 0, len(s.stories))
	for _, st := range s.stories {
		out = append(out, st.ToSummary())
	}
	return out
}

// Get returns one story (E13) or domain.ErrNotFound.
func (s *StoryService) Get(id string) (domain.Story, error) {
	i, ok := s.byID[id]
	if !ok {
		return domain.Story{}, fmt.Errorf("story %q: %w", id, domain.ErrNotFound)
	}
	return s.stories[i], nil
}

// ForIncident implements StoryLinker for /incidents/{id}/sar.
func (s *StoryService) ForIncident(id int64) (string, bool) {
	sid, ok := s.byIncident[id]
	return sid, ok
}

// MediaPath resolves a media file of a loaded story to an absolute path inside the stories root.
// Every failure is reported as domain.ErrNotFound (no information leak). Rules: known story id,
// plain file name (no separators, drive letters, "..", or leading dot), allowed extension,
// regular file, and the symlink-resolved path must stay inside the root.
func (s *StoryService) MediaPath(id, file string) (path, contentType string, err error) {
	notFound := fmt.Errorf("media %q/%q: %w", id, file, domain.ErrNotFound)
	if s.root == "" || !storyIDRe.MatchString(id) {
		return "", "", notFound
	}
	if _, ok := s.byID[id]; !ok {
		return "", "", notFound
	}
	if file == "" || strings.HasPrefix(file, ".") || strings.ContainsAny(file, `/\:`+"\x00") ||
		strings.Contains(file, "..") || filepath.Base(file) != file {
		return "", "", notFound
	}
	ct, ok := AllowedMediaExt[strings.ToLower(filepath.Ext(file))]
	if !ok {
		return "", "", notFound
	}
	p, err := filepath.EvalSymlinks(filepath.Join(s.root, id, file))
	if err != nil {
		return "", "", notFound
	}
	rel, err := filepath.Rel(s.root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", "", notFound
	}
	fi, err := os.Stat(p)
	if err != nil || !fi.Mode().IsRegular() {
		return "", "", notFound
	}
	return p, ct, nil
}
