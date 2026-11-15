package ingest

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/dwts/backend/internal/domain"
)

// Reasons Finalize rejects an incident. The runner counts them per reason.
const (
	skipNoKey      = "missing_source_or_id"
	skipNoTitle    = "missing_title"
	skipBadType    = "bad_type"
	skipBadTime    = "bad_time"
	skipBelowYear  = "below_min_year"
	skipBadCoords  = "bad_coords"
	skipBadPrec    = "bad_precision"
	skipBadProps   = "bad_props"
	maxSmallintYr  = 32767
	maxSeverity    = 3
	linestringHead = "LINESTRING"
)

// Finalize normalizes inc in place so the writer can store it. It fills
// Year (UTC), SearchText and RawHash, trims text, converts times to UTC,
// clamps Severity to 0..3, drops impossible optional values (a NaN magnitude,
// negative deaths, an end before the start, a malformed track) and validates
// the coordinates. It returns false when the incident must be skipped: a
// missing source/id/title, an unknown type, an invalid time precision, a
// coordinate outside lon -180..180 / lat -90..90, a start year below minYear,
// or props that do not marshal to JSON.
//
// RawHash is the md5 (salted with hashVersion) of inc.Raw when the adapter
// supplied the compact source record, otherwise of a stable serialization of
// the normalized incident. Raw itself is never persisted (ruling R-B.2).
func Finalize(inc *domain.Incident, minYear int) (ok bool) {
	return finalize(inc, minYear) == ""
}

// finalize is Finalize with the skip reason ("" = ok), for the runner's logs.
func finalize(inc *domain.Incident, minYear int) (skip string) {
	inc.Source = cleanText(inc.Source)
	inc.SourceID = cleanText(inc.SourceID)
	if inc.Source == "" || inc.SourceID == "" {
		return skipNoKey
	}
	inc.Title = cleanText(inc.Title)
	inc.Name = cleanText(inc.Name)
	if inc.Title == "" {
		inc.Title = inc.Name
	}
	if inc.Title == "" {
		return skipNoTitle
	}
	// fire_activity is aggregate-only (fire_cells); incidents.type does not allow it.
	if t, known := domain.ParseType(string(inc.Type)); !known || t == domain.FireActivity {
		return skipBadType
	}

	if inc.TimePrecision == "" {
		inc.TimePrecision = "second"
	}
	switch inc.TimePrecision {
	case "second", "day", "month", "year":
	default:
		return skipBadPrec
	}
	if inc.StartTime.IsZero() {
		return skipBadTime
	}
	inc.StartTime = inc.StartTime.UTC()
	year := inc.StartTime.Year()
	if year < minYear {
		return skipBelowYear
	}
	if year > maxSmallintYr {
		return skipBadTime
	}
	inc.Year = int16(year)
	if inc.EndTime != nil {
		end := inc.EndTime.UTC()
		if end.IsZero() || end.Before(inc.StartTime) {
			inc.EndTime = nil
		} else {
			inc.EndTime = &end
		}
	}

	if !validCoord(inc.Lon, 180) || !validCoord(inc.Lat, 90) {
		return skipBadCoords
	}

	inc.Description = cleanText(inc.Description)
	inc.URL = cleanText(inc.URL)
	inc.MagnitudeUnit = cleanText(inc.MagnitudeUnit)
	inc.PlaceText = cleanText(inc.PlaceText)
	if inc.Severity < 0 {
		inc.Severity = 0
	} else if inc.Severity > maxSeverity {
		inc.Severity = maxSeverity
	}
	if inc.Magnitude != nil && (math.IsNaN(float64(*inc.Magnitude)) || math.IsInf(float64(*inc.Magnitude), 0)) {
		inc.Magnitude = nil
	}
	if inc.Deaths != nil && *inc.Deaths < 0 {
		inc.Deaths = nil
	}
	inc.CountryHints = cleanHints(inc.CountryHints)
	inc.TrackWKT = cleanTrack(inc.TrackWKT)
	if len(inc.Props) == 0 {
		inc.Props = nil // the writer stores {}
	} else {
		inc.Props = cleanProps(inc.Props)
		if _, err := json.Marshal(inc.Props); err != nil {
			return skipBadProps
		}
	}

	inc.SearchText = SearchText(inc.Title, inc.Name, inc.PlaceText, string(inc.Type), strconv.Itoa(year), inc.SourceID)
	inc.RawHash = rawHash(inc)
	return ""
}

// validCoord reports whether v is within ±limit; NaN and ±Inf fail both comparisons.
func validCoord(v, limit float64) bool {
	return v >= -limit && v <= limit
}

// cleanText trims s and makes it storable in a Postgres text column: invalid
// UTF-8 (a Latin-1 source) becomes U+FFFD and NUL bytes, which Postgres
// rejects, are removed.
func cleanText(s string) string {
	return strings.TrimSpace(storable(s))
}

// cleanHints makes the country codes storable, upper-cases, trims and
// de-duplicates them, keeping the source's order (the first hint is the fallback primary country).
func cleanHints(in []string) []string {
	var out []string
	seen := make(map[string]bool, len(in))
	for _, h := range in {
		h = strings.ToUpper(strings.TrimSpace(storable(h)))
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	return out
}

// cleanTrack parses a WKT LINESTRING and returns it in canonical form
// ("LINESTRING(lon lat,lon lat,...)"), or "" when it is unusable, so one
// malformed track cannot fail a whole 5000-row batch in ST_GeomFromText. The
// incident itself is kept either way. Every coordinate must be a finite
// number with lat within ±90; a lon in (180, 360] (some IBTrACS basins) is
// shifted by -360 and must then be within ±180. Consecutive repeated points
// are collapsed and at least two distinct points must remain.
func cleanTrack(wkt string) string {
	wkt = strings.TrimSpace(wkt)
	if len(wkt) < len(linestringHead) || !strings.EqualFold(wkt[:len(linestringHead)], linestringHead) {
		return ""
	}
	body := strings.TrimSpace(wkt[len(linestringHead):])
	if len(body) < 2 || body[0] != '(' || body[len(body)-1] != ')' {
		return ""
	}
	type pt struct{ lon, lat float64 }
	var pts []pt
	for _, pair := range strings.Split(body[1:len(body)-1], ",") {
		f := strings.Fields(pair)
		if len(f) != 2 {
			return ""
		}
		lon, err1 := strconv.ParseFloat(f[0], 64)
		lat, err2 := strconv.ParseFloat(f[1], 64)
		if err1 != nil || err2 != nil {
			return ""
		}
		if lon > 180 && lon <= 360 {
			lon -= 360
		}
		if !validCoord(lon, 180) || !validCoord(lat, 90) {
			return ""
		}
		if n := len(pts); n > 0 && pts[n-1] == (pt{lon, lat}) {
			continue
		}
		pts = append(pts, pt{lon, lat})
	}
	if len(pts) < 2 {
		return ""
	}
	var b strings.Builder
	b.WriteString("LINESTRING(")
	for i, p := range pts {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(p.lon, 'f', -1, 64))
		b.WriteByte(' ')
		b.WriteString(strconv.FormatFloat(p.lat, 'f', -1, 64))
	}
	b.WriteByte(')')
	return b.String()
}

// storable makes s storable in Postgres text and jsonb: invalid UTF-8
// becomes U+FFFD and NUL bytes (rejected by both) are removed. Unlike
// cleanText it does not trim.
func storable(s string) string {
	s = strings.ToValidUTF8(s, "\uFFFD")
	if strings.IndexByte(s, 0) >= 0 {
		s = strings.ReplaceAll(s, "\x00", "")
	}
	return s
}

// cleanProps applies storable to every string (keys and values) inside
// props, recursively through nested maps and slices, in place.
func cleanProps(props map[string]any) map[string]any {
	for k, v := range props {
		ck := storable(k)
		if ck != k {
			delete(props, k)
		}
		props[ck] = cleanValue(v)
	}
	return props
}

func cleanValue(v any) any {
	switch x := v.(type) {
	case string:
		return storable(x)
	case map[string]any:
		return cleanProps(x)
	case []any:
		for i := range x {
			x[i] = cleanValue(x[i])
		}
		return x
	case []string:
		for i := range x {
			x[i] = storable(x[i])
		}
		return x
	case map[string]string:
		out := make(map[string]string, len(x))
		for k, s := range x {
			out[storable(k)] = storable(s)
		}
		return out
	}
	return v
}

// hashVersion salts every raw_hash. Bump it when a mapping changes in a way
// the source record does not show (a new derived field, a different title
// rule), so the next ingest rewrites every row instead of skipping it as
// unchanged.
const hashVersion = "1"

// saltedMD5 is md5(hashVersion NUL b) as hex.
func saltedMD5(b []byte) string {
	h := md5.New()
	h.Write([]byte(hashVersion))
	h.Write([]byte{0})
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}

// rawHash is the salted md5 of inc.Raw when the adapter provided the source
// record, otherwise of a stable JSON serialization of every stored column.
// The fallback is deterministic (struct field order, sorted map keys, UTC
// times), so re-ingesting an unchanged record yields the same hash.
func rawHash(inc *domain.Incident) string {
	if len(inc.Raw) > 0 {
		return saltedMD5(inc.Raw)
	}
	row := struct {
		Source, SourceID, Type, Title, Name, Description string
		Start                                            string
		End                                              *string
		Precision                                        string
		Lon, Lat                                         float64
		Track                                            string
		Magnitude                                        *float32
		Unit                                             string
		Severity                                         int16
		Deaths                                           *int32
		Hints                                            []string
		URL                                              string
		Props                                            map[string]any
		Search                                           string
	}{
		inc.Source, inc.SourceID, string(inc.Type), inc.Title, inc.Name, inc.Description,
		inc.StartTime.UTC().Format("2006-01-02T15:04:05.999999999Z"),
		nil,
		string(inc.TimePrecision), inc.Lon, inc.Lat, inc.TrackWKT,
		inc.Magnitude, inc.MagnitudeUnit, int16(inc.Severity), inc.Deaths,
		inc.CountryHints, inc.URL, inc.Props, inc.SearchText,
	}
	if inc.EndTime != nil {
		e := inc.EndTime.UTC().Format("2006-01-02T15:04:05.999999999Z")
		row.End = &e
	}
	b, err := json.Marshal(row)
	if err != nil { // props were checked by finalize; unreachable for finalized incidents
		b = []byte(inc.Source + "\x00" + inc.SourceID)
	}
	return saltedMD5(b)
}

// asciiFold maps the letters Unicode normalization cannot decompose to the
// ASCII spelling Postgres' unaccent would give.
var asciiFold = strings.NewReplacer(
	"ø", "o", "ł", "l", "đ", "d", "ð", "d", "ß", "ss", "æ", "ae", "œ", "oe", "ı", "i", "þ", "th",
)

// SearchText joins parts into one search document: lower-cased, accents
// stripped (NFD, then every Mn mark removed), whitespace collapsed to single
// spaces. Empty parts vanish. SearchText("Tōhoku M9.1") == "tohoku m9.1".
func SearchText(parts ...string) string {
	joined := strings.ToLower(strings.Join(parts, " "))
	var b strings.Builder
	b.Grow(len(joined))
	for _, r := range norm.NFD.String(joined) {
		if !unicode.Is(unicode.Mn, r) {
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(asciiFold.Replace(b.String())), " ")
}

// SeverityFromAlert maps an alert color (GDACS) to a severity: green 1,
// orange 2, red 3; anything else 0 (unknown).
func SeverityFromAlert(level string) domain.Severity {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "green":
		return 1
	case "orange":
		return 2
	case "red":
		return 3
	}
	return 0
}
