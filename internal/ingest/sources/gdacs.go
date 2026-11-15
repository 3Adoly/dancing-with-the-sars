package sources

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/dwts/backend/internal/domain"
	"github.com/dwts/backend/internal/ingest"
	"github.com/dwts/backend/internal/ingest/fetch"
)

const (
	// GDACSBaseURL is the GDACS event API (Guide M12, Detailed §11).
	GDACSBaseURL = "https://www.gdacs.org/gdacsapi/api/events/geteventlist"
	// gdacsQuery is the Guide's query plus alertlevel=Green;Orange;Red:
	// without it GDACS returns only Orange and Red events (checked live,
	// Feb 2023: 3 events vs 400+), yet the Guide maps Green → 1 and expects
	// ~10k rows. EQ and TC are not ingested (Detailed §6.4).
	gdacsQuery = "/SEARCH?eventlist=FL;VO;DR;WF&alertlevel=Green;Orange;Red&pagesize=100"
	gdacsPace  = 500 * time.Millisecond // Guide M12: "be gentle"
	// gdacsMaxPages stops a server that never returns an empty page.
	gdacsMaxPages = 500
)

var gdacsTypes = map[string]domain.IncidentType{
	"FL": domain.Flood, "VO": domain.Volcano, "DR": domain.Drought, "WF": domain.Wildfire,
}

// gdacsISO3Aliases maps GDACS ISO3 codes to Natural Earth ADM0_A3 (carried ruling).
var gdacsISO3Aliases = map[string]string{"SSD": "SDS", "XKX": "KOS", "PSE": "PSX"}

// GDACS is the Global Disaster Alert and Coordination System adapter
// (floods, volcanoes, droughts, wildfires).
type GDACS struct {
	F       *fetch.Fetcher
	Log     *slog.Logger
	BaseURL string           // "" = GDACSBaseURL; tests use an httptest server
	Now     func() time.Time // nil = time.Now; decides which months are "current"
}

// NewGDACS returns the adapter with its own copy of f paced 500 ms between
// network calls (cache hits do not wait). f is not changed.
func NewGDACS(f *fetch.Fetcher, log *slog.Logger) *GDACS {
	fc := *f
	fc.Limiter = rate.NewLimiter(rate.Every(gdacsPace), 1)
	if log == nil {
		log = slog.Default()
	}
	return &GDACS{F: &fc, Log: log}
}

func (s *GDACS) Name() string { return "gdacs" }

// Fetch walks calendar months from w.From's month to w.To's, each page by
// page until a page has no features (a 204 or empty body counts as none).
// GDACS returns every event overlapping the month, so a long drought comes
// back in many months; the upsert keeps the last (newest) copy.
//
// Cache: a month that ended before the current month is cached once (only
// the global --refresh downloads it again). The current month's URLs stay
// the same while GDACS adds events, so they are always downloaded again
// through a Refresh copy of the Fetcher (lesson of Task 9).
func (s *GDACS) Fetch(ctx context.Context, w ingest.Window, emit func(domain.Incident) error) error {
	if !w.From.Before(w.To) {
		return nil
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	n := now().UTC()
	curMonth := time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, time.UTC)
	base := strings.TrimRight(s.BaseURL, "/")
	if base == "" {
		base = GDACSBaseURL
	}
	refresh := *s.F
	refresh.Refresh = true
	from := w.From.UTC()
	total, pages := 0, 0
	for m := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, time.UTC); m.Before(w.To); m = m.AddDate(0, 1, 0) {
		last := m.AddDate(0, 1, -1)
		f := s.F
		if !last.Before(curMonth) {
			f = &refresh
		}
		for page := 1; ; page++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			if page > gdacsMaxPages {
				return fmt.Errorf("gdacs %s: still paging at page %d", m.Format("2006-01"), page)
			}
			url := base + gdacsQuery + "&fromdate=" + m.Format("2006-01-02") + "&todate=" + last.Format("2006-01-02") +
				"&pagenumber=" + strconv.Itoa(page)
			cnt, err := s.page(ctx, f, url, emit)
			if err != nil {
				return err
			}
			pages++
			total += cnt
			if cnt == 0 {
				break
			}
		}
	}
	s.Log.Info("gdacs done", "pages", pages, "emitted", total)
	return nil
}

func (s *GDACS) page(ctx context.Context, f *fetch.Fetcher, url string, emit func(domain.Incident) error) (int, error) {
	rc, err := f.Get(ctx, s.Name(), url)
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	n, err := parseGDACSPage(rc, emit)
	if err != nil {
		return n, fmt.Errorf("gdacs %s: %w", url, err)
	}
	return n, nil
}

type gdacsCountry struct {
	ISO3 string `json:"iso3"`
}

type gdacsFeature struct {
	Geometry struct {
		Type        string          `json:"type"`
		Coordinates json.RawMessage `json:"coordinates"`
	} `json:"geometry"`
	Properties struct {
		EventType   string         `json:"eventtype"`
		EventID     json.Number    `json:"eventid"`
		EpisodeID   json.Number    `json:"episodeid"`
		Name        string         `json:"name"`
		Description string         `json:"description"`
		AlertLevel  string         `json:"alertlevel"`
		AlertScore  *float64       `json:"alertscore"`
		Country     string         `json:"country"`
		FromDate    string         `json:"fromdate"`
		ToDate      string         `json:"todate"`
		ISO3        string         `json:"iso3"`
		Glide       string         `json:"glide"`
		Source      string         `json:"source"`
		Affected    []gdacsCountry `json:"affectedcountries"`
		URL         struct {
			Report string `json:"report"`
		} `json:"url"`
		Severity *struct {
			Severity *float64 `json:"severity"`
			Text     string   `json:"severitytext"`
			Unit     string   `json:"severityunit"`
		} `json:"severitydata"`
	} `json:"properties"`
}

// parseGDACSPage streams one FeatureCollection and returns how many
// features it held (emitted or not). An empty or blank body is no data.
// Features of another type, without an id, start date or point are skipped.
func parseGDACSPage(r io.Reader, emit func(domain.Incident) error) (int, error) {
	br := bufio.NewReader(r)
	if _, err := br.Peek(1); err == io.EOF {
		return 0, nil
	}
	dec := json.NewDecoder(br)
	dec.UseNumber()
	if _, err := dec.Token(); err == io.EOF {
		return 0, nil // whitespace only
	} else if err != nil {
		return 0, fmt.Errorf("parse gdacs: %w", err)
	}
	n := 0
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return n, fmt.Errorf("parse gdacs: %w", err)
		}
		if key, _ := tok.(string); key != "features" {
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return n, fmt.Errorf("parse gdacs: %w", err)
			}
			continue
		}
		if err := expectDelim(dec, '[', "gdacs"); err != nil {
			return n, err
		}
		for dec.More() {
			var raw json.RawMessage
			if err := dec.Decode(&raw); err != nil {
				return n, fmt.Errorf("parse gdacs feature %d: %w", n, err)
			}
			n++
			inc, ok := gdacsIncident(raw)
			if !ok {
				continue
			}
			if err := emit(inc); err != nil {
				return n, err
			}
		}
		if err := expectDelim(dec, ']', "gdacs"); err != nil {
			return n, err
		}
	}
	return n, nil
}

// gdacsIncident maps one feature (Guide M12). Decisions where the Guide is
// silent: dates have no zone and are read as UTC with precision "day";
// titles are `name` with GDACS' empty list items (", ,") removed; the
// point is the feature's Point geometry (GDACS' centroid); magnitude is
// severitydata.severity only when its unit is km2 (drought area).
func gdacsIncident(raw json.RawMessage) (domain.Incident, bool) {
	var f gdacsFeature
	if err := json.Unmarshal(raw, &f); err != nil {
		return domain.Incident{}, false
	}
	p := &f.Properties
	typ, ok := gdacsTypes[strings.ToUpper(p.EventType)]
	if !ok || p.EventID == "" {
		return domain.Incident{}, false
	}
	start, ok := gdacsTime(p.FromDate)
	if !ok {
		return domain.Incident{}, false
	}
	var c []float64
	if f.Geometry.Type != "Point" || json.Unmarshal(f.Geometry.Coordinates, &c) != nil || len(c) < 2 {
		return domain.Incident{}, false
	}
	lon, lat := normLon(c[0]), c[1]
	if !validCoordPair(lon, lat) {
		return domain.Incident{}, false
	}
	title := cleanGDACSList(p.Name)
	if title == "" {
		title = cleanGDACSList(p.Description)
	}
	inc := domain.Incident{
		Source:        "gdacs",
		SourceID:      strings.ToUpper(p.EventType) + "-" + p.EventID.String(),
		Type:          typ,
		Title:         title,
		StartTime:     start,
		TimePrecision: "day",
		Lon:           lon,
		Lat:           lat,
		Severity:      ingest.SeverityFromAlert(p.AlertLevel),
		CountryHints:  gdacsHints(p.ISO3, p.Affected),
		URL:           p.URL.Report,
		PlaceText:     cleanGDACSList(p.Country),
		Raw:           raw,
		Props:         map[string]any{"alertLevel": p.AlertLevel},
	}
	if d := cleanGDACSList(p.Description); d != title {
		inc.Description = d
	}
	if end, ok := gdacsTime(p.ToDate); ok && !end.Before(start) {
		inc.EndTime = &end
	}
	if p.EpisodeID != "" {
		inc.Props["episodeId"] = p.EpisodeID.String()
	}
	if p.Glide != "" {
		inc.Props["glide"] = p.Glide
	}
	if p.AlertScore != nil {
		inc.Props["alertScore"] = *p.AlertScore
	}
	if sd := p.Severity; sd != nil {
		if sd.Text != "" {
			inc.Props["severityText"] = sd.Text
		}
		if sd.Severity != nil && strings.EqualFold(sd.Unit, "km2") && *sd.Severity > 0 && !math.IsInf(*sd.Severity, 0) {
			inc.Magnitude = f32ptr(sd.Severity)
			inc.MagnitudeUnit = "km2"
		}
	}
	return inc, true
}

func gdacsTime(s string) (time.Time, bool) {
	s = strings.TrimSuffix(strings.TrimSpace(s), "Z")
	t, err := time.Parse("2006-01-02T15:04:05", s)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// gdacsHints returns iso3 then every affectedcountries iso3, upper-cased,
// aliased to ADM0_A3 and de-duplicated.
func gdacsHints(iso3 string, aff []gdacsCountry) []string {
	var out []string
	add := func(c string) {
		c = strings.ToUpper(strings.TrimSpace(c))
		if len(c) != 3 {
			return
		}
		if a, ok := gdacsISO3Aliases[c]; ok {
			c = a
		}
		for _, x := range out {
			if x == c {
				return
			}
		}
		out = append(out, c)
	}
	add(iso3)
	for _, a := range aff {
		add(a.ISO3)
	}
	return out
}

// cleanGDACSList drops the empty items GDACS leaves in comma lists
// ("Aruba, , Brazil" → "Aruba, Brazil").
func cleanGDACSList(s string) string {
	parts := strings.Split(collapseSpaces(s), ",")
	return joinNonEmpty(", ", trimAll(parts)...)
}

func trimAll(ss []string) []string {
	for i := range ss {
		ss[i] = strings.TrimSpace(ss[i])
	}
	return ss
}
