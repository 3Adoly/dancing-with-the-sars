package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dwts/backend/internal/domain"
	"github.com/dwts/backend/internal/service"
)

// realStories builds a StoryService over the dev placeholder manifest in testdata/stories.
// (The manifest is decoded here directly; the production loader is M14's stories.Load.)
func realStories(t *testing.T) *service.StoryService {
	t.Helper()
	dir := filepath.Join("..", "..", "testdata", "stories")
	load := func() ([]domain.Story, error) {
		b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
		if err != nil {
			return nil, err
		}
		var m struct{ Stories []domain.Story }
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, err
		}
		for i := range m.Stories {
			st := &m.Stories[i]
			st.Thumbnail = "/media/stories/" + st.ID + "/" + st.Thumbnail
			for j := range st.Layers {
				st.Layers[j].URL = "/media/stories/" + st.ID + "/" + st.Layers[j].URL
			}
		}
		return m.Stories, nil
	}
	s, err := service.NewStoryService(context.Background(), load, dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func storiesRouter(t *testing.T) http.Handler {
	return NewRouter(Deps{Stories: realStories(t), CORSOrigins: []string{"http://localhost:5173"}})
}

func TestStoriesEndpoints(t *testing.T) {
	h := storiesRouter(t)

	rec := do(t, h, http.MethodGet, "/api/v1/stories", nil)
	var list StoriesResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &list) != nil || len(list.Stories) != 2 || list.Stories[0].ID != "aomori-2025-nisar" {
		t.Fatalf("E12: %d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Cache-Control") != ccStories || rec.Header().Get("ETag") == "" {
		t.Errorf("E12 headers %v", rec.Header())
	}
	ex, _ := os.ReadFile(filepath.Join("..", "..", "api", "examples", "stories.json"))
	if got, want := topKeys(t, rec.Body.Bytes()), topKeys(t, ex); !reflect.DeepEqual(got, want) {
		t.Errorf("E12 keys %v want %v", got, want)
	}
	if rec2 := do(t, h, http.MethodGet, "/api/v1/stories", map[string]string{"If-None-Match": rec.Header().Get("ETag")}); rec2.Code != 304 {
		t.Errorf("E12 304: %d", rec2.Code)
	}

	rec = do(t, h, http.MethodGet, "/api/v1/stories/demo-two-sensors", nil)
	var st domain.Story
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &st) != nil {
		t.Fatalf("E13: %d %s", rec.Code, rec.Body)
	}
	if st.Layers[0].Platform != "SAOCOM-1A" || st.Layers[1].Platform != "NISAR" || !strings.HasPrefix(st.Layers[0].URL, "/media/stories/demo-two-sensors/") {
		t.Errorf("E13 layers %+v", st.Layers)
	}
	ex, _ = os.ReadFile(filepath.Join("..", "..", "api", "examples", "story-saocom.json"))
	gotKeys, wantKeys := topKeys(t, rec.Body.Bytes()), topKeys(t, ex)
	if !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Errorf("E13 keys %v want %v", gotKeys, wantKeys)
	}
	if rec2 := do(t, h, http.MethodGet, "/api/v1/stories/demo-two-sensors", map[string]string{"If-None-Match": rec.Header().Get("ETag")}); rec2.Code != 304 {
		t.Errorf("E13 304: %d", rec2.Code)
	}
	rec = do(t, h, http.MethodGet, "/api/v1/stories/nope", nil)
	if rec.Code != 404 || decodeError(t, rec).Error.Code != CodeNotFound {
		t.Errorf("E13 404: %d", rec.Code)
	}
}

func TestStoryMedia(t *testing.T) {
	h := storiesRouter(t)
	cases := []struct{ path, ct string }{
		{"/media/stories/aomori-2025-nisar/change_logratio.png", "image/png"},
		{"/media/stories/aomori-2025-nisar/thumb.jpg", "image/jpeg"},
	}
	for _, c := range cases {
		rec := do(t, h, http.MethodGet, c.path, nil)
		if rec.Code != 200 || rec.Header().Get("Content-Type") != c.ct || rec.Header().Get("Cache-Control") != ccMedia ||
			rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Body.Len() == 0 {
			t.Errorf("%s: %d %v", c.path, rec.Code, rec.Header())
		}
	}
	bad := []string{
		"/media/stories/aomori-2025-nisar/..%2f..%2f..%2fgo.mod",
		"/media/stories/aomori-2025-nisar/..%2fmanifest.json",
		"/media/stories/aomori-2025-nisar/..%5c..%5cgo.mod",
		"/media/stories/aomori-2025-nisar/%2fetc%2fpasswd",
		"/media/stories/aomori-2025-nisar/C:%5cWindows%5cwin.ini",
		"/media/stories/..%2f..%2f/go.mod",
		"/media/stories/aomori-2025-nisar/..",
		"/media/stories/aomori-2025-nisar/",
		"/media/stories/aomori-2025-nisar",
		"/media/stories/aomori-2025-nisar/missing.png",
		"/media/stories/aomori-2025-nisar/manifest.txt",
		"/media/stories/unknown-story/thumb.jpg",
		"/media/stories/../stories/aomori-2025-nisar/thumb.jpg",
	}
	for _, p := range bad {
		req := httptestRequest(p)
		rec := doReq(h, req)
		if rec.Code != 404 {
			t.Errorf("%s: want 404, got %d (%s)", p, rec.Code, rec.Body)
		}
		if strings.Contains(rec.Body.String(), "module github.com") || strings.Contains(rec.Body.String(), "<pre>") {
			t.Errorf("%s leaked content or a directory listing", p)
		}
	}
	// No stories service → 404, E12 empty list.
	h2 := NewRouter(Deps{})
	if rec := do(t, h2, http.MethodGet, "/media/stories/aomori-2025-nisar/thumb.jpg", nil); rec.Code != 404 {
		t.Errorf("nil stories media: %d", rec.Code)
	}
	if rec := do(t, h2, http.MethodGet, "/api/v1/stories", nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"stories":[]`) {
		t.Errorf("nil stories list: %d %s", rec.Code, rec.Body)
	}
}

func TestHealthzStoriesCount(t *testing.T) {
	rec := do(t, storiesRouter(t), http.MethodGet, "/healthz", nil)
	var hr HealthResponse
	if json.Unmarshal(rec.Body.Bytes(), &hr) != nil || hr.Stories != 2 {
		t.Errorf("healthz stories: %s", rec.Body)
	}
}

func httptestRequest(p string) *http.Request { return httptest.NewRequest(http.MethodGet, p, nil) }

func doReq(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}
