package sources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
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
	// EONETBaseURL is the EONET v3 events endpoint (Guide M9, Detailed §11).
	EONETBaseURL = "https://eonet.gsfc.nasa.gov/api/v3/events"
	// eonetPace spaces network calls. The Guide sets no rate; a full run is
	// only ~27 calls, so this reuses the USGS 250 ms.
	eonetPace = 250 * time.Millisecond
	// eonetTimeout bounds one download. The client timeout covers the whole
	// body, and the 2026 response (17 MB, ~9,500 events, mostly GDACS
	// wildfires) took 91 s on 2026-11-15 and keeps growing through the year.
	eonetTimeout = 30 * time.Minute
	// eonetRefetchYears: a yearly window of the current year or the one
	// before it is downloaded again on every run (see Fetch).
	eonetRefetchYears = 2
	eonetDateLayout   = "2006-01-02"
)

// EONET is the adapter for NASA's Earth Observatory Natural Event Tracker
// (Guide M9). It asks /events?status=all for each calendar year of the
// window and emits one incident per event.
type EONET struct {
	F       *fetch.Fetcher
	Log     *slog.Logger
	BaseURL string           // "" = EONETBaseURL; tests point it at an httptest server
	Now     func() time.Time // nil = time.Now; decides which windows are refetched
}

// NewEONET returns the adapter with its own copy of f: a rate limiter that
// spaces network calls 250 ms apart (cache hits do not wait) and an HTTP
// client whose timeout is at least eonetTimeout. f itself is not changed.
func NewEONET(f *fetch.Fetcher, log *slog.Logger) *EONET {
	fc := *f
	fc.Limiter = rate.NewLimiter(rate.Every(eonetPace), 1)
	fc.HC = atLeastTimeout(fc.HC, eonetTimeout)
	if log == nil {
		log = slog.Default()
	}
	return &EONET{F: &fc, Log: log}
}

func (s *EONET) Name() string { return "eonet" }

// Fetch walks w in calendar-year slices:
// …?status=all&start=YYYY-MM-DD&end=YYYY-MM-DD with an inclusive end. The
// first slice starts at w.From. The last slice ends on the day before w.To
// only when w.To lies before today (UTC); a window that reaches today
// (cmd/ingest sets To = time.Now() at start) asks for its whole year (end=YYYY-12-31; EONET returns the same events
// for a future end), so a full run uses exactly the Guide's URLs and the
// current year's cache file keeps one key instead of a new one each day.
// (A --since window still starts on a different day each run; those files
// are small.)
//
// EONET selects events by their geometry dates and returns every event
// whole (all geometries, not only those inside the window), so an event
// spanning several years is emitted once per year it touches with the same
// content; the runner and the upsert keep the last copy. Slices run oldest
// first, so the freshest response wins.
//
// The disk cache is keyed by URL. A slice of the current year or the year
// before is downloaded again on every run (a copy of the Fetcher with
// Refresh set): its events may still be open, and an event whose last fix
// was in December gets its "closed" date only later, without showing up in
// the next year's response. Older years are served from the cache; only
// the global --refresh downloads them again (e.g. for a long-lived iceberg
// whose newer copy arrives through the refetched years anyway).
func (s *EONET) Fetch(ctx context.Context, w ingest.Window, emit func(domain.Incident) error) error {
	from, to := w.From.UTC(), w.To.UTC()
	if !from.Before(to) {
		return nil
	}
	log := s.log()
	now := s.now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	refetchFrom := now.Year() - eonetRefetchYears + 1
	last := to.Add(-time.Nanosecond)
	var total eonetStats
	for y := from.Year(); y <= last.Year(); y++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		start := time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC)
		if from.After(start) {
			start = from
		}
		end := time.Date(y, 12, 31, 0, 0, 0, 0, time.UTC)
		if last.Before(end) && to.Before(today) {
			end = last
		}
		f := s.F
		if y >= refetchFrom {
			fc := *s.F // same limiter and client; only Refresh differs
			fc.Refresh = true
			f = &fc
		}
		url := s.base() + "?status=all&start=" + start.Format(eonetDateLayout) + "&end=" + end.Format(eonetDateLayout)
		t0 := time.Now()
		st, err := s.window(ctx, f, url, emit)
		if err != nil {
			return err
		}
		log.Info("eonet window", "year", y, "events", st.events, "emitted", st.emitted, "skipped", st.skipped,
			"refetched", f.Refresh, "dur", time.Since(t0).Round(time.Millisecond))
		total.events += st.events
		total.emitted += st.emitted
		total.skipped += st.skipped
	}
	log.Info("eonet done", "events", total.events, "emitted", total.emitted, "skipped", total.skipped)
	return nil
}

func (s *EONET) window(ctx context.Context, f *fetch.Fetcher, url string, emit func(domain.Incident) error) (eonetStats, error) {
	rc, err := f.Get(ctx, s.Name(), url)
	if err != nil {
		return eonetStats{}, err
	}
	defer rc.Close()
	st, err := parseEONET(rc, emit)
	if err != nil {
		return st, fmt.Errorf("eonet %s: %w", url, err)
	}
	return st, nil
}

func (s *EONET) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func (s *EONET) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *EONET) base() string {
	if s.BaseURL != "" {
		return strings.TrimRight(s.BaseURL, "/")
	}
	return EONETBaseURL
}

// eonetStats counts the events of one response.
type eonetStats struct{ events, emitted, skipped int }

// parseEONET streams one response ({"title":…,"events":[…]} in any key
// order), decoding one event at a time, and emits each usable one. Events
// with no id, no geometry, no usable point or an unparsable first date are
// counted in skipped. An emit error stops the parse and is returned as is.
func parseEONET(r io.Reader, emit func(domain.Incident) error) (st eonetStats, err error) {
	dec := json.NewDecoder(r)
	if err := expectDelim(dec, '{', "eonet"); err != nil {
		return st, err
	}
	seen := false
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return st, fmt.Errorf("parse eonet: %w", err)
		}
		if key, _ := tok.(string); key != "events" {
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return st, fmt.Errorf("parse eonet: %w", err)
			}
			continue
		}
		seen = true
		if err := expectDelim(dec, '[', "eonet"); err != nil {
			return st, err
		}
		for dec.More() {
			var raw json.RawMessage // a fresh slice per event: it becomes Incident.Raw
			if err := dec.Decode(&raw); err != nil {
				return st, fmt.Errorf("parse eonet event %d: %w", st.events, err)
			}
			inc, ok, err := eonetIncident(raw)
			if err != nil {
				return st, fmt.Errorf("parse eonet event %d: %w", st.events, err)
			}
			st.events++
			if !ok {
				st.skipped++
				continue
			}
			if err := emit(inc); err != nil {
				return st, err
			}
			st.emitted++
		}
		if err := expectDelim(dec, ']', "eonet"); err != nil {
			return st, err
		}
	}
	if err := expectDelim(dec, '}', "eonet"); err != nil {
		return st, err
	}
	if !seen {
		return st, errors.New("parse eonet: no events array")
	}
	return st, nil
}

// eonetEvent is the part of an EONET v3 event the adapter maps.
type eonetEvent struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Description *string `json:"description"`
	Link        string  `json:"link"`
	Closed      *string `json:"closed"`
	Categories  []struct {
		ID string `json:"id"`
	} `json:"categories"`
	Sources []struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	} `json:"sources"`
	Geometry []eonetGeometry `json:"geometry"`
}

type eonetGeometry struct {
	MagnitudeValue *float64 `json:"magnitudeValue"`
	MagnitudeUnit  *string  `json:"magnitudeUnit"`
	Date           string   `json:"date"`
	Type           string   `json:"type"`
	// Point: [lon, lat]; Polygon: [[[lon, lat], …]] (rings). Decoded lazily.
	Coordinates json.RawMessage `json:"coordinates"`
}

// eonetIncident maps one event (Guide M9). ok is false for an event that
// cannot become an incident.
//
// Decisions where the Guide is silent:
//   - Precision is "day" when the first geometry date is exactly midnight
//     UTC (volcano, iceberg and many fire reports carry dates only),
//     otherwise "second".
//   - magnitude is the largest non-null magnitudeValue over all geometries,
//     with that geometry's magnitudeUnit verbatim (kts, acres, hectare,
//     NM^2): a storm's peak wind, a fire's final size.
//   - Severity: EONET has none. Storms with a kts magnitude follow the
//     Saffir–Simpson split used for IBTrACS (≥ 96 kt, major hurricane → 3;
//     ≥ 64 kt → 2); everything else is 1, like the other sources' default.
//   - The polygon centroid is the mean of the outer ring's vertices with the
//     closing vertex (a repeat of the first) left out.
//   - URL is the first source's page (GDACS report, NHC archive, Smithsonian
//     volcano page …), falling back to the EONET API link.
//   - No country hints: EONET gives none; postprocess places the point.
//   - Title and description are HTML-unescaped ("Ha&#039;apai" → "Ha'apai",
//     "&amp;" → "&"; 108 titles in the 2000→2026 data).
//   - Coordinates are repaired or skipped per geometry (eonetLonLat): a pair
//     in [lat, lon] order is swapped (props.swappedLatLon), an impossible
//     pair is ignored and the next geometry is tried. Start stays the first
//     geometry's date. A clockwise GDACS polygon is read as [lat, lon] too
//     (see eonetPoint).
func eonetIncident(raw json.RawMessage) (inc domain.Incident, ok bool, err error) {
	var e eonetEvent
	if err := json.Unmarshal(raw, &e); err != nil {
		return inc, false, err
	}
	if e.ID == "" || len(e.Geometry) == 0 {
		return inc, false, nil
	}
	start, err := time.Parse(time.RFC3339, e.Geometry[0].Date)
	if err != nil {
		return inc, false, nil
	}
	start = start.UTC()
	gdacs := false
	for _, src := range e.Sources {
		gdacs = gdacs || src.ID == "GDACS"
	}
	lon, lat, swapped, ok := eonetPoint(e.Geometry, gdacs)
	if !ok {
		return inc, false, nil
	}
	cat := ""
	if len(e.Categories) > 0 {
		cat = e.Categories[0].ID
	}
	typ := eonetType(cat)

	inc = domain.Incident{
		Source:        "eonet",
		SourceID:      e.ID,
		Type:          typ,
		Title:         html.UnescapeString(e.Title),
		StartTime:     start,
		TimePrecision: "second",
		Lon:           lon,
		Lat:           lat,
		URL:           e.Link,
		TrackWKT:      eonetTrack(e.Geometry),
		Raw:           raw,
	}
	if start.Equal(start.Truncate(24 * time.Hour)) {
		inc.TimePrecision = "day"
	}
	if e.Description != nil {
		inc.Description = html.UnescapeString(*e.Description)
	}
	if e.Closed != nil {
		if end, err := time.Parse(time.RFC3339, *e.Closed); err == nil {
			end = end.UTC()
			inc.EndTime = &end
		}
	}
	var mag *float64
	for _, g := range e.Geometry {
		if g.MagnitudeValue != nil && (mag == nil || *g.MagnitudeValue > *mag) {
			mag = g.MagnitudeValue
			inc.MagnitudeUnit = ""
			if g.MagnitudeUnit != nil {
				inc.MagnitudeUnit = *g.MagnitudeUnit
			}
		}
	}
	inc.Magnitude = f32ptr(mag)
	inc.Severity = eonetSeverity(typ, mag, inc.MagnitudeUnit)

	srcIDs := []string{}
	for _, src := range e.Sources {
		if src.ID != "" {
			srcIDs = append(srcIDs, src.ID)
		}
	}
	if len(e.Sources) > 0 && e.Sources[0].URL != "" {
		inc.URL = e.Sources[0].URL
	}
	inc.Props = map[string]any{"category": cat, "sources": srcIDs, "link": e.Link, "geometries": len(e.Geometry)}
	if swapped {
		inc.Props["swappedLatLon"] = true
	}
	return inc, true, nil
}

// eonetType maps categories[0].id (Guide M9); unknown ids are other.
func eonetType(cat string) domain.IncidentType {
	switch cat {
	case "wildfires":
		return domain.Wildfire
	case "severeStorms":
		return domain.Storm
	case "volcanoes":
		return domain.Volcano
	case "floods":
		return domain.Flood
	case "landslides":
		return domain.Landslide
	case "earthquakes":
		return domain.Earthquake
	case "drought":
		return domain.Drought
	}
	return domain.Other
}

// eonetSeverity: storms by peak wind in kts (≥ 96 → 3, ≥ 64 → 2), else 1.
func eonetSeverity(typ domain.IncidentType, mag *float64, unit string) domain.Severity {
	if typ == domain.Storm && mag != nil && unit == "kts" {
		switch {
		case *mag >= 96:
			return 3
		case *mag >= 64:
			return 2
		}
	}
	return 1
}

// eonetPoint is the first Point geometry with usable coordinates, or else
// the centroid of the first usable Polygon's outer ring (Guide M9).
// swapped reports that the pair was read as [lat, lon] (see eonetLonLat).
//
// gdacs marks an event with a GDACS source. GDACS flood polygons are real
// shapes written counter-clockwise (RFC 7946); from late June to early
// October 2026 EONET served 171 of them as [lat, lon], which mirrors the
// ring and makes it clockwise in the given order, and most of those are in
// range both ways (Flood in Finland [60.5, 24.7] would land in Iran). So a
// clockwise GDACS ring that is not an axis-aligned box is read swapped. The
// 2015–2018 hand-drawn EO/GDACS boxes are clockwise but correct, hence the
// box exception; non-GDACS polygons are never swapped this way.
func eonetPoint(gs []eonetGeometry, gdacs bool) (lon, lat float64, swapped, ok bool) {
	for _, g := range gs {
		if g.Type != "Point" {
			continue
		}
		if a, b, ok := pointCoords(g.Coordinates); ok {
			if lon, lat, swapped, ok := eonetLonLat(a, b); ok {
				return lon, lat, swapped, true
			}
		}
	}
	for _, g := range gs {
		if g.Type != "Polygon" {
			continue
		}
		if a, b, cw, box, ok := ringCentroid(g.Coordinates); ok {
			if gdacs && cw && !box && validCoordPair(b, a) {
				return b, a, true, true
			}
			if lon, lat, swapped, ok := eonetLonLat(a, b); ok {
				return lon, lat, swapped, true
			}
		}
	}
	return 0, 0, false, false
}

// eonetLonLat checks a GeoJSON [lon, lat] pair. Some EONET records carry
// [lat, lon] instead (the 2026 GDACS flood polygons until ~October, one
// 2017 BC Wildfire point): a pair that is out of range as [lon, lat] but in
// range swapped is returned swapped. Anything else out of range (IRWIN
// points like [189, 7], Typhoon Maria's first fix [151.1, 93.0]) is not ok.
// A swapped pair that happens to be in range both ways (|both| ≤ 90) cannot
// be detected here and is kept as given.
func eonetLonLat(a, b float64) (lon, lat float64, swapped, ok bool) {
	switch {
	case validCoordPair(a, b):
		return a, b, false, true
	case validCoordPair(b, a):
		return b, a, true, true
	}
	return 0, 0, false, false
}

// ringCentroid is the mean of a Polygon's outer-ring vertices, the closing
// vertex (a repeat of the first) left out. cw reports a clockwise ring in
// the given (x, y) order (negative shoelace area); box an axis-aligned
// rectangle (at most two distinct x and two distinct y values).
func ringCentroid(c json.RawMessage) (x, y float64, cw, box, ok bool) {
	var rings [][][]float64
	if json.Unmarshal(c, &rings) != nil || len(rings) == 0 {
		return 0, 0, false, false, false
	}
	ring := rings[0]
	for _, v := range ring {
		if len(v) < 2 {
			return 0, 0, false, false, false
		}
	}
	if n := len(ring); n > 1 && ring[0][0] == ring[n-1][0] && ring[0][1] == ring[n-1][1] {
		ring = ring[:n-1]
	}
	if len(ring) == 0 {
		return 0, 0, false, false, false
	}
	var area float64
	xs, ys := map[float64]bool{}, map[float64]bool{}
	for i, v := range ring {
		x += v[0]
		y += v[1]
		w := ring[(i+1)%len(ring)]
		area += v[0]*w[1] - w[0]*v[1]
		xs[v[0]], ys[v[1]] = true, true
	}
	n := float64(len(ring))
	return x / n, y / n, area < 0, len(xs) <= 2 && len(ys) <= 2, true
}

// pointCoords decodes a GeoJSON Point's [lon, lat, …].
func pointCoords(c json.RawMessage) (lon, lat float64, ok bool) {
	var p []float64
	if json.Unmarshal(c, &p) != nil || len(p) < 2 {
		return 0, 0, false
	}
	return p[0], p[1], true
}

// eonetTrack is a WKT LINESTRING through every usable Point geometry in
// order (repaired or skipped as in eonetLonLat) when there are at least two
// (Guide M9: storm tracks; icebergs get one too), or "". ingest.Finalize
// drops repeated points.
func eonetTrack(gs []eonetGeometry) string {
	var b strings.Builder
	n := 0
	for _, g := range gs {
		if g.Type != "Point" {
			continue
		}
		x, y, ok := pointCoords(g.Coordinates)
		if !ok {
			continue
		}
		lon, lat, _, ok := eonetLonLat(x, y)
		if !ok {
			continue
		}
		if n == 0 {
			b.WriteString("LINESTRING(")
		} else {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(lon, 'f', -1, 64))
		b.WriteByte(' ')
		b.WriteString(strconv.FormatFloat(lat, 'f', -1, 64))
		n++
	}
	if n < 2 {
		return ""
	}
	b.WriteByte(')')
	return b.String()
}
