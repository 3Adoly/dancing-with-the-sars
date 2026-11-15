package httpapi

import (
	"net/http"
	"net/url"
	"os"

	"github.com/go-chi/chi/v5"

	"github.com/dwts/backend/internal/domain"
)

// StoriesAPI is what the story handlers need. *service.StoryService implements it.
type StoriesAPI interface {
	Len() int
	ETag() string
	List() []domain.StorySummary
	Get(id string) (domain.Story, error)
	MediaPath(id, file string) (path, contentType string, err error)
}

const (
	ccStories = "public, max-age=300"
	ccMedia   = "public, max-age=86400, immutable"
)

func storiesETag(s StoriesAPI, parts ...string) string {
	return MakeETag(0, append([]string{"stories", s.ETag()}, parts...)...)
}

// stories is E12. With no story service it returns an empty list.
func (h *handlers) stories(w http.ResponseWriter, r *http.Request) {
	if h.d.Stories == nil {
		writeJSON(w, http.StatusOK, StoriesResponse{Stories: []domain.StorySummary{}})
		return
	}
	if CheckETag(w, r, storiesETag(h.d.Stories, "list"), ccStories) {
		return
	}
	writeJSON(w, http.StatusOK, StoriesResponse{Stories: h.d.Stories.List()})
}

// story is E13.
func (h *handlers) story(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if h.d.Stories == nil {
		writeError(w, r, domain.ErrNotFound)
		return
	}
	st, err := h.d.Stories.Get(id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if CheckETag(w, r, storiesETag(h.d.Stories, "story", id), ccStories) {
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// storyMedia is E14: a story file, immutable cache, never a directory listing, no traversal.
// Path params are unescaped once and then validated by MediaPath (which rejects separators, "..",
// drive letters and symlink escapes), so encoded tricks like ..%2f or %5c are refused.
func (h *handlers) storyMedia(w http.ResponseWriter, r *http.Request) {
	if h.d.Stories == nil {
		writeError(w, r, domain.ErrNotFound)
		return
	}
	id, err1 := url.PathUnescape(chi.URLParam(r, "id"))
	file, err2 := url.PathUnescape(chi.URLParam(r, "file"))
	if err1 != nil || err2 != nil {
		writeError(w, r, domain.ErrNotFound)
		return
	}
	p, ct, err := h.d.Stories.MediaPath(id, file)
	if err != nil {
		writeError(w, r, err)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		writeError(w, r, domain.ErrNotFound)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		writeError(w, r, domain.ErrNotFound)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", ccMedia)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, fi.Name(), fi.ModTime(), f) // handles Range, If-Modified-Since, HEAD
}
