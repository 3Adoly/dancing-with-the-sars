package sources

import (
	"bufio"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/dwts/backend/internal/domain"
	"github.com/dwts/backend/internal/ingest"
	"github.com/dwts/backend/internal/ingest/fetch"
)

const (
	// GLCURL is the NASA Global Landslide Catalog export (Guide M10). It
	// answers 302 with a pre-signed S3 URL; the HTTP client follows it and
	// the disk cache is keyed by this original URL.
	GLCURL = "https://data.nasa.gov/docs/legacy/Global_Landslide_Catalog_Export/Global_Landslide_Catalog_Export_rows.csv"
	// glcDateLayout is event_date (Guide M10).
	glcDateLayout = "01/02/2006 03:04:05 PM"
)

// glcColumns are the header names a row cannot do without; the others
// (description, size, fatalities, country, props) are read when present.
var glcColumns = []string{"event_id", "event_date", "event_title", "longitude", "latitude"}

// glcISO2Aliases are ISO2 codes the countries table cannot resolve
// (carried ruling): Natural Earth gives Taiwan ISO_A2 "CN-TW", so TW maps
// to TWN by hand. Aliases win over the map.
var glcISO2Aliases = map[string]string{"TW": "TWN"}

// GLC is the NASA Global Landslide Catalog adapter (Guide M10): one CSV of
// 11,033 landslides, 1988–2017, one incident per row.
type GLC struct {
	F *fetch.Fetcher
	// ISO2 maps an ISO 3166-1 alpha-2 code to the countries.iso3 (ADM0_A3)
	// key. cmd/ingest loads it from the countries table at start; nil means
	// no hints except the aliases.
	ISO2 map[string]string
	Log  *slog.Logger
	URL  string // "" = GLCURL; tests point it at an httptest server
}

// NewGLC returns the adapter with its own copy of f paced by a rate limiter
// (one call a second; there is only one call). f itself is not changed.
func NewGLC(f *fetch.Fetcher, iso2 map[string]string, log *slog.Logger) *GLC {
	fc := *f
	fc.Limiter = rate.NewLimiter(rate.Every(time.Second), 1)
	if log == nil {
		log = slog.Default()
	}
	return &GLC{F: &fc, ISO2: iso2, Log: log}
}

func (s *GLC) Name() string { return "glc" }

// Fetch emits every landslide whose event_date lies in [w.From, w.To).
//
// The catalog is a frozen 2017 export, so the file is downloaded once and
// then served from the disk cache for every window; only the global
// --refresh downloads it again (no Refresh copy as for the live sources).
func (s *GLC) Fetch(ctx context.Context, w ingest.Window, emit func(domain.Incident) error) error {
	if !w.From.Before(w.To) {
		return nil
	}
	if len(s.ISO2) == 0 {
		s.log().Warn("glc: no ISO2 country map (is the countries table loaded?); only aliases give country hints")
	}
	url := s.URL
	if url == "" {
		url = GLCURL
	}
	start := time.Now()
	rc, err := s.F.Get(ctx, s.Name(), url)
	if err != nil {
		return err
	}
	defer rc.Close()
	st, err := parseGLC(ctx, rc, s.ISO2, w, emit)
	if err != nil {
		return fmt.Errorf("glc: %w", err)
	}
	s.log().Info("glc done", "rows", st.rows, "emitted", st.emitted, "skipped_window", st.window,
		"skipped_bad_date", st.badDate, "skipped_bad_coords", st.badCoords, "skipped_no_id", st.noID,
		"hinted", st.hinted, "no_country_code", st.noCode, "unknown_country_code", st.unknownCode,
		"dur", time.Since(start).Round(time.Second))
	return nil
}

func (s *GLC) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// glcStats counts CSV rows by outcome. hinted, noCode and unknownCode split
// the emitted rows by their country hint.
type glcStats struct {
	rows, emitted int
	window        int // event_date outside the window
	badDate       int
	badCoords     int
	noID          int
	hinted        int // country_code resolved to an ISO3 hint
	noCode        int // blank country_code
	unknownCode   int // country_code neither in the map nor an alias
}

// parseGLC streams the CSV and emits one incident per usable row whose
// event_date lies in [w.From, w.To) (a zero bound is open). Rows with no
// event_id, a date not in glcDateLayout or unusable coordinates are counted
// and skipped. An emit error or a cancelled ctx stops the parse and is
// returned as is.
func parseGLC(ctx context.Context, r io.Reader, iso2 map[string]string, w ingest.Window, emit func(domain.Incident) error) (st glcStats, err error) {
	cr := csv.NewReader(bufio.NewReaderSize(r, 1<<20))
	cr.ReuseRecord = true
	cr.FieldsPerRecord = -1

	head, err := cr.Read()
	if err != nil {
		return st, fmt.Errorf("read header: %w", err)
	}
	col := make(map[string]int, len(head))
	for i, h := range head {
		col[strings.TrimSpace(strings.TrimPrefix(h, "\ufeff"))] = i
	}
	var missing []string
	for _, c := range glcColumns {
		if _, ok := col[c]; !ok {
			missing = append(missing, c)
		}
	}
	if len(missing) > 0 {
		return st, fmt.Errorf("header lacks columns %s", strings.Join(missing, ", "))
	}
	get := func(rec []string, name string) string {
		if i, ok := col[name]; ok && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}

	for line := 2; ; line++ {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			return st, nil
		}
		if err != nil {
			return st, fmt.Errorf("line %d: %w", line, err)
		}
		if err := ctx.Err(); err != nil {
			return st, err
		}
		st.rows++
		id := get(rec, "event_id")
		if id == "" {
			st.noID++
			continue
		}
		t, err := time.Parse(glcDateLayout, get(rec, "event_date"))
		if err != nil {
			st.badDate++
			continue
		}
		if (!w.From.IsZero() && t.Before(w.From)) || (!w.To.IsZero() && !t.Before(w.To)) {
			st.window++
			continue
		}
		lon, err1 := strconv.ParseFloat(get(rec, "longitude"), 64)
		lat, err2 := strconv.ParseFloat(get(rec, "latitude"), 64)
		if err1 != nil || err2 != nil || !validCoordPair(lon, lat) {
			st.badCoords++
			continue
		}

		code := strings.ToUpper(get(rec, "country_code"))
		hints := glcHint(code, iso2)
		inc := domain.Incident{
			Source:        "glc",
			SourceID:      strings.Clone(id),
			Type:          domain.Landslide,
			Title:         strings.Clone(get(rec, "event_title")),
			Description:   strings.Clone(get(rec, "event_description")),
			StartTime:     t,
			TimePrecision: "day",
			Lon:           lon,
			Lat:           lat,
			Severity:      glcSeverity(get(rec, "landslide_size")),
			Deaths:        glcDeaths(get(rec, "fatality_count")),
			CountryHints:  hints,
			URL:           strings.Clone(get(rec, "source_link")),
			PlaceText:     joinNonEmpty(", ", get(rec, "location_description"), get(rec, "admin_division_name"), get(rec, "country_name")),
			Props:         glcProps(get(rec, "landslide_category"), get(rec, "landslide_trigger"), get(rec, "landslide_setting"), get(rec, "location_accuracy")),
		}
		if err := emit(inc); err != nil {
			return st, err
		}
		st.emitted++
		switch {
		case hints != nil:
			st.hinted++
		case code == "":
			st.noCode++
		default:
			st.unknownCode++
		}
	}
}

// Decisions where the Guide is silent:
//   - event_date carries a wall-clock time without a zone (local time at the
//     landslide: Oso is 10:45 AM, the PDT time of the slide). It is stored as
//     if it were UTC, so the calendar day never shifts, with precision "day"
//     (5,533 rows are a bare midnight anyway).
//   - Every row is type landslide, whatever landslide_category says
//     (mudslide, rock_fall, debris_flow, lahar, snow_avalanche ...); the
//     category is props.category.
//   - The place text (search only) is location_description,
//     admin_division_name and country_name.
//   - Raw stays nil: raw_hash is the md5 of the normalized incident
//     (ruling R-B.2), as for ibtracs.

// glcSeverity (Guide M10): catastrophic/very_large → 3, large/medium → 2,
// else 1.
func glcSeverity(size string) domain.Severity {
	switch strings.ToLower(strings.TrimSpace(size)) {
	case "catastrophic", "very_large":
		return 3
	case "large", "medium":
		return 2
	}
	return 1
}

// glcDeaths parses fatality_count; blank or junk is nil (unknown).
func glcDeaths(s string) *int32 {
	v, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return nil
	}
	n := int32(v)
	return &n
}

// glcHint resolves an ISO2 country_code to a one-element hint list: the
// alias first, then the countries map. An unknown or blank code is no hint.
func glcHint(code string, iso2 map[string]string) []string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return nil
	}
	if iso3, ok := glcISO2Aliases[code]; ok {
		return []string{iso3}
	}
	if iso3 := iso2[code]; iso3 != "" {
		return []string{iso3}
	}
	return nil
}

// glcProps is {category, trigger, setting, locationAccuracy} (Guide M10),
// leaving out blank values; nil when all are blank.
func glcProps(category, trigger, setting, accuracy string) map[string]any {
	props := map[string]any{}
	for k, v := range map[string]string{"category": category, "trigger": trigger, "setting": setting, "locationAccuracy": accuracy} {
		if v != "" {
			props[k] = strings.Clone(v)
		}
	}
	if len(props) == 0 {
		return nil
	}
	return props
}
