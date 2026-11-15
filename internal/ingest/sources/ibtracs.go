package sources

import (
	"bufio"
	"context"
	"encoding/csv"
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
	// IBTrACSBaseURL is the directory of the IBTrACS v04r01 CSV files.
	IBTrACSBaseURL = "https://www.ncei.noaa.gov/data/international-best-track-archive-for-climate-stewardship-ibtracs/v04r01/access/csv"
	// ibtracsAllFile is the whole archive as one CSV (Guide M8, Detailed
	// §11), ~333 MB, one row per storm fix, grouped by SID. Cached once.
	ibtracsAllFile = "ibtracs.ALL.list.v04r01.csv"
	// ibtracsRecentFile holds the storms of the last three seasons with the
	// same columns (~12 MB). Recent windows read it, always freshly downloaded.
	ibtracsRecentFile = "ibtracs.last3years.list.v04r01.csv"
	// ibtracsRecentYears: a window whose From lies in the current year or
	// the two before it is served from ibtracsRecentFile.
	ibtracsRecentYears = 3
	// ibtracsViewURL is the IBTrACS storm page (NCICS), suffixed with the SID.
	ibtracsViewURL = "https://ncics.org/ibtracs/index.php?name=v04r01-"
	// ibtracsTimeout bounds one download. The HTTP client timeout covers
	// the whole body, and the ALL file took 30 min to download from this
	// machine (2026-11-15), far past the default 5 minutes.
	ibtracsTimeout = 3 * time.Hour
	// ibtracsMinWindKt: weaker storms (and storms with no wind at all, most
	// of the pre-1950 non-Atlantic records) are skipped (Guide M8).
	ibtracsMinWindKt = 34
	// ibtracsTimeLayout is ISO_TIME, UTC.
	ibtracsTimeLayout = "2006-01-02 15:04:05"
)

// ibtracsColumns are the header names the adapter reads. A file missing any
// of them is rejected.
var ibtracsColumns = []string{"SID", "SEASON", "BASIN", "NAME", "ISO_TIME", "LAT", "LON",
	"WMO_WIND", "WMO_PRES", "TRACK_TYPE", "USA_WIND", "USA_PRES", "USA_SSHS"}

// ibtracsBasins names the BASIN codes for the search text.
var ibtracsBasins = map[string]string{
	"NA": "North Atlantic", "SA": "South Atlantic", "EP": "Eastern North Pacific",
	"WP": "Western North Pacific", "SP": "South Pacific", "SI": "South Indian", "NI": "North Indian",
}

// IBTrACS is the tropical-cyclone adapter (Guide M8): it streams the whole
// IBTrACS archive and emits one storm incident per SID.
type IBTrACS struct {
	F       *fetch.Fetcher
	MinYear int // storms with SEASON < MinYear are skipped
	Log     *slog.Logger
	BaseURL string           // "" = IBTrACSBaseURL; tests point it at an httptest server
	Now     func() time.Time // nil = time.Now; decides which file a window reads
}

// NewIBTrACS returns the adapter with its own copy of f: a rate limiter
// (one call a second; there is only one call) and, when f's HTTP client has
// a timeout shorter than ibtracsTimeout, a copy of that client with the
// longer timeout. f itself is not changed.
func NewIBTrACS(f *fetch.Fetcher, minYear int, log *slog.Logger) *IBTrACS {
	fc := *f
	fc.Limiter = rate.NewLimiter(rate.Every(time.Second), 1)
	fc.HC = atLeastTimeout(fc.HC, ibtracsTimeout)
	if log == nil {
		log = slog.Default()
	}
	return &IBTrACS{F: &fc, MinYear: minYear, Log: log}
}

func (s *IBTrACS) Name() string { return "ibtracs" }

// Fetch emits every storm whose [first fix, last fix] overlaps w.
//
// Which file a window reads (the disk cache is keyed by URL):
//   - A window whose From lies in the current year or the two before it
//     (--since 30d, make ingest-recent) reads the ~12 MB last3years file,
//     downloaded again on every run (a copy of the Fetcher with Refresh set)
//     so new and revised storms of the active seasons always arrive.
//   - Any older window (a full run, --from 1950 ...) reads the ~333 MB ALL
//     file, downloaded once and then served from the cache (Guide M8:
//     "cached once"). Only the global --refresh downloads it again, which
//     is how a full run picks up NCEI's later revisions.
func (s *IBTrACS) Fetch(ctx context.Context, w ingest.Window, emit func(domain.Incident) error) error {
	if !w.From.Before(w.To) {
		return nil
	}
	f, file := s.F, ibtracsAllFile
	if w.From.UTC().Year() > s.now().UTC().Year()-ibtracsRecentYears {
		fc := *s.F // same limiter and client; only Refresh differs
		fc.Refresh = true
		f, file = &fc, ibtracsRecentFile
	}
	base := strings.TrimRight(s.BaseURL, "/")
	if base == "" {
		base = IBTrACSBaseURL
	}
	url := base + "/" + file
	start := time.Now()
	rc, err := f.Get(ctx, s.Name(), url)
	if err != nil {
		return err
	}
	defer rc.Close()
	st, err := parseIBTrACS(ctx, rc, s.MinYear, w, emit)
	if err != nil {
		return fmt.Errorf("ibtracs: %w", err)
	}
	s.log().Info("ibtracs done", "file", file, "storms", st.storms, "emitted", st.emitted, "skipped_season", st.season,
		"skipped_weak", st.weak, "skipped_spur", st.spur, "skipped_window", st.window, "skipped_bad", st.bad,
		"bad_fixes", st.badFixes, "dur", time.Since(start).Round(time.Second))
	return nil
}

func (s *IBTrACS) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func (s *IBTrACS) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// ibtracsStats counts storms (SIDs) by outcome.
type ibtracsStats struct {
	storms, emitted int
	season          int // SEASON < minYear
	weak            int // max wind < 34 kt or no wind at all
	spur            int // TRACK_TYPE spur-*
	window          int // outside the window
	bad             int // bad SEASON or no usable fix
	badFixes        int // rows dropped for a bad ISO_TIME, LAT or LON
}

// ibtracsFix is one usable row. Missing winds/pressures are NaN.
type ibtracsFix struct {
	t                time.Time
	lat, lon         float64
	wmo, usa         float64 // wind, kt
	wmoPres, usaPres float64 // mb
	sshs             int
	hasSSHS          bool
}

// ibtracsStorm accumulates the rows of one SID.
type ibtracsStorm struct {
	sid, season, basin, name, trackType string
	fixes                               []ibtracsFix
}

// parseIBTrACS streams the CSV (header row, units row, then fixes grouped
// by SID) and emits one incident per storm that passes the filters: SEASON
// ≥ minYear, a main or provisional track (not a spur), max wind ≥ 34 kt and
// [first, last fix] overlapping w (a zero w.From / w.To is unbounded). Only
// one storm is in memory at a time. An emit error or a cancelled ctx stops
// the parse and is returned as is.
func parseIBTrACS(ctx context.Context, r io.Reader, minYear int, w ingest.Window, emit func(domain.Incident) error) (st ibtracsStats, err error) {
	cr := csv.NewReader(bufio.NewReaderSize(r, 1<<20))
	cr.ReuseRecord = true
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true

	head, err := cr.Read()
	if err != nil {
		return st, fmt.Errorf("read header: %w", err)
	}
	idx := make(map[string]int, len(head))
	for i, h := range head {
		idx[strings.TrimSpace(strings.TrimPrefix(h, "\ufeff"))] = i
	}
	col := map[string]int{}
	var missing []string
	for _, c := range ibtracsColumns {
		i, ok := idx[c]
		if !ok {
			missing = append(missing, c)
		}
		col[c] = i
	}
	if len(missing) > 0 {
		return st, fmt.Errorf("header lacks columns %s", strings.Join(missing, ", "))
	}
	if _, err := cr.Read(); err != nil { // row 2: units
		if errors.Is(err, io.EOF) {
			return st, nil
		}
		return st, fmt.Errorf("read units row: %w", err)
	}

	var cur *ibtracsStorm
	flush := func() error {
		if cur == nil {
			return nil
		}
		s := cur
		cur = nil
		st.storms++
		inc, reason := ibtracsIncident(s, minYear, w)
		switch reason {
		case "":
		case "season":
			st.season++
			return nil
		case "weak":
			st.weak++
			return nil
		case "spur":
			st.spur++
			return nil
		case "window":
			st.window++
			return nil
		default:
			st.bad++
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := emit(inc); err != nil {
			return err
		}
		st.emitted++
		return nil
	}

	get := func(rec []string, name string) string {
		if i := col[name]; i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}
	for line := 3; ; line++ {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return st, fmt.Errorf("line %d: %w", line, err)
		}
		sid := get(rec, "SID")
		if sid == "" {
			continue
		}
		if cur == nil || cur.sid != sid {
			if err := ctx.Err(); err != nil {
				return st, err
			}
			if err := flush(); err != nil {
				return st, err
			}
			cur = &ibtracsStorm{
				sid:       strings.Clone(sid),
				season:    strings.Clone(get(rec, "SEASON")),
				basin:     strings.Clone(get(rec, "BASIN")),
				name:      strings.Clone(get(rec, "NAME")),
				trackType: strings.Clone(get(rec, "TRACK_TYPE")),
			}
		}
		fx, ok := ibtracsParseFix(get(rec, "ISO_TIME"), get(rec, "LAT"), get(rec, "LON"))
		if !ok {
			st.badFixes++
			continue
		}
		fx.wmo = numOrNaN(get(rec, "WMO_WIND"))
		fx.usa = numOrNaN(get(rec, "USA_WIND"))
		fx.wmoPres = numOrNaN(get(rec, "WMO_PRES"))
		fx.usaPres = numOrNaN(get(rec, "USA_PRES"))
		if v, err := strconv.Atoi(get(rec, "USA_SSHS")); err == nil {
			fx.sshs, fx.hasSSHS = v, true
		}
		cur.fixes = append(cur.fixes, fx)
	}
	if err := ctx.Err(); err != nil {
		return st, err
	}
	return st, flush()
}

func ibtracsParseFix(iso, lat, lon string) (ibtracsFix, bool) {
	t, err := time.Parse(ibtracsTimeLayout, iso)
	if err != nil {
		return ibtracsFix{}, false
	}
	la, err1 := strconv.ParseFloat(lat, 64)
	lo, err2 := strconv.ParseFloat(lon, 64)
	if err1 != nil || err2 != nil || math.IsNaN(la) || math.IsNaN(lo) || la < -90 || la > 90 || lo < -360 || lo > 360 {
		return ibtracsFix{}, false
	}
	return ibtracsFix{t: t.UTC(), lat: la, lon: normLon(lo)}, true
}

// numOrNaN parses a numeric cell; a blank (IBTrACS writes " ") or junk is NaN.
func numOrNaN(s string) float64 {
	if s == "" {
		return math.NaN()
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return math.NaN()
	}
	return v
}

// normLon maps a longitude to [-180, 180] (IBTrACS writes 180–360 for
// storms that cross the antimeridian), rounded to 1e-6° so 181.6 becomes
// -178.4 and not -178.39999999999998.
func normLon(v float64) float64 {
	for v > 180 {
		v -= 360
	}
	for v < -180 {
		v += 360
	}
	return math.Round(v*1e6) / 1e6
}

// ibtracsIncident maps one storm (Guide M8). reason is "" when it becomes an
// incident, else "season", "spur", "weak", "window" or "bad".
//
// Decisions where the Guide is silent (or a controller ruling applies):
//   - One wind agency per storm: WMO_WIND when any fix has one, else
//     USA_WIND, never mixed fix by fix (mixing gave Haiyan 170 kt). The
//     choice is props.windSource; minPressureMb comes from the same agency
//     (WMO_PRES / USA_PRES) and is omitted when that agency has none.
//   - The point is the first fix carrying the max wind.
//   - Spur tracks (TRACK_TYPE spur-merge/-split/-other) are alternative
//     tracks of a storm that has its own main SID; they are skipped so a
//     storm is not counted twice. main, PROVISIONAL and US-PROVISIONAL are kept.
//   - NAME "NOT_NAMED" and "UNNAMED" (the spelling v04r01 actually uses)
//     both mean no name.
//   - {Storm} in the title is "Tropical Storm" below 64 kt; from 64 kt
//     "Hurricane" in NA/EP, "Typhoon" in WP, else "Cyclone". The basin is
//     that of the first fix (genesis basin); it is also props.basin.
//   - props.sshs is the max USA_SSHS as IBTrACS codes it (-5..5; negative
//     codes are non-hurricane stages); omitted when no fix has one.
//   - The track is every 2nd fix plus the last one, so it ends where the
//     storm ends.
//   - Raw stays nil: raw_hash is the md5 of the normalized incident
//     (ruling R-B.2), computed by ingest.Finalize.
//   - The time precision is "second" (ISO_TIME is an exact fix time).
func ibtracsIncident(s *ibtracsStorm, minYear int, w ingest.Window) (domain.Incident, string) {
	var inc domain.Incident
	season, err := strconv.Atoi(s.season)
	if err != nil || len(s.fixes) == 0 {
		return inc, "bad"
	}
	if season < minYear {
		return inc, "season"
	}
	if strings.HasPrefix(strings.ToLower(s.trackType), "spur") {
		return inc, "spur"
	}

	windSource, wind, pres := "WMO_WIND", func(f *ibtracsFix) float64 { return f.wmo }, func(f *ibtracsFix) float64 { return f.wmoPres }
	hasWMO := false
	for i := range s.fixes {
		if !math.IsNaN(s.fixes[i].wmo) {
			hasWMO = true
			break
		}
	}
	if !hasWMO {
		windSource, wind, pres = "USA_WIND", func(f *ibtracsFix) float64 { return f.usa }, func(f *ibtracsFix) float64 { return f.usaPres }
	}
	best, maxWind, minPres := -1, math.Inf(-1), math.Inf(1)
	maxSSHS, hasSSHS := 0, false
	for i := range s.fixes {
		f := &s.fixes[i]
		if v := wind(f); !math.IsNaN(v) && v > maxWind {
			best, maxWind = i, v
		}
		if p := pres(f); !math.IsNaN(p) && p < minPres {
			minPres = p
		}
		if f.hasSSHS && (!hasSSHS || f.sshs > maxSSHS) {
			maxSSHS, hasSSHS = f.sshs, true
		}
	}
	if best < 0 || maxWind < ibtracsMinWindKt {
		return inc, "weak"
	}

	start, end := s.fixes[0].t, s.fixes[len(s.fixes)-1].t
	if end.Before(start) { // fixes are in time order; guard anyway
		start, end = end, start
	}
	if (!w.To.IsZero() && !start.Before(w.To)) || (!w.From.IsZero() && end.Before(w.From)) {
		return inc, "window"
	}

	name := s.name
	if name == "NOT_NAMED" || name == "UNNAMED" {
		name = ""
	}
	title := fmt.Sprintf("Unnamed storm %d (%s)", season, s.basin)
	if name != "" {
		title = fmt.Sprintf("%s %s (%d)", ibtracsClass(s.basin, maxWind), name, season)
	}
	props := map[string]any{"basin": s.basin, "windSource": windSource}
	if !math.IsInf(minPres, 1) {
		props["minPressureMb"] = minPres
	}
	if hasSSHS {
		props["sshs"] = maxSSHS
	}
	mag := float32(maxWind)
	pt := s.fixes[best]
	inc = domain.Incident{
		Source:        "ibtracs",
		SourceID:      s.sid,
		Type:          domain.Storm,
		Title:         title,
		Name:          name,
		StartTime:     start,
		EndTime:       &end,
		TimePrecision: "second",
		Lon:           pt.lon,
		Lat:           pt.lat,
		Magnitude:     &mag,
		MagnitudeUnit: "kt",
		Severity:      ibtracsSeverity(maxSSHS, hasSSHS),
		URL:           ibtracsViewURL + s.sid,
		PlaceText:     ibtracsBasins[s.basin],
		Props:         props,
		TrackWKT:      ibtracsTrack(s.fixes),
	}
	return inc, ""
}

// ibtracsClass is the {Storm} word of the title.
func ibtracsClass(basin string, maxWindKt float64) string {
	if maxWindKt < 64 {
		return "Tropical Storm"
	}
	switch basin {
	case "NA", "EP":
		return "Hurricane"
	case "WP":
		return "Typhoon"
	}
	return "Cyclone"
}

// ibtracsSeverity (Guide M8): max USA_SSHS ≥ 3 → 3, 1–2 → 2, else 1.
func ibtracsSeverity(maxSSHS int, has bool) domain.Severity {
	switch {
	case has && maxSSHS >= 3:
		return 3
	case has && maxSSHS >= 1:
		return 2
	}
	return 1
}

// ibtracsTrack is a WKT LINESTRING of every 2nd fix plus the last one, or
// "" with fewer than two points.
func ibtracsTrack(fixes []ibtracsFix) string {
	if len(fixes) < 2 {
		return ""
	}
	var b strings.Builder
	b.WriteString("LINESTRING(")
	add := func(f ibtracsFix, first bool) {
		if !first {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(f.lon, 'f', -1, 64))
		b.WriteByte(' ')
		b.WriteString(strconv.FormatFloat(f.lat, 'f', -1, 64))
	}
	for i := 0; i < len(fixes); i += 2 {
		add(fixes[i], i == 0)
	}
	if (len(fixes)-1)%2 != 0 {
		add(fixes[len(fixes)-1], false)
	}
	b.WriteByte(')')
	return b.String()
}
