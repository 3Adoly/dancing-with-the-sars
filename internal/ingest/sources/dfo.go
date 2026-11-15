package sources

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
	"golang.org/x/time/rate"

	"github.com/dwts/backend/internal/domain"
	"github.com/dwts/backend/internal/ingest"
	"github.com/dwts/backend/internal/ingest/fetch"
)

const (
	// DFOURL is the Dartmouth Flood Observatory "Global Archive of Large Flood
	// Events" ArcGIS FeatureServer layer (Guide M11, Detailed §11). The query
	// string is added per page.
	DFOURL = "https://services1.arcgis.com/AXaYBvnJsB5Q7sDF/ArcGIS/rest/services/Global_Archive_of_large_flood_events/FeatureServer/0/query"
	// dfoPageSize is resultRecordCount (Guide M11): resultOffset grows by it.
	dfoPageSize = 1000
	// dfoMaxOffset stops a server that never returns an empty page (the
	// archive has ~5.1k features).
	dfoMaxOffset = 1_000_000
	// dfoPace spaces network calls (the Guide sets no rate; a full run is 6
	// pages).
	dfoPace = 250 * time.Millisecond
	// dfoTopUnmatched is how many unmatched country names the summary logs.
	dfoTopUnmatched = 15
)

// DFO is the Dartmouth Flood Observatory adapter (Guide M11): the Global
// Archive of Large Flood Events, 1985–2021, one incident per feature.
type DFO struct {
	F *fetch.Fetcher
	// Names maps a countries.name (Natural Earth NAME, as stored) to its iso3
	// (ADM0_A3) key. cmd/ingest loads it from the countries table at start;
	// nil means no hints except the aliases.
	Names map[string]string
	Log   *slog.Logger
	URL   string // "" = DFOURL; tests point it at an httptest server
}

// NewDFO returns the adapter with its own copy of f whose rate limiter
// spaces network calls 250 ms apart (cache hits do not wait). f itself is
// not changed.
func NewDFO(f *fetch.Fetcher, names map[string]string, log *slog.Logger) *DFO {
	fc := *f
	fc.Limiter = rate.NewLimiter(rate.Every(dfoPace), 1)
	if log == nil {
		log = slog.Default()
	}
	return &DFO{F: &fc, Names: names, Log: log}
}

func (s *DFO) Name() string { return "dfo" }

// Fetch pages through the whole archive (resultOffset 0, 1000, … until a
// page has no features or no exceededTransferLimit, Detailed §11) and emits
// every flood that began in [w.From, w.To). The service cannot filter by
// date with the Guide's URL, so every run reads all pages.
//
// The archive is frozen (its last flood is from 2021), so the pages are
// downloaded once and then served from the disk cache for every window;
// only the global --refresh downloads them again (no Refresh copy as for
// the live sources). An ArcGIS error answers 200 with an {"error":…} body,
// which the cache keeps: the run fails and says to rerun with --refresh.
func (s *DFO) Fetch(ctx context.Context, w ingest.Window, emit func(domain.Incident) error) error {
	if !w.From.Before(w.To) {
		return nil
	}
	log := s.log()
	if len(s.Names) == 0 {
		log.Warn("dfo: no country name map (is the countries table loaded?); only aliases give country hints")
	}
	base := s.URL
	if base == "" {
		base = DFOURL
	}
	start := time.Now()
	st := newDFOState(s.Names)
	pages := 0
	for off := 0; ; off += dfoPageSize {
		if err := ctx.Err(); err != nil {
			return err
		}
		if off > dfoMaxOffset {
			return fmt.Errorf("dfo: still paging at resultOffset %d; giving up", off)
		}
		url := base + "?where=1%3D1&outFields=*&returnGeometry=false&orderByFields=ID&resultOffset=" +
			strconv.Itoa(off) + "&resultRecordCount=" + strconv.Itoa(dfoPageSize) + "&f=json"
		pg, err := s.page(ctx, url, st, w, emit)
		if err != nil {
			return err
		}
		pages++
		log.Debug("dfo page", "offset", off, "features", pg.features, "exceeded", pg.exceeded)
		if pg.features == 0 || !pg.exceeded {
			break
		}
	}
	log.Info("dfo done", "pages", pages, "rows", st.rows, "emitted", st.emitted, "skipped_window", st.window,
		"skipped_no_id", st.noID, "skipped_bad_time", st.badTime, "skipped_no_location", st.noLocation,
		"duplicate_ids", st.dupIDs, "hinted", st.hinted, "unhinted", st.emitted-st.hinted,
		"unmatched_names", st.topUnmatched(dfoTopUnmatched), "dur", time.Since(start).Round(time.Second))
	return nil
}

// page fetches and parses one page.
func (s *DFO) page(ctx context.Context, url string, st *dfoState, w ingest.Window, emit func(domain.Incident) error) (dfoPage, error) {
	rc, err := s.F.Get(ctx, s.Name(), url)
	if err != nil {
		return dfoPage{}, err
	}
	defer rc.Close()
	pg, err := parseDFOPage(ctx, rc, st, w, emit)
	if err != nil {
		return pg, fmt.Errorf("dfo %s: %w", url, err)
	}
	return pg, nil
}

func (s *DFO) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// dfoState is the per-run state shared by all pages: the country resolver,
// the IDs seen so far and the counters for the summary.
type dfoState struct {
	countries dfoCountries
	seen      map[string]bool
	unmatched map[string]int // display name → rows whose name gave no hint

	rows, emitted int
	window        int // BEGAN outside the window
	noID          int // no attributes or no ID
	badTime       int // no BEGAN
	noLocation    int // no or invalid LONG/LAT, or exactly 0,0
	dupIDs        int // features whose ID an earlier feature already had (still emitted)
	hinted        int // emitted rows with at least one country hint
}

func newDFOState(names map[string]string) *dfoState {
	return &dfoState{countries: newDFOCountries(names), seen: map[string]bool{}, unmatched: map[string]int{}}
}

// topUnmatched formats the n most frequent unmatched names as "name×count".
func (st *dfoState) topUnmatched(n int) string {
	type kv struct {
		k string
		v int
	}
	all := make([]kv, 0, len(st.unmatched))
	for k, v := range st.unmatched {
		all = append(all, kv{k, v})
	}
	slices.SortFunc(all, func(a, b kv) int { return cmp.Or(cmp.Compare(b.v, a.v), cmp.Compare(a.k, b.k)) })
	parts := make([]string, 0, n)
	for i, e := range all {
		if i == n {
			parts = append(parts, fmt.Sprintf("… %d more", len(all)-n))
			break
		}
		parts = append(parts, fmt.Sprintf("%s×%d", e.k, e.v))
	}
	return strings.Join(parts, ", ")
}

// dfoPage is the outcome of parsing one page.
type dfoPage struct {
	features int  // features on the page, usable or not
	exceeded bool // exceededTransferLimit: more pages follow
}

// parseDFOPage streams one FeatureServer query response, decoding one
// feature at a time, and emits each usable one. An {"error":…} body, a body
// with no features array or malformed JSON is an error; an emit error or a
// cancelled ctx stops the parse and is returned as is.
func parseDFOPage(ctx context.Context, r io.Reader, st *dfoState, w ingest.Window, emit func(domain.Incident) error) (pg dfoPage, err error) {
	dec := json.NewDecoder(r)
	if err := expectDelim(dec, '{', "dfo"); err != nil {
		return pg, err
	}
	seen := false
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return pg, fmt.Errorf("parse dfo: %w", err)
		}
		switch key, _ := tok.(string); key {
		case "error":
			var e struct {
				Code    int      `json:"code"`
				Message string   `json:"message"`
				Details []string `json:"details"`
			}
			if err := dec.Decode(&e); err != nil {
				return pg, fmt.Errorf("parse dfo error: %w", err)
			}
			return pg, fmt.Errorf("arcgis error %d: %s %s (the cached page must be fetched again with --refresh)",
				e.Code, e.Message, strings.Join(e.Details, "; "))
		case "exceededTransferLimit":
			if err := dec.Decode(&pg.exceeded); err != nil {
				return pg, fmt.Errorf("parse dfo exceededTransferLimit: %w", err)
			}
		case "features":
			seen = true
			if err := expectDelim(dec, '[', "dfo"); err != nil {
				return pg, err
			}
			for dec.More() {
				if err := ctx.Err(); err != nil {
					return pg, err
				}
				var raw json.RawMessage // a fresh slice per feature: it becomes Incident.Raw
				if err := dec.Decode(&raw); err != nil {
					return pg, fmt.Errorf("parse dfo feature %d: %w", pg.features, err)
				}
				pg.features++
				inc, ok, err := st.incident(raw, w)
				if err != nil {
					return pg, fmt.Errorf("parse dfo feature %d: %w", pg.features-1, err)
				}
				if !ok {
					continue
				}
				if err := emit(inc); err != nil {
					return pg, err
				}
				st.emitted++
				if inc.CountryHints != nil {
					st.hinted++
				}
			}
			if err := expectDelim(dec, ']', "dfo"); err != nil {
				return pg, err
			}
		default:
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return pg, fmt.Errorf("parse dfo: %w", err)
			}
		}
	}
	if err := expectDelim(dec, '}', "dfo"); err != nil {
		return pg, err
	}
	if !seen {
		return pg, errors.New("parse dfo: no features array")
	}
	return pg, nil
}

// dfoAttributes are the feature attributes used (Guide M11). Pointers tell
// a missing or null field from a zero; strings read null as "".
type dfoAttributes struct {
	FID        *int64   `json:"FID"`
	ID         *float64 `json:"ID"` // esriFieldTypeDouble, always whole
	Glide      string   `json:"GLIDENUMBE"`
	Country    string   `json:"COUNTRY"`
	OtherCount string   `json:"OTHERCOUNT"`
	Long       *float64 `json:"LONG"`
	Lat        *float64 `json:"LAT"`
	Area       *float64 `json:"AREA"`
	Began      *float64 `json:"BEGAN"` // epoch ms
	Ended      *float64 `json:"ENDED"` // epoch ms
	Validation string   `json:"VALIDATION"`
	Dead       *float64 `json:"DEAD"`
	Displaced  *float64 `json:"DISPLACED"`
	MainCause  string   `json:"MAINCAUSE"`
	Severity   *float64 `json:"SEVERITY"`
}

// incident maps one feature (Guide M11). ok is false for a feature that is
// skipped (and counted in st).
//
// Decisions where the Guide is silent:
//   - SourceID is the DFO ID. Two IDs occur twice in the archive (278 and
//     4842, different floods or places); the second feature with an ID
//     becomes "<ID>-<FID>" so it keeps its own row instead of overwriting
//     the first (pages are ordered by ID, then served from the cache, so
//     which one is second is stable).
//   - BEGAN/ENDED are UTC midnights: precision "day". ENDED before BEGAN is
//     dropped by Finalize.
//   - LONG/LAT exactly 0,0 (ID 5132, AREA 0) is a missing location, not a
//     flood in the Gulf of Guinea: skipped.
//   - DEAD 0 is "unknown" (carried ruling) → Deaths NULL; DISPLACED 0 is left
//     out of props for the same reason. AREA 0 → no magnitude.
//   - MAINCAUSE "0" or blank → title "Flood, {COUNTRY} ({year})". GLIDE and
//     VALIDATION "0" or blank are left out of props.
//   - The title shows COUNTRY as resolve() cleaned it ("Chinan" → "China");
//     PlaceText (search only) is COUNTRY plus OTHERCOUNT.
//   - Hints: COUNTRY first (the fallback primary country), then each
//     comma-separated OTHERCOUNT name. A name that resolves to nothing is
//     counted in st.unmatched, never an error.
//   - No URL: the archive has no per-event page.
func (st *dfoState) incident(raw json.RawMessage, w ingest.Window) (inc domain.Incident, ok bool, err error) {
	var f struct {
		Attributes *dfoAttributes `json:"attributes"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return inc, false, err
	}
	st.rows++
	a := f.Attributes
	if a == nil || a.ID == nil || math.IsNaN(*a.ID) || math.IsInf(*a.ID, 0) {
		st.noID++
		return inc, false, nil
	}
	id := strconv.FormatFloat(*a.ID, 'f', -1, 64)
	sourceID := id
	if st.seen[id] {
		st.dupIDs++
		if a.FID != nil {
			sourceID = id + "-" + strconv.FormatInt(*a.FID, 10)
		} else {
			sourceID = id + "-" + strconv.Itoa(st.rows)
		}
	}
	st.seen[id] = true

	if a.Began == nil {
		st.badTime++
		return inc, false, nil
	}
	start := time.UnixMilli(int64(*a.Began)).UTC()
	if (!w.From.IsZero() && start.Before(w.From)) || (!w.To.IsZero() && !start.Before(w.To)) {
		st.window++
		return inc, false, nil
	}
	if a.Long == nil || a.Lat == nil || !validCoordPair(*a.Long, *a.Lat) || (*a.Long == 0 && *a.Lat == 0) {
		st.noLocation++
		return inc, false, nil
	}

	hints, country := st.countries.resolve(a.Country)
	if hints == nil && country != "" {
		st.unmatched[country]++
	}
	var others []string
	for _, name := range strings.Split(dfoBlank(a.OtherCount), ",") {
		h, display := st.countries.resolve(name)
		if display == "" {
			continue
		}
		others = append(others, display)
		if h == nil {
			st.unmatched[display]++
		}
		hints = append(hints, h...)
	}

	cause := collapseSpaces(dfoBlank(a.MainCause))
	title := "Flood"
	if cause != "" {
		title = cause + " flood"
	}
	if country != "" {
		title += ", " + country
	}
	title += " (" + strconv.Itoa(start.Year()) + ")"

	inc = domain.Incident{
		Source:        "dfo",
		SourceID:      sourceID,
		Type:          domain.Flood,
		Title:         title,
		StartTime:     start,
		TimePrecision: "day",
		Lon:           *a.Long,
		Lat:           *a.Lat,
		Severity:      dfoSeverity(a.Severity),
		Deaths:        dfoDeaths(a.Dead),
		CountryHints:  dfoDedupe(hints),
		PlaceText:     joinNonEmpty(", ", country, strings.Join(others, ", ")),
		Props:         dfoProps(a),
		Raw:           raw,
	}
	if len(inc.CountryHints) == 0 {
		inc.CountryHints = nil
	}
	if a.Ended != nil {
		end := time.UnixMilli(int64(*a.Ended)).UTC()
		inc.EndTime = &end
	}
	if a.Area != nil && *a.Area > 0 && !math.IsInf(*a.Area, 0) {
		m := float32(*a.Area)
		inc.Magnitude, inc.MagnitudeUnit = &m, "km2"
	}
	return inc, true, nil
}

// dfoBlank is s trimmed, with DFO's "0" placeholder for "none" as "".
func dfoBlank(s string) string {
	s = strings.TrimSpace(s)
	if s == "0" {
		return ""
	}
	return s
}

// dfoSeverity (Guide M11): SEVERITY 1 → 1, 1.5 → 2, 2 → 3; anything else is
// 0 (unknown).
func dfoSeverity(v *float64) domain.Severity {
	if v == nil {
		return 0
	}
	switch *v {
	case 1:
		return 1
	case 1.5:
		return 2
	case 2:
		return 3
	}
	return 0
}

// dfoDeaths is DEAD rounded; 0, negative or missing is nil (DFO writes 0 for
// "unknown", carried ruling).
func dfoDeaths(v *float64) *int32 {
	if v == nil || !(*v >= 1) || *v > math.MaxInt32 {
		return nil
	}
	n := int32(math.Round(*v))
	return &n
}

// dfoProps is {displaced, glide, validation}, leaving out zero, "0" and
// blank values; nil when all are.
func dfoProps(a *dfoAttributes) map[string]any {
	props := map[string]any{}
	if a.Displaced != nil && *a.Displaced >= 1 && *a.Displaced < 1e15 {
		props["displaced"] = int64(math.Round(*a.Displaced))
	}
	if g := dfoBlank(a.Glide); g != "" {
		props["glide"] = g
	}
	if v := collapseSpaces(dfoBlank(a.Validation)); v != "" {
		props["validation"] = v
	}
	if len(props) == 0 {
		return nil
	}
	return props
}

// dfoAliases map a folded (dfoFold) DFO country name to ADM0_A3 codes, for
// names the Natural Earth NAME column does not match: long or old names
// ("Democratic Republic of Congo", "Burma", "Zaire"), parts of a country
// ("England", "Alaska", "Tibet", "Canary Islands"), French overseas
// departments (part of FRA in Natural Earth) and the archive's misspellings.
// They win over the countries map. Names with no single successor today
// (USSR, Soviet Union, Yugoslavia, Czechoslovakia, Virgin Islands) and
// regions (Africa, Europe, Caribbean) stay unmatched on purpose.
var dfoAliases = map[string][]string{
	"usa": {"USA"}, "united states": {"USA"}, "alaska": {"USA"}, "texas": {"USA"}, "hawaii": {"USA"},
	"uk": {"GBR"}, "britain": {"GBR"}, "great britain": {"GBR"}, "england": {"GBR"}, "scotland": {"GBR"},
	"wales": {"GBR"}, "northern ireland": {"GBR"}, "unitd kingdom": {"GBR"},
	"democratic republic of congo": {"COD"}, "democratic republic of the congo": {"COD"},
	"democratic republic congo": {"COD"}, "dr congo": {"COD"}, "drc": {"COD"}, "zaire": {"COD"},
	"republic of congo": {"COG"}, "republic of the congo": {"COG"}, "congo republic": {"COG"}, "congo brazzaville": {"COG"},
	"ivory coast": {"CIV"}, "cote d iavoir": {"CIV"},
	"burma": {"MMR"}, "myanamar": {"MMR"},
	"central african republic": {"CAF"}, "czech republic": {"CZE"}, "dominican republic": {"DOM"},
	"east timor": {"TLS"}, "timor": {"TLS"},
	"bosnia": {"BIH"}, "bosnia and herzegovina": {"BIH"}, "bosnia and herzogovina": {"BIH"},
	"bosnia herzegovina": {"BIH"}, "bosnia herzegovenia": {"BIH"},
	"serbia and montenegro": {"SRB", "MNE"}, "serbia montenegro": {"SRB", "MNE"},
	"macedonia": {"MKD"}, "viet nam": {"VNM"}, "the gambia": {"GMB"}, "south sudan": {"SDS"},
	"solomon islands": {"SLB"}, "comoros islands": {"COM"}, "antigua": {"ATG"}, "antigua and barbuda": {"ATG"},
	"saint vincent": {"VCT"}, "saint vincent and the grenadines": {"VCT"}, "st lucia": {"LCA"},
	"trinidad": {"TTO"}, "tobago": {"TTO"}, "west germany": {"DEU"}, "tasmania": {"AUS"}, "tibet": {"CHN"},
	"siberia": {"RUS"}, "gaza": {"PSX"}, "west bank": {"PSX"}, "united arab emerates": {"ARE"},
	"french polynesia": {"PYF"}, "western sahara": {"SAH"}, "azores": {"PRT"}, "azores islands": {"PRT"},
	"canary islands": {"ESP"}, "reunion": {"FRA"}, "martinique": {"FRA"}, "guadeloupe": {"FRA"},
	"french guiana": {"FRA"}, "swaziland": {"SWZ"}, "cape verde": {"CPV"},
	"kazahkstan": {"KAZ"}, "kazahkistan": {"KAZ"}, "camaroun": {"CMR"}, "camaroon": {"CMR"},
	"guatamala": {"GTM"}, "gutamala": {"GTM"}, "columbia": {"COL"},
	"philipines": {"PHL"}, "phillipines": {"PHL"}, "phillippines": {"PHL"}, "philippine": {"PHL"},
	"bangaldesh": {"BGD"}, "bangledesh": {"BGD"}, "boliva": {"BOL"}, "burkino faso": {"BFA"},
	"el savador": {"SLV"}, "madascar": {"MDG"}, "malayasia": {"MYS"}, "moldava": {"MDA"},
	"papua new gunea": {"PNG"}, "venezulea": {"VEN"}, "zimbawe": {"ZWE"},
}

// dfoCountries resolves DFO country names to ADM0_A3 codes.
type dfoCountries struct {
	names map[string]string // dfoFold(countries.name) → iso3
}

func newDFOCountries(names map[string]string) dfoCountries {
	c := dfoCountries{names: make(map[string]string, len(names))}
	for name, iso3 := range names {
		if k := dfoFold(name); k != "" && iso3 != "" {
			c.names[k] = iso3
		}
	}
	return c
}

// lookup returns the codes for a folded name: the alias first, then the
// countries map.
func (c dfoCountries) lookup(folded string) []string {
	if iso3s, ok := dfoAliases[folded]; ok {
		return slices.Clone(iso3s)
	}
	if iso3 := c.names[folded]; iso3 != "" {
		return []string{iso3}
	}
	return nil
}

// resolve maps one DFO country field to codes and the name to display.
// Matching ignores case, accents and punctuation. In order it tries:
//  1. the whole name;
//  2. a list ("Canada and USA", "Britain, Ireland", "Burma/Myanmar"): each
//     part on its own, codes in order;
//  3. a stray letter or two typed after the name ("Chinan", "Brazilra",
//     "South Korea i"): the name without them, if that is exact.
//
// display is the name with leading and trailing junk removed and spaces
// collapsed ("0Peru" → "Peru", "USA." → "USA", "Chinan" → "China"); it is ""
// for a blank or "0" field. codes is nil when nothing matched.
func (c dfoCountries) resolve(raw string) (codes []string, display string) {
	display = collapseSpaces(strings.TrimFunc(dfoBlank(raw), func(r rune) bool { return !unicode.IsLetter(r) }))
	if display == "" {
		return nil, ""
	}
	if codes := c.lookup(dfoFold(display)); codes != nil {
		return codes, display
	}
	if parts := dfoSplitList(display); len(parts) > 1 {
		for _, p := range parts {
			codes = append(codes, c.trimmed(dfoFold(p))...)
		}
		if codes != nil {
			return dfoDedupe(codes), display
		}
	}
	if codes, cut := c.typo(dfoFold(display)); codes != nil {
		return codes, dfoCutLetters(display, cut)
	}
	return nil, display
}

// trimmed is lookup, then typo.
func (c dfoCountries) trimmed(folded string) []string {
	if codes := c.lookup(folded); codes != nil {
		return codes
	}
	codes, _ := c.typo(folded)
	return codes
}

// typo tries the folded name without a trailing one-letter word ("south
// korea i"), then without its last letter ("chinan", "uka"), then without
// its last two ("brazilra"). At least two letters must remain, four when
// two are cut, so short codes do not swallow words ("ussr" is not "us"
// plus junk). cut is the number of letters removed.
func (c dfoCountries) typo(folded string) (codes []string, cut int) {
	if i := strings.LastIndexByte(folded, ' '); i > 0 && utf8.RuneCountInString(folded[i+1:]) == 1 {
		if codes := c.lookup(folded[:i]); codes != nil {
			return codes, 1
		}
	}
	r := []rune(folded)
	for k := 1; k <= 2 && len(r)-k >= 2*k; k++ {
		if r[len(r)-k-1] == ' ' {
			break
		}
		if codes := c.lookup(string(r[:len(r)-k])); codes != nil {
			return codes, k
		}
	}
	return nil, 0
}

// dfoCutLetters removes the last n letters from s and trims what is left.
func dfoCutLetters(s string, n int) string {
	r := []rune(s)
	for i := len(r) - 1; i >= 0 && n > 0; i-- {
		if unicode.IsLetter(r[i]) {
			r = r[:i]
			n--
		}
	}
	return strings.TrimFunc(string(r), func(r rune) bool { return !unicode.IsLetter(r) })
}

// dfoSplitList splits a list of names on ",", "/", ";" and the word "and".
func dfoSplitList(s string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '/' || r == ';' }) {
		words := strings.Fields(p)
		start := 0
		for i, w := range words {
			if strings.EqualFold(w, "and") {
				if i > start {
					out = append(out, strings.Join(words[start:i], " "))
				}
				start = i + 1
			}
		}
		if start < len(words) {
			out = append(out, strings.Join(words[start:], " "))
		}
	}
	return out
}

// dfoFold is the matching key of a country name: accents removed (NFD
// minus combining marks), lower case, every run of non-letters one space
// ("Côte d'Ivoire" → "cote d ivoire", "Dem. Rep. Congo" → "dem rep congo").
func dfoFold(s string) string {
	var b strings.Builder
	gap := false
	for _, r := range norm.NFD.String(s) {
		switch {
		case unicode.Is(unicode.Mn, r):
		case unicode.IsLetter(r):
			if gap && b.Len() > 0 {
				b.WriteByte(' ')
			}
			gap = false
			b.WriteRune(unicode.ToLower(r))
		default:
			gap = true
		}
	}
	return b.String()
}

// dfoDedupe removes duplicates, keeping first-seen order (slices.Compact only
// drops adjacent repeats).
func dfoDedupe(in []string) []string {
	if in == nil {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := in[:0:0]
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
