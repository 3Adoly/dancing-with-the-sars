package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dwts/backend/internal/domain"
)

type stubSAR struct {
	lastID      int64
	lastWindow  int
	lastRefresh bool
	err         error
	stale       bool
}

func (s *stubSAR) DefaultWindowDays() int { return 45 }
func (s *stubSAR) ForIncidentWindow(_ context.Context, id int64, w int, refresh bool) (domain.SARResult, error) {
	s.lastID, s.lastWindow, s.lastRefresh = id, w, refresh
	if s.err != nil {
		return domain.SARResult{}, s.err
	}
	var r domain.SARResult
	mustExample("sar.json", &r)
	r.Stale = s.stale
	return r, nil
}
func (s *stubSAR) CoverageTimeline() domain.SARCoverageTimeline {
	var t domain.SARCoverageTimeline
	mustExample("sar-coverage-timeline.json", &t)
	return t
}

func sarRouter(s SARAPI) http.Handler {
	return NewRouter(Deps{SAR: s, CORSOrigins: []string{"http://localhost:5173"}})
}

func TestSAREndpoints(t *testing.T) {
	st := &stubSAR{}
	h := sarRouter(st)

	rec := do(t, h, http.MethodGet, "/api/v1/incidents/500812/sar", nil)
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != ccSAR || st.lastID != 500812 || st.lastWindow != 45 || st.lastRefresh {
		t.Fatalf("E10 default: %d %q %+v", rec.Code, rec.Header().Get("Cache-Control"), st)
	}
	ex, _ := os.ReadFile(filepath.Join("..", "..", "api", "examples", "sar.json"))
	if got, want := topKeys(t, rec.Body.Bytes()), topKeys(t, ex); !reflect.DeepEqual(got, want) {
		t.Errorf("E10 keys %v want %v", got, want)
	}

	rec = do(t, h, http.MethodGet, "/api/v1/incidents/500812/sar?windowDays=90&refresh=true", nil)
	if rec.Code != 200 || st.lastWindow != 90 || !st.lastRefresh || rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("E10 params: %d %+v %q", rec.Code, st, rec.Header().Get("Cache-Control"))
	}

	st.stale = true
	rec = do(t, h, http.MethodGet, "/api/v1/incidents/500812/sar", nil)
	if rec.Header().Get("Cache-Control") != ccSARStale {
		t.Errorf("stale cache-control %q", rec.Header().Get("Cache-Control"))
	}

	rec = do(t, h, http.MethodGet, "/api/v1/sar/coverage-timeline", nil)
	ex, _ = os.ReadFile(filepath.Join("..", "..", "api", "examples", "sar-coverage-timeline.json"))
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != ccSARTimeln || !reflect.DeepEqual(topKeys(t, rec.Body.Bytes()), topKeys(t, ex)) {
		t.Errorf("E11: %d %q %s", rec.Code, rec.Header().Get("Cache-Control"), rec.Body)
	}
}

func TestSARErrors(t *testing.T) {
	cases := []struct {
		path   string
		err    error
		status int
		code   string
	}{
		{"/api/v1/incidents/abc/sar", nil, 400, CodeInvalidParam},
		{"/api/v1/incidents/1/sar?windowDays=0", nil, 400, CodeInvalidParam},
		{"/api/v1/incidents/1/sar?windowDays=181", nil, 400, CodeInvalidParam},
		{"/api/v1/incidents/1/sar?refresh=maybe", nil, 400, CodeInvalidParam},
		{"/api/v1/incidents/1/sar", fmt.Errorf("x: %w", domain.ErrNotFound), 404, CodeNotFound},
		{"/api/v1/incidents/1/sar", fmt.Errorf("x: %w", domain.ErrUpstream), 502, CodeUpstreamError},
		{"/api/v1/incidents/1/sar", fmt.Errorf("x: %w", domain.ErrUpstreamTimeout), 504, CodeUpstreamTimeout},
		{"/api/v1/incidents/1/sar", fmt.Errorf("x: %w", domain.ErrNotReady), 503, CodeNotReady},
	}
	for _, c := range cases {
		rec := do(t, sarRouter(&stubSAR{err: c.err}), http.MethodGet, c.path, nil)
		if rec.Code != c.status || decodeError(t, rec).Error.Code != c.code {
			t.Errorf("%s (%v): %d %s", c.path, c.err, rec.Code, rec.Body)
		}
	}
	// No SAR service wired → 503 not_ready.
	rec := do(t, NewRouter(Deps{}), http.MethodGet, "/api/v1/sar/coverage-timeline", nil)
	if rec.Code != 503 || decodeError(t, rec).Error.Code != CodeNotReady {
		t.Errorf("nil SAR: %d", rec.Code)
	}
}
