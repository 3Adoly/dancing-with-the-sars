package httpapi

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/dwts/backend/internal/domain"
)

// SARAPI is what the SAR handlers need. *service.SARService implements it.
type SARAPI interface {
	DefaultWindowDays() int
	ForIncidentWindow(ctx context.Context, id int64, windowDays int, refresh bool) (domain.SARResult, error)
	CoverageTimeline() domain.SARCoverageTimeline
}

const (
	ccSAR       = "public, max-age=3600"
	ccSARStale  = "public, max-age=60" // a stale fallback should be retried soon
	ccSARTimeln = "public, max-age=86400"
)

// incidentSAR is E10 GET /api/v1/incidents/{id}/sar?windowDays=&refresh=.
func (h *handlers) incidentSAR(w http.ResponseWriter, r *http.Request) {
	if h.d.SAR == nil {
		writeError(w, r, errNotReady())
		return
	}
	id, err := parseID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	q := r.URL.Query()
	window, err := parseIntRange(q, "windowDays", h.d.SAR.DefaultWindowDays(), 1, 180)
	if err != nil {
		writeError(w, r, err)
		return
	}
	refresh, err := parseBool(q, "refresh", false)
	if err != nil {
		writeError(w, r, err)
		return
	}
	res, err := h.d.SAR.ForIncidentWindow(r.Context(), id, window, refresh)
	if err != nil {
		writeError(w, r, err)
		return
	}
	switch {
	case refresh:
		w.Header().Set("Cache-Control", "no-store")
	case res.Stale:
		w.Header().Set("Cache-Control", ccSARStale)
	default:
		w.Header().Set("Cache-Control", ccSAR)
	}
	writeJSON(w, http.StatusOK, res)
}

// sarCoverageTimeline is E11 GET /api/v1/sar/coverage-timeline (static mission table).
func (h *handlers) sarCoverageTimeline(w http.ResponseWriter, r *http.Request) {
	if h.d.SAR == nil {
		writeError(w, r, errNotReady())
		return
	}
	w.Header().Set("Cache-Control", ccSARTimeln)
	writeJSON(w, http.StatusOK, h.d.SAR.CoverageTimeline())
}
