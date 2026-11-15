package sources

import (
	"context"
	"encoding/json"
	"errors"
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
	// NOAABaseURL is the NCEI hazard service (Guide M7, Detailed §11).
	NOAABaseURL = "https://www.ngdc.noaa.gov/hazel/hazard-service/api/v1"
	// noaaViewURL is the public event page; Detailed §13 E8 shows its volcano form.
	noaaViewURL = "https://www.ngdc.noaa.gov/hazel/view/hazards"
	// noaaPace spaces network calls. The Guide sets no rate for NOAA; this
	// reuses the USGS 250 ms (a full run is only ~33 pages).
	noaaPace = 250 * time.Millisecond
)

// noaaKind selects one of the three NOAA catalogs.
type noaaKind int

const (
	noaaEQ noaaKind = iota
	noaaTS
	noaaVO
)

// noaaSpec is what differs between the three catalogs.
var noaaSpec = [...]struct {
	name string              // Name() and incidents.source
	path string              // API path under NOAABaseURL
	view string              // hazard segment of the public event page
	typ  domain.IncidentType // incidents.type
	noun string              // title suffix: "{place} {noun} ({year})"
}{
	noaaEQ: {"noaa_eq", "/earthquakes", "earthquake", domain.Earthquake, "earthquake"},
	noaaTS: {"noaa_ts", "/tsunamis/events", "tsunami", domain.Tsunami, "tsunami"},
	noaaVO: {"noaa_vo", "/volcanoes", "volcano", domain.Volcano, "eruption"},
}

// NOAA is the adapter for one NCEI hazard catalog: significant earthquakes
// (noaa_eq), tsunami events (noaa_ts) or significant volcanic eruptions
// (noaa_vo). It walks page=1..totalPages (200 rows a page) of the catalog
// filtered by the window's years.
type NOAA struct {
	F       *fetch.Fetcher
	Log     *slog.Logger
	BaseURL string           // "" = NOAABaseURL; tests point it at an httptest server
	Now     func() time.Time // nil = time.Now; decides whether maxYear is sent
	kind    noaaKind
}

// NewNOAAEarthquakes, NewNOAATsunamis and NewNOAAVolcanoes return the adapter
// with its own copy of f whose rate limiter spaces network calls 250 ms
// apart (cache hits do not wait). f itself is not changed.
func NewNOAAEarthquakes(f *fetch.Fetcher, log *slog.Logger) *NOAA { return newNOAA(f, log, noaaEQ) }
func NewNOAATsunamis(f *fetch.Fetcher, log *slog.Logger) *NOAA    { return newNOAA(f, log, noaaTS) }
func NewNOAAVolcanoes(f *fetch.Fetcher, log *slog.Logger) *NOAA   { return newNOAA(f, log, noaaVO) }

func newNOAA(f *fetch.Fetcher, log *slog.Logger, k noaaKind) *NOAA {
	fc := *f
	fc.Limiter = rate.NewLimiter(rate.Every(noaaPace), 1)
	if log == nil {
		log = slog.Default()
	}
	return &NOAA{F: &fc, Log: log, kind: k}
}

func (s *NOAA) Name() string { return noaaSpec[s.kind].name }

// Fetch emits every catalog row whose year lies in the window's years. NOAA
// filters by whole years only (minYear/maxYear), so the window is widened to
// whole years: a --since 30d run emits the whole current year again, which
// the upsert makes harmless. maxYear is sent only when the window ends
// before the current year, so a full run uses the Guide's URL
// (…?minYear=1900&page=N).
//
// The disk cache is keyed by URL, and an open-ended window (one reaching the
// current year) asks for the same URLs on every run while NOAA keeps adding
// events behind them. Such a run therefore downloads every page again (a
// copy of the Fetcher with Refresh set; ~33 pages for a full run, one or two
// for --since) and overwrites the cache. Only a window that ends before the
// current year is served from the cache.
func (s *NOAA) Fetch(ctx context.Context, w ingest.Window, emit func(domain.Incident) error) error {
	if !w.From.Before(w.To) {
		return nil
	}
	minYear := w.From.UTC().Year()
	maxYear := w.To.UTC().Add(-time.Nanosecond).Year()
	query := "?minYear=" + strconv.Itoa(minYear)
	f := s.F
	if maxYear < s.now().UTC().Year() {
		query += "&maxYear=" + strconv.Itoa(maxYear)
	} else {
		fc := *s.F // same limiter and client; only Refresh differs
		fc.Refresh = true
		f = &fc
	}
	log := s.log()
	var emitted, skipped int
	for page, total := 1, 1; page <= total; page++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		url := s.base() + noaaSpec[s.kind].path + query + "&page=" + strconv.Itoa(page)
		pg, err := s.page(ctx, f, url, minYear, maxYear, emit)
		if err != nil {
			return err
		}
		if pg.totalPages < 0 {
			return fmt.Errorf("%s %s: response has no totalPages", s.Name(), url)
		}
		if page == 1 {
			total = pg.totalPages
		}
		emitted += pg.emitted
		skipped += pg.skipped
		log.Debug(s.Name()+" page", "page", page, "of", total, "emitted", pg.emitted, "skipped", pg.skipped)
		if pg.items == 0 && page < total {
			log.Warn(s.Name()+" page is empty before totalPages; stopping", "page", page, "totalPages", total)
			break
		}
	}
	log.Info(s.Name()+" done", "minYear", minYear, "maxYear", maxYear, "emitted", emitted, "skipped", skipped)
	return nil
}

func (s *NOAA) page(ctx context.Context, f *fetch.Fetcher, url string, minYear, maxYear int, emit func(domain.Incident) error) (noaaPage, error) {
	rc, err := f.Get(ctx, s.Name(), url)
	if err != nil {
		return noaaPage{}, err
	}
	defer rc.Close()
	pg, err := parseNOAAPage(rc, s.kind, minYear, maxYear, emit)
	if err != nil {
		return pg, fmt.Errorf("%s %s: %w", s.Name(), url, err)
	}
	return pg, nil
}

func (s *NOAA) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func (s *NOAA) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *NOAA) base() string {
	if s.BaseURL != "" {
		return strings.TrimRight(s.BaseURL, "/")
	}
	return NOAABaseURL
}

// noaaPage is the outcome of parsing one page.
type noaaPage struct {
	items, emitted, skipped int
	totalPages              int // -1 when the page has none
}

// parseNOAAPage streams one page ({"items":[…],"page":…,"totalPages":…}, in
// any key order), decoding one item at a time, and emits each usable row.
// Rows with no id, no year, no lat/lon, or a year outside
// [minYear, maxYear] (0 = unbounded) are counted in skipped. An emit error
// stops the parse and is returned as is.
func parseNOAAPage(r io.Reader, k noaaKind, minYear, maxYear int, emit func(domain.Incident) error) (pg noaaPage, err error) {
	pg.totalPages = -1
	dec := json.NewDecoder(r)
	if err := expectDelim(dec, '{', "noaa"); err != nil {
		return pg, err
	}
	seen := false
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return pg, fmt.Errorf("parse noaa: %w", err)
		}
		switch key, _ := tok.(string); key {
		case "totalPages":
			if err := dec.Decode(&pg.totalPages); err != nil {
				return pg, fmt.Errorf("parse noaa totalPages: %w", err)
			}
		case "items":
			seen = true
			if err := expectDelim(dec, '[', "noaa"); err != nil {
				return pg, err
			}
			for dec.More() {
				var raw json.RawMessage // a fresh slice per item: it becomes Incident.Raw
				if err := dec.Decode(&raw); err != nil {
					return pg, fmt.Errorf("parse noaa item %d: %w", pg.items, err)
				}
				inc, ok, err := noaaIncident(k, raw, minYear, maxYear)
				if err != nil {
					return pg, fmt.Errorf("parse noaa item %d: %w", pg.items, err)
				}
				pg.items++
				if !ok {
					pg.skipped++
					continue
				}
				if err := emit(inc); err != nil {
					return pg, err
				}
				pg.emitted++
			}
			if err := expectDelim(dec, ']', "noaa"); err != nil {
				return pg, err
			}
		default:
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return pg, fmt.Errorf("parse noaa: %w", err)
			}
		}
	}
	if err := expectDelim(dec, '}', "noaa"); err != nil {
		return pg, err
	}
	if !seen {
		return pg, errors.New("parse noaa: no items array")
	}
	return pg, nil
}

// noaaRecord is the union of the item fields the three catalogs use.
// Pointers tell a missing field from a zero.
type noaaRecord struct {
	ID     *int64   `json:"id"`
	Year   *int     `json:"year"`
	Month  *int     `json:"month"`
	Day    *int     `json:"day"`
	Hour   *int     `json:"hour"`
	Minute *int     `json:"minute"`
	Second *float64 `json:"second"` // fractional, e.g. 24.1

	LocationName string   `json:"locationName"` // eq, ts
	Name         string   `json:"name"`         // vo
	Location     string   `json:"location"`     // vo region
	Country      string   `json:"country"`
	Latitude     *float64 `json:"latitude"`
	Longitude    *float64 `json:"longitude"`

	EqMagnitude    *float64 `json:"eqMagnitude"`
	MaxWaterHeight *float64 `json:"maxWaterHeight"`
	VEI            *float64 `json:"vei"`

	Deaths                 *int64 `json:"deaths"`
	DeathsTotal            *int64 `json:"deathsTotal"`
	DamageAmountOrder      *int   `json:"damageAmountOrder"`
	DeathsAmountOrder      *int   `json:"deathsAmountOrder"`
	DamageAmountOrderTotal *int   `json:"damageAmountOrderTotal"`
	DeathsAmountOrderTotal *int   `json:"deathsAmountOrderTotal"`

	EqDepth               *float64 `json:"eqDepth"`
	Intensity             *float64 `json:"intensity"`
	Injuries              *int64   `json:"injuries"`
	DamageMillionsDollars *float64 `json:"damageMillionsDollars"`
	TsunamiEventID        *int64   `json:"tsunamiEventId"`
	EarthquakeEventID     *int64   `json:"earthquakeEventId"`
	VolcanoEventID        *int64   `json:"volcanoEventId"`
	CauseCode             *int     `json:"causeCode"`
	EventValidity         *int     `json:"eventValidity"`
	TsIntensity           *float64 `json:"tsIntensity"`
	NumRunups             *int     `json:"numRunups"`
	Elevation             *float64 `json:"elevation"`
	Morphology            string   `json:"morphology"`
}

// noaaIncident maps one catalog row (Guide M7). ok is false for a row that
// cannot become an incident or lies outside [minYear, maxYear].
//
// Decisions where the Guide is silent:
//   - The "publish" flag is ignored (Pinatubo 1991 has publish:false).
//   - Titles use locationName (volcanoes: name) with runs of spaces collapsed
//     ("JAPAN:  HONSHU" → "JAPAN: HONSHU"); with no locationName the country
//     is used, with neither the title is "Earthquake (1950)" etc.
//   - Earthquake magnitude has no unit: NOAA's eqMagnitude is a preferred
//     value across mixed scales (Mw, Ms, Mb, unknown).
//   - Missing hour → precision "day"; an hour present → "second" (missing
//     minute/second count as 0). A month outside 1..12 counts as missing, a
//     day that does not exist in its month too.
//   - Country names go to PlaceText (search) and props, not CountryHints:
//     hints take ISO3/ADM0 codes and postprocess places the point anyway.
func noaaIncident(k noaaKind, raw json.RawMessage, minYear, maxYear int) (inc domain.Incident, ok bool, err error) {
	var r noaaRecord
	if err := json.Unmarshal(raw, &r); err != nil {
		return inc, false, err
	}
	if r.ID == nil || r.Year == nil || r.Latitude == nil || r.Longitude == nil {
		return inc, false, nil
	}
	year := *r.Year
	if (minYear != 0 && year < minYear) || (maxYear != 0 && year > maxYear) {
		return inc, false, nil
	}
	start, prec := noaaTime(&r)
	spec := noaaSpec[k]

	place := collapseSpaces(r.LocationName)
	if k == noaaVO {
		place = collapseSpaces(r.Name)
	}
	if place == "" {
		place = collapseSpaces(r.Country)
	}
	title := fmt.Sprintf("%s %s (%d)", place, spec.noun, year)
	if place == "" {
		title = fmt.Sprintf("%s%s (%d)", strings.ToUpper(spec.noun[:1]), spec.noun[1:], year)
	}

	id := strconv.FormatInt(*r.ID, 10)
	inc = domain.Incident{
		Source:        spec.name,
		SourceID:      id,
		Type:          spec.typ,
		Title:         title,
		StartTime:     start,
		TimePrecision: prec,
		Lon:           *r.Longitude,
		Lat:           *r.Latitude,
		Severity:      noaaSeverity(k, &r),
		Deaths:        noaaDeaths(&r),
		URL:           noaaViewURL + "/" + spec.view + "/event-more-info/" + id,
		PlaceText:     joinNonEmpty(", ", collapseSpaces(r.LocationName), collapseSpaces(r.Location), collapseSpaces(r.Country)),
		Props:         noaaProps(k, &r),
		Raw:           raw,
	}
	switch k {
	case noaaEQ:
		inc.Magnitude = f32ptr(r.EqMagnitude)
	case noaaTS:
		inc.Magnitude = f32ptr(r.MaxWaterHeight)
		inc.MagnitudeUnit = "m"
	case noaaVO:
		inc.Name = collapseSpaces(r.Name)
		inc.Magnitude = f32ptr(r.VEI)
		inc.MagnitudeUnit = "VEI"
	}
	return inc, true, nil
}

// noaaTime builds the UTC start from year/month/day/hour/minute/second.
// Missing month → Jan 1, precision "year"; missing day → the 1st, "month";
// missing hour → midnight, "day"; else "second".
func noaaTime(r *noaaRecord) (time.Time, domain.Precision) {
	y := *r.Year
	if r.Month == nil || *r.Month < 1 || *r.Month > 12 {
		return time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC), "year"
	}
	m := time.Month(*r.Month)
	if r.Day == nil || *r.Day < 1 || *r.Day > daysIn(y, m) {
		return time.Date(y, m, 1, 0, 0, 0, 0, time.UTC), "month"
	}
	d := *r.Day
	if r.Hour == nil || *r.Hour < 0 || *r.Hour > 23 {
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC), "day"
	}
	t := time.Date(y, m, d, *r.Hour, 0, 0, 0, time.UTC)
	if r.Minute != nil && *r.Minute >= 0 && *r.Minute < 60 {
		t = t.Add(time.Duration(*r.Minute) * time.Minute)
	}
	if r.Second != nil && *r.Second >= 0 && *r.Second < 61 {
		// Round to the millisecond: 24.1 s is 24.100000000000001 as a float.
		t = t.Add(time.Duration(math.Round(*r.Second*1000)) * time.Millisecond)
	}
	return t, "second"
}

func daysIn(y int, m time.Month) int { return time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day() }

// noaaDeaths is `deaths`, or `deathsTotal` when deaths is missing (Guide M7).
// Out-of-range values are dropped.
func noaaDeaths(r *noaaRecord) *int32 {
	v := r.Deaths
	if v == nil {
		v = r.DeathsTotal
	}
	if v == nil || *v < 0 || *v > math.MaxInt32 {
		return nil
	}
	d := int32(*v)
	return &d
}

// noaaSeverity (Guide M7): the largest damage/deaths amount order, ≥3 → 3,
// 2 → 2, else 1. The *Total variants (which add the secondary effects, e.g.
// a quake's tsunami) count too: tsunami rows often carry only those. For
// volcanoes the VEI rule (≥4 → 3, 3 → 2, else 1) applies as well and the
// higher of the two wins ("or deaths", Detailed §11).
func noaaSeverity(k noaaKind, r *noaaRecord) domain.Severity {
	order := 0
	for _, p := range []*int{r.DamageAmountOrder, r.DeathsAmountOrder, r.DamageAmountOrderTotal, r.DeathsAmountOrderTotal} {
		if p != nil && *p > order {
			order = *p
		}
	}
	sev := domain.Severity(1)
	switch {
	case order >= 3:
		sev = 3
	case order == 2:
		sev = 2
	}
	if k == noaaVO && r.VEI != nil {
		switch {
		case *r.VEI >= 4:
			sev = max(sev, 3)
		case *r.VEI >= 3:
			sev = max(sev, 2)
		}
	}
	return sev
}

// noaaProps keeps the descriptive fields of each catalog. damageAmountOrder
// stays a JSON number: postprocess P6 copies it onto the matching USGS row.
func noaaProps(k noaaKind, r *noaaRecord) map[string]any {
	p := map[string]any{}
	putInt := func(key string, v *int) {
		if v != nil {
			p[key] = *v
		}
	}
	putInt64 := func(key string, v *int64) {
		if v != nil {
			p[key] = *v
		}
	}
	putFloat := func(key string, v *float64) {
		if v != nil {
			p[key] = *v
		}
	}
	if c := collapseSpaces(r.Country); c != "" {
		p["country"] = c
	}
	putInt("damageAmountOrder", r.DamageAmountOrder)
	putInt("deathsAmountOrder", r.DeathsAmountOrder)
	switch k {
	case noaaEQ:
		putFloat("depthKm", r.EqDepth)
		putFloat("intensity", r.Intensity)
		putInt64("injuries", r.Injuries)
		putFloat("damageMillionsDollars", r.DamageMillionsDollars)
		putInt64("tsunamiEventId", r.TsunamiEventID)
	case noaaTS:
		putFloat("eqMagnitude", r.EqMagnitude)
		putInt("causeCode", r.CauseCode)
		putInt("eventValidity", r.EventValidity)
		putFloat("tsIntensity", r.TsIntensity)
		putInt("numRunups", r.NumRunups)
		putInt64("earthquakeEventId", r.EarthquakeEventID)
		putInt64("volcanoEventId", r.VolcanoEventID)
	case noaaVO:
		putFloat("vei", r.VEI)
		putFloat("elevationM", r.Elevation)
		if m := collapseSpaces(r.Morphology); m != "" {
			p["morphology"] = m
		}
		putInt64("tsunamiEventId", r.TsunamiEventID)
	}
	return p
}

func f32ptr(v *float64) *float32 {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
		return nil
	}
	f := float32(*v)
	return &f
}
