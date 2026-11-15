package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/dwts/backend/internal/domain"
)

// IncidentsAPI is what E7–E9 need. *service.IncidentService implements it.
type IncidentsAPI interface {
	Get(ctx context.Context, id int64) (domain.Incident, error)
	List(ctx context.Context, f domain.IncidentFilter) ([]domain.IncidentSummary, string, error)
	Search(ctx context.Context, q domain.SearchQuery) ([]domain.IncidentSummary, error)
}

const (
	ccIncidents = "public, max-age=300"
	ccIncident  = "public, max-age=3600"
	ccSearch    = "public, max-age=300"
)

// dataVersion is the dataset version used in ETags (0 when stats are not loaded).
func (h *handlers) dataVersion() int64 {
	if h.d.Stats != nil {
		return h.d.Stats.Version()
	}
	return 0
}

func (h *handlers) yearBounds() (int, int) {
	if h.d.Stats != nil {
		return h.d.Stats.YearBounds()
	}
	return 1900, time.Now().UTC().Year()
}

// incidents is E7.
func (h *handlers) incidents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := domain.IncidentFilter{}
	var err error
	if f.Types, err = parseTypes(q, false); err != nil {
		writeError(w, r, err)
		return
	}
	minY, maxY := h.yearBounds()
	if f.Years, err = parseYearRange(q, minY, maxY); err != nil {
		writeError(w, r, err)
		return
	}
	if f.BBox, err = parseBBox(q, false); err != nil {
		writeError(w, r, err)
		return
	}
	if iso := strings.ToUpper(strings.TrimSpace(q.Get("iso3"))); iso != "" {
		if !validISO3(iso) {
			writeError(w, r, BadParam("iso3", "iso3 must be a 3-letter ADM0_A3 code"))
			return
		}
		f.ISO3 = iso
	}
	if v := strings.TrimSpace(q.Get("minMag")); v != "" {
		m, perr := strconv.ParseFloat(v, 32)
		if perr != nil {
			writeError(w, r, BadParam("minMag", "minMag must be a number"))
			return
		}
		mm := float32(m)
		f.MinMagnitude = &mm
	}
	sev, err := parseIntRange(q, "minSeverity", 0, 0, 3)
	if err != nil {
		writeError(w, r, err)
		return
	}
	f.MinSeverity = domain.Severity(sev)
	if f.Sort, err = parseSort(q); err != nil {
		writeError(w, r, err)
		return
	}
	if f.Limit, err = parseLimit(q, 500, 5000); err != nil {
		writeError(w, r, err)
		return
	}
	if f.Cursor, err = parseCursor(q); err != nil {
		writeError(w, r, err)
		return
	}
	if h.d.Incidents == nil {
		writeError(w, r, errIncidentsNotReady())
		return
	}
	if CheckETag(w, r, MakeETag(h.dataVersion(), "incidents", CanonicalQuery(q)), ccIncidents) {
		return
	}
	items, next, err := h.d.Incidents.List(r.Context(), f)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, IncidentsResponse{Items: items, NextCursor: next, Limit: f.Limit})
}

// incident is E8.
func (h *handlers) incident(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	if h.d.Incidents == nil {
		writeError(w, r, errIncidentsNotReady())
		return
	}
	inc, err := h.d.Incidents.Get(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if CheckETag(w, r, MakeETag(h.dataVersion(), "incident", strconv.FormatInt(id, 10), inc.StoryID), ccIncident) {
		return
	}
	writeJSON(w, http.StatusOK, inc)
}

// search is E9.
func (h *handlers) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	text := strings.TrimSpace(q.Get("q"))
	if n := utf8.RuneCountInString(text); n < 2 || n > 100 {
		writeError(w, r, BadParam("q", "q is required (2–100 characters)"))
		return
	}
	sq := domain.SearchQuery{Q: text}
	var err error
	if sq.Types, err = parseTypes(q, false); err != nil {
		writeError(w, r, err)
		return
	}
	if q.Get("from") != "" || q.Get("to") != "" {
		minY, maxY := h.yearBounds()
		yr, yerr := parseYearRange(q, minY, maxY)
		if yerr != nil {
			writeError(w, r, yerr)
			return
		}
		sq.Years = &yr
	}
	if sq.Limit, err = parseLimit(q, 20, 50); err != nil {
		writeError(w, r, err)
		return
	}
	if h.d.Incidents == nil {
		writeError(w, r, errIncidentsNotReady())
		return
	}
	if CheckETag(w, r, MakeETag(h.dataVersion(), "search", CanonicalQuery(q)), ccSearch) {
		return
	}
	items, err := h.d.Incidents.Search(r.Context(), sq)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, SearchResponse{Q: text, Items: items})
}

func errIncidentsNotReady() *APIError {
	return &APIError{Status: http.StatusServiceUnavailable, Code: CodeNotReady,
		Message: "incident data is not available (no database or incidents fixture configured)"}
}
