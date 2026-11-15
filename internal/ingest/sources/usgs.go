// Package sources holds one ingest adapter per data source (Guide M6–M12).
// Each adapter fetches through fetch.Fetcher (disk cache, retries), streams
// the payload and emits normalized incidents; ingest.Runner does the rest.
package sources

import (
	"context"
	"encoding/json"
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
	// USGSBaseURL is the FDSN event web service; /count and /query hang off it.
	USGSBaseURL = "https://earthquake.usgs.gov/fdsnws/event/1"
	// usgsMaxPerQuery is the service's hard limit per query (HTTP 400 above it).
	usgsMaxPerQuery = 20000
	// usgsPace is the minimum gap between two network calls (Guide M6: 250 ms).
	usgsPace = 250 * time.Millisecond
	// usgsTimeLayout is how window bounds go into the URL (UTC, no zone suffix).
	usgsTimeLayout = "2006-01-02T15:04:05"
)

// USGS is the earthquake adapter for the USGS FDSN event service (Guide M6,
// Detailed §11). It walks the window in decade, year or month slices, asks
// /count for each and splits any slice over 20,000 events in half before
// querying it.
type USGS struct {
	F       *fetch.Fetcher
	MinMag  float64
	Log     *slog.Logger
	BaseURL string // "" = USGSBaseURL; tests point it at an httptest server
}

// NewUSGS returns the adapter with its own copy of f whose rate limiter
// spaces network calls 250 ms apart. Cache hits do not wait, so a resumed run
// replays already-downloaded windows at disk speed. f itself is not changed.
func NewUSGS(f *fetch.Fetcher, minMag float64, log *slog.Logger) *USGS {
	fc := *f
	fc.Limiter = rate.NewLimiter(rate.Every(usgsPace), 1)
	if log == nil {
		log = slog.Default()
	}
	return &USGS{F: &fc, MinMag: minMag, Log: log}
}

func (s *USGS) Name() string { return "usgs" }

// Fetch emits every event of magnitude ≥ MinMag in w.
func (s *USGS) Fetch(ctx context.Context, w ingest.Window, emit func(domain.Incident) error) error {
	log := s.log()
	for _, win := range usgsWindows(w.From, w.To) {
		err := splitByCount(ctx, win, usgsMaxPerQuery, s.count, func(sub ingest.Window, n int) error {
			emitted, skipped, err := s.query(ctx, sub, emit)
			if err != nil {
				return err
			}
			log.Info("usgs window", "from", sub.From.Format(usgsTimeLayout), "to", sub.To.Format(usgsTimeLayout),
				"count", n, "emitted", emitted, "skipped", skipped)
			if emitted+skipped >= usgsMaxPerQuery {
				log.Warn("usgs window returned the per-query limit; events may be missing",
					"from", sub.From, "to", sub.To, "count", n)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *USGS) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// params is the shared query string: the Guide's parameter order, so the
// cache key of a window never changes between runs.
func (s *USGS) params(w ingest.Window) string {
	return "format=geojson&starttime=" + w.From.UTC().Format(usgsTimeLayout) +
		"&endtime=" + w.To.UTC().Format(usgsTimeLayout) +
		"&minmagnitude=" + strconv.FormatFloat(s.MinMag, 'f', -1, 64)
}

func (s *USGS) base() string {
	if s.BaseURL != "" {
		return strings.TrimRight(s.BaseURL, "/")
	}
	return USGSBaseURL
}

// count asks /count how many events w holds.
func (s *USGS) count(ctx context.Context, w ingest.Window) (int, error) {
	var resp struct {
		Count *int `json:"count"`
	}
	url := s.base() + "/count?" + s.params(w)
	if err := s.F.GetJSON(ctx, s.Name(), url, &resp); err != nil {
		return 0, err
	}
	if resp.Count == nil {
		return 0, fmt.Errorf("usgs count %s: response has no count", url)
	}
	return *resp.Count, nil
}

// query downloads w's events (at most 20,000) and streams them to emit.
func (s *USGS) query(ctx context.Context, w ingest.Window, emit func(domain.Incident) error) (emitted, skipped int, err error) {
	url := s.base() + "/query?" + s.params(w) + "&orderby=time-asc&limit=" + strconv.Itoa(usgsMaxPerQuery)
	rc, err := s.F.Get(ctx, s.Name(), url)
	if err != nil {
		return 0, 0, err
	}
	defer rc.Close()
	emitted, skipped, err = parseUSGS(rc, emit)
	if err != nil {
		return emitted, skipped, fmt.Errorf("usgs %s..%s: %w", w.From.Format(usgsTimeLayout), w.To.Format(usgsTimeLayout), err)
	}
	return emitted, skipped, nil
}

// usgsWindows slices [from, to) at the Guide's boundaries: decades before
// 1960, years 1960–1972, months from 1973. The first and last slices are
// clipped to from and to. Times are taken in UTC.
func usgsWindows(from, to time.Time) []ingest.Window {
	from, to = from.UTC(), to.UTC()
	var out []ingest.Window
	for t := from; t.Before(to); {
		next := usgsNextBoundary(t)
		if next.After(to) {
			next = to
		}
		out = append(out, ingest.Window{From: t, To: next})
		t = next
	}
	return out
}

// usgsNextBoundary is the first slice boundary strictly after t (UTC).
func usgsNextBoundary(t time.Time) time.Time {
	y := t.Year()
	switch {
	case y < 1960:
		return time.Date(y-y%10+10, 1, 1, 0, 0, 0, 0, time.UTC)
	case y < 1973:
		return time.Date(y+1, 1, 1, 0, 0, 0, 0, time.UTC)
	default:
		return time.Date(y, t.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	}
}

// splitByCount calls visit(w, n) with n = count(w) when 0 < n ≤ limit. When n
// exceeds limit it halves w (at a whole second) and recurses on each half in
// order. Empty windows are skipped. A window that cannot be split any
// further while still over limit is an error.
func splitByCount(ctx context.Context, w ingest.Window, limit int,
	count func(context.Context, ingest.Window) (int, error), visit func(ingest.Window, int) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	n, err := count(ctx, w)
	if err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	if n <= limit {
		return visit(w, n)
	}
	mid := w.From.Add(w.To.Sub(w.From) / 2).Truncate(time.Second)
	if !mid.After(w.From) || !mid.Before(w.To) {
		return fmt.Errorf("usgs: cannot split window %s..%s (count %d > %d)", w.From.Format(time.RFC3339), w.To.Format(time.RFC3339), n, limit)
	}
	if err := splitByCount(ctx, ingest.Window{From: w.From, To: mid}, limit, count, visit); err != nil {
		return err
	}
	return splitByCount(ctx, ingest.Window{From: mid, To: w.To}, limit, count, visit)
}

// usgsFeature is the part of a GeoJSON feature the adapter maps. Pointers
// tell a JSON null from a zero.
type usgsFeature struct {
	ID         string `json:"id"`
	Properties struct {
		Mag     *float64 `json:"mag"`
		Place   string   `json:"place"`
		Time    *int64   `json:"time"` // epoch ms, negative before 1970
		URL     string   `json:"url"`
		Felt    *int     `json:"felt"`
		Tsunami *int     `json:"tsunami"`
		Sig     *int     `json:"sig"`
		Alert   *string  `json:"alert"`
		MagType *string  `json:"magType"`
	} `json:"properties"`
	Geometry *struct {
		Coordinates []*float64 `json:"coordinates"` // [lon, lat, depth km]
	} `json:"geometry"`
}

// parseUSGS streams a FeatureCollection, decoding one feature at a time,
// and emits each usable one. Features with no id, a null mag or time, or no
// lon/lat are counted in skipped. An emit error stops the parse and is
// returned as is.
func parseUSGS(r io.Reader, emit func(domain.Incident) error) (emitted, skipped int, err error) {
	dec := json.NewDecoder(r)
	if err := expectDelim(dec, '{', "usgs"); err != nil {
		return 0, 0, err
	}
	seen := false
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return emitted, skipped, fmt.Errorf("parse usgs: %w", err)
		}
		if key, _ := tok.(string); key != "features" {
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return emitted, skipped, fmt.Errorf("parse usgs: %w", err)
			}
			continue
		}
		seen = true
		if err := expectDelim(dec, '[', "usgs"); err != nil {
			return emitted, skipped, err
		}
		for dec.More() {
			var raw json.RawMessage // a fresh slice per feature: it becomes Incident.Raw
			if err := dec.Decode(&raw); err != nil {
				return emitted, skipped, fmt.Errorf("parse usgs feature %d: %w", emitted+skipped, err)
			}
			inc, ok, err := usgsIncident(raw)
			if err != nil {
				return emitted, skipped, fmt.Errorf("parse usgs feature %d: %w", emitted+skipped, err)
			}
			if !ok {
				skipped++
				continue
			}
			if err := emit(inc); err != nil {
				return emitted, skipped, err
			}
			emitted++
		}
		if err := expectDelim(dec, ']', "usgs"); err != nil {
			return emitted, skipped, err
		}
	}
	if err := expectDelim(dec, '}', "usgs"); err != nil {
		return emitted, skipped, err
	}
	if !seen {
		return emitted, skipped, errors.New("parse usgs: no features array")
	}
	return emitted, skipped, nil
}

// expectDelim reads the next token and fails unless it is want. what names
// the payload ("usgs", "noaa") in the error.
func expectDelim(dec *json.Decoder, want json.Delim, what string) error {
	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("parse %s: %w", what, err)
	}
	if d, ok := tok.(json.Delim); !ok || d != want {
		return fmt.Errorf("parse %s: got %v, want %q", what, tok, want)
	}
	return nil
}

// usgsIncident maps one feature (Guide M6). ok is false for a feature that
// cannot become an incident.
func usgsIncident(raw json.RawMessage) (inc domain.Incident, ok bool, err error) {
	var f usgsFeature
	if err := json.Unmarshal(raw, &f); err != nil {
		return inc, false, err
	}
	p := f.Properties
	if f.ID == "" || p.Mag == nil || p.Time == nil || f.Geometry == nil ||
		len(f.Geometry.Coordinates) < 2 || f.Geometry.Coordinates[0] == nil || f.Geometry.Coordinates[1] == nil {
		return inc, false, nil
	}
	mag := *p.Mag
	place := strings.TrimSpace(p.Place)
	title := fmt.Sprintf("M %.1f - %s", mag, place)
	if place == "" {
		title = fmt.Sprintf("M %.1f earthquake", mag)
	}
	alert := ""
	if p.Alert != nil {
		alert = strings.ToLower(strings.TrimSpace(*p.Alert))
	}

	props := map[string]any{}
	if c := f.Geometry.Coordinates; len(c) > 2 && c[2] != nil {
		props["depthKm"] = *c[2]
	}
	if p.Felt != nil {
		props["felt"] = *p.Felt
	}
	if p.Tsunami != nil {
		props["tsunami"] = *p.Tsunami
	}
	if p.Sig != nil {
		props["sig"] = *p.Sig
	}
	if alert != "" {
		props["alert"] = alert
	}

	m32 := float32(mag)
	inc = domain.Incident{
		Source:        "usgs",
		SourceID:      f.ID,
		Type:          domain.Earthquake,
		Title:         title,
		StartTime:     time.UnixMilli(*p.Time).UTC(),
		TimePrecision: "second",
		Lon:           *f.Geometry.Coordinates[0],
		Lat:           *f.Geometry.Coordinates[1],
		Magnitude:     &m32,
		Severity:      usgsSeverity(alert, mag),
		URL:           p.URL,
		PlaceText:     place,
		Props:         props,
		Raw:           raw,
	}
	if p.MagType != nil {
		inc.MagnitudeUnit = *p.MagType
	}
	return inc, true, nil
}

// usgsSeverity: the PAGER alert when there is one (red 3, orange 2,
// yellow/green 1), otherwise by magnitude (≥7 → 3, ≥6 → 2, else 1).
func usgsSeverity(alert string, mag float64) domain.Severity {
	switch strings.ToLower(strings.TrimSpace(alert)) {
	case "red":
		return 3
	case "orange":
		return 2
	case "yellow", "green":
		return 1
	}
	switch {
	case mag >= 7:
		return 3
	case mag >= 6:
		return 2
	}
	return 1
}
