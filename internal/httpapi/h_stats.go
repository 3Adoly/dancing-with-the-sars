package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/dwts/backend/internal/domain"
)

// StatsAPI is what the stats handlers need. *service.StatsService implements it; tests use stubs.
type StatsAPI interface {
	Ready() bool
	Version() int64
	YearBounds() (minYear, maxYear int)
	Meta(ctx context.Context) (domain.Meta, error)
	IncidentTypes() []domain.TypeInfo
	Choropleth(ctx context.Context, q domain.ChoroplethQuery) (domain.Choropleth, error)
	Timeseries(ctx context.Context, q domain.TimeseriesQuery) (domain.Timeseries, error)
}

// Cache-Control values (Plan_Detailed §8.2).
const (
	ccMeta       = "public, max-age=300"
	ccTypes      = "public, max-age=3600"
	ccChoropleth = "public, max-age=300, stale-while-revalidate=3600"
	ccTimeseries = "public, max-age=300"
)

func errNotReady() *APIError {
	return &APIError{Status: http.StatusServiceUnavailable, Code: CodeNotReady,
		Message: "statistics are not loaded yet (no data source or still loading), try again shortly"}
}

// statsReady returns the stats service if it has a loaded cube, else writes 503 not_ready.
func (h *handlers) statsReady(w http.ResponseWriter, r *http.Request) (StatsAPI, bool) {
	if h.d.Stats == nil || !h.d.Stats.Ready() {
		writeError(w, r, errNotReady())
		return nil, false
	}
	return h.d.Stats, true
}

// meta is E2.
func (h *handlers) meta(w http.ResponseWriter, r *http.Request) {
	st, ok := h.statsReady(w, r)
	if !ok {
		return
	}
	if CheckETag(w, r, MakeETag(st.Version(), "meta"), ccMeta) {
		return
	}
	m, err := st.Meta(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// incidentTypes is E3. It works without data (static catalog); the ETag follows the dataset
// version because loaded coverage refines fromYear.
func (h *handlers) incidentTypes(w http.ResponseWriter, r *http.Request) {
	var types []domain.TypeInfo
	var v int64
	if h.d.Stats != nil {
		v = h.d.Stats.Version()
		if CheckETag(w, r, MakeETag(v, "incident-types"), ccTypes) {
			return
		}
		types = h.d.Stats.IncidentTypes()
	} else {
		if CheckETag(w, r, MakeETag(0, "incident-types"), ccTypes) {
			return
		}
		types = domain.TypeCatalog()
	}
	writeJSON(w, http.StatusOK, IncidentTypesResponse{Types: types})
}

// choropleth is E4.
func (h *handlers) choropleth(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	types, err := parseTypes(q, true)
	if err == nil {
		err = ensureSingleUnit(types)
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	// Validate input first (400 regardless of readiness), then require a loaded cube.
	minY, maxY := 1900, time.Now().UTC().Year()
	if h.d.Stats != nil {
		minY, maxY = h.d.Stats.YearBounds()
	}
	years, err := parseYearRange(q, minY, maxY)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if lvl := strings.TrimSpace(q.Get("level")); lvl != "" && lvl != "country" {
		writeError(w, r, BadParam("level", "level must be country"))
		return
	}
	st, ok := h.statsReady(w, r)
	if !ok {
		return
	}
	if CheckETag(w, r, MakeETag(st.Version(), "choropleth", CanonicalQuery(q)), ccChoropleth) {
		return
	}
	res, err := st.Choropleth(r.Context(), domain.ChoroplethQuery{Types: types, Years: years})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// timeseries is E5.
func (h *handlers) timeseries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	types, err := parseTypes(q, true)
	if err == nil {
		err = ensureSingleUnit(types)
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	// Validate input first (400 regardless of readiness), then require a loaded cube.
	minY, maxY := 1900, time.Now().UTC().Year()
	if h.d.Stats != nil {
		minY, maxY = h.d.Stats.YearBounds()
	}
	years, err := parseYearRange(q, minY, maxY)
	if err != nil {
		writeError(w, r, err)
		return
	}
	iso := strings.ToUpper(strings.TrimSpace(q.Get("iso3")))
	if iso != "" && !validISO3(iso) {
		writeError(w, r, BadParam("iso3", "iso3 must be a 3-letter ADM0_A3 code"))
		return
	}
	st, ok := h.statsReady(w, r)
	if !ok {
		return
	}
	if CheckETag(w, r, MakeETag(st.Version(), "timeseries", CanonicalQuery(q)), ccTimeseries) {
		return
	}
	res, err := st.Timeseries(r.Context(), domain.TimeseriesQuery{Types: types, Years: years, ISO3: iso})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func validISO3(s string) bool {
	if len(s) != 3 {
		return false
	}
	for i := 0; i < 3; i++ {
		if c := s[i]; !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
