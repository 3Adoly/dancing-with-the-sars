package ingest

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/dwts/backend/internal/domain"
)

func TestSearchText(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want string
	}{
		{"accents", []string{"Tōhoku M9.1"}, "tohoku m9.1"},
		{"parts joined and lowered", []string{"M 9.1 - NEAR THE EAST COAST", "", "Honshu, Japan", "earthquake", "2011", "official20110311054624120_30"},
			"m 9.1 - near the east coast honshu, japan earthquake 2011 official20110311054624120_30"},
		{"collapse whitespace", []string{"  a \t b\n\nc  ", "  d"}, "a b c d"},
		{"diacritics", []string{"Éruption du Piton de la Fournaise, Réunion; São Tomé; Çanakkale; Köln"},
			"eruption du piton de la fournaise, reunion; sao tome; canakkale; koln"},
		{"letters that do not decompose", []string{"Łódź Søren Đà Nẵng Straße Œuvre"}, "lodz soren da nang strasse oeuvre"},
		{"precomposed and decomposed agree", []string{"Tōhoku", "Tōhoku"}, "tohoku tohoku"},
		{"non latin survives", []string{"Αθήνα 東日本"}, "αθηνα 東日本"},
		{"empty", nil, ""},
		{"only blanks", []string{"", " ", "\t"}, ""},
	}
	for _, tc := range tests {
		if got := SearchText(tc.in...); got != tc.want {
			t.Errorf("%s: SearchText(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestSeverityFromAlert(t *testing.T) {
	tests := map[string]domain.Severity{
		"green": 1, "Green": 1, "GREEN ": 1, "orange": 2, "Orange": 2, "red": 3, "RED": 3, " red": 3,
		"": 0, "yellow": 0, "purple": 0,
	}
	for in, want := range tests {
		if got := SeverityFromAlert(in); got != want {
			t.Errorf("SeverityFromAlert(%q) = %d, want %d", in, got, want)
		}
	}
}

// valid returns a fresh, normalizable incident.
func valid() domain.Incident {
	mag := float32(9.1)
	deaths := int32(18000)
	end := time.Date(2011, 3, 11, 6, 0, 0, 0, time.UTC)
	return domain.Incident{
		Source: "usgs", SourceID: "official20110311054624120_30", Type: domain.Earthquake,
		Title: "M 9.1 - Tōhoku", StartTime: time.Date(2011, 3, 11, 5, 46, 24, 0, time.UTC), EndTime: &end,
		TimePrecision: "second", Lon: 142.37, Lat: 38.3, Magnitude: &mag, MagnitudeUnit: "Mw", Severity: 3, Deaths: &deaths,
		CountryHints: []string{"jpn"}, Props: map[string]any{"depthKm": 29.0, "sig": 1272},
	}
}

func TestFinalizeFillsDerivedFields(t *testing.T) {
	inc := valid()
	inc.PlaceText = "  Honshu, Japan "
	if !Finalize(&inc, 1900) {
		t.Fatal("valid incident rejected")
	}
	if inc.Year != 2011 {
		t.Errorf("Year = %d, want 2011", inc.Year)
	}
	if want := "m 9.1 - tohoku honshu, japan earthquake 2011 official20110311054624120_30"; inc.SearchText != want {
		t.Errorf("SearchText = %q, want %q", inc.SearchText, want)
	}
	if len(inc.RawHash) != 32 {
		t.Errorf("RawHash = %q, want md5 hex", inc.RawHash)
	}
	if got := inc.CountryHints; len(got) != 1 || got[0] != "JPN" {
		t.Errorf("CountryHints = %v, want [JPN]", got)
	}
}

func TestFinalizeYearIsUTC(t *testing.T) {
	// 2000-01-01 03:00 in UTC+5 is still 1999 in UTC.
	inc := valid()
	inc.StartTime = time.Date(2000, 1, 1, 3, 0, 0, 0, time.FixedZone("x", 5*3600))
	if !Finalize(&inc, 1900) || inc.Year != 1999 || inc.StartTime.Location() != time.UTC {
		t.Errorf("Year = %d, loc = %v, want 1999 in UTC", inc.Year, inc.StartTime.Location())
	}
	// Negative (pre-1970) years work too.
	inc = valid()
	inc.StartTime = time.Date(1906, 1, 31, 15, 36, 10, 0, time.UTC)
	if !Finalize(&inc, 1900) || inc.Year != 1906 {
		t.Errorf("Year = %d, want 1906", inc.Year)
	}
}

func TestFinalizeSkips(t *testing.T) {
	nan := math.NaN()
	tests := []struct {
		name   string
		mut    func(*domain.Incident)
		reason string
	}{
		{"no source id", func(i *domain.Incident) { i.SourceID = " " }, skipNoKey},
		{"no source", func(i *domain.Incident) { i.Source = "" }, skipNoKey},
		{"no title or name", func(i *domain.Incident) { i.Title, i.Name = " ", "" }, skipNoTitle},
		{"unknown type", func(i *domain.Incident) { i.Type = "meteor" }, skipBadType},
		{"fire_activity is not an incident", func(i *domain.Incident) { i.Type = domain.FireActivity }, skipBadType},
		{"bad precision", func(i *domain.Incident) { i.TimePrecision = "hour" }, skipBadPrec},
		{"zero time", func(i *domain.Incident) { i.StartTime = time.Time{} }, skipBadTime},
		{"below min year", func(i *domain.Incident) { i.StartTime = time.Date(1899, 12, 31, 23, 59, 59, 0, time.UTC) }, skipBelowYear},
		{"lon too big", func(i *domain.Incident) { i.Lon = 180.5 }, skipBadCoords},
		{"lon too small", func(i *domain.Incident) { i.Lon = -181 }, skipBadCoords},
		{"lat too big", func(i *domain.Incident) { i.Lat = 90.01 }, skipBadCoords},
		{"lat NaN", func(i *domain.Incident) { i.Lat = nan }, skipBadCoords},
		{"lon Inf", func(i *domain.Incident) { i.Lon = math.Inf(1) }, skipBadCoords},
		{"props that cannot marshal", func(i *domain.Incident) { i.Props = map[string]any{"x": nan} }, skipBadProps},
	}
	for _, tc := range tests {
		inc := valid()
		tc.mut(&inc)
		if got := finalize(&inc, 1900); got != tc.reason {
			t.Errorf("%s: skip reason = %q, want %q", tc.name, got, tc.reason)
		}
		inc = valid()
		tc.mut(&inc)
		if Finalize(&inc, 1900) {
			t.Errorf("%s: Finalize returned true", tc.name)
		}
	}
}

func TestFinalizeKeepsBoundaryValues(t *testing.T) {
	for _, c := range [][2]float64{{180, 90}, {-180, -90}, {0, 0}} {
		inc := valid()
		inc.Lon, inc.Lat = c[0], c[1]
		if !Finalize(&inc, 1900) {
			t.Errorf("lon/lat %v must be accepted", c)
		}
	}
	inc := valid()
	inc.StartTime = time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)
	if !Finalize(&inc, 1900) {
		t.Error("year == minYear must be accepted")
	}
}

func TestFinalizeTitleFallsBackToName(t *testing.T) {
	inc := valid()
	inc.Title, inc.Name = "", "Pinatubo"
	if !Finalize(&inc, 1900) || inc.Title != "Pinatubo" {
		t.Errorf("Title = %q, want the name as fallback", inc.Title)
	}
}

func TestFinalizeClamps(t *testing.T) {
	inc := valid()
	inc.Severity = 7
	nan := float32(math.NaN())
	inc.Magnitude = &nan
	neg := int32(-3)
	inc.Deaths = &neg
	early := inc.StartTime.Add(-time.Hour)
	inc.EndTime = &early
	inc.TimePrecision = ""
	inc.CountryHints = []string{" fra", "FRA", "", "deu"}
	inc.TrackWKT = "POINT(1 2)"
	inc.Props = map[string]any{}
	if !Finalize(&inc, 1900) {
		t.Fatal("rejected")
	}
	if inc.Severity != 3 {
		t.Errorf("Severity = %d, want clamped to 3", inc.Severity)
	}
	if inc.Magnitude != nil {
		t.Errorf("NaN magnitude must be dropped, got %v", *inc.Magnitude)
	}
	if inc.Deaths != nil {
		t.Errorf("negative deaths must be dropped, got %d", *inc.Deaths)
	}
	if inc.EndTime != nil {
		t.Errorf("an end before the start must be dropped, got %v", inc.EndTime)
	}
	if inc.TimePrecision != "second" {
		t.Errorf("TimePrecision = %q, want default second", inc.TimePrecision)
	}
	if want := []string{"FRA", "DEU"}; len(inc.CountryHints) != 2 || inc.CountryHints[0] != want[0] || inc.CountryHints[1] != want[1] {
		t.Errorf("CountryHints = %v, want %v", inc.CountryHints, want)
	}
	if inc.TrackWKT != "" {
		t.Errorf("a non-LINESTRING track must be dropped, got %q", inc.TrackWKT)
	}
	if inc.Props != nil {
		t.Errorf("empty props normalize to nil, got %v", inc.Props)
	}

	inc = valid()
	inc.Severity = -2
	inc.TrackWKT = "LINESTRING(0 0,1 1)"
	if !Finalize(&inc, 1900) || inc.Severity != 0 || inc.TrackWKT != "LINESTRING(0 0,1 1)" {
		t.Errorf("Severity = %d, TrackWKT = %q", inc.Severity, inc.TrackWKT)
	}
	inc = valid()
	inc.TrackWKT = "LINESTRING(0 0)" // one point: ST_GeomFromText would fail the batch
	Finalize(&inc, 1900)
	if inc.TrackWKT != "" {
		t.Errorf("a one-point track must be dropped, got %q", inc.TrackWKT)
	}
}

func TestRawHashFromRawWhenPresent(t *testing.T) {
	inc := valid()
	inc.Raw = json.RawMessage(`{"id":"x","mag":9.1}`)
	if !Finalize(&inc, 1900) {
		t.Fatal("rejected")
	}
	sum := md5.Sum(append([]byte(hashVersion+"\x00"), inc.Raw...))
	if want := hex.EncodeToString(sum[:]); inc.RawHash != want {
		t.Errorf("RawHash = %s, want md5(hashVersion NUL Raw) = %s", inc.RawHash, want)
	}
	plain := md5.Sum(inc.Raw)
	if inc.RawHash == hex.EncodeToString(plain[:]) {
		t.Error("RawHash must be salted with hashVersion, not the bare md5(Raw)")
	}
	// Normalized fields do not matter when Raw is present.
	other := valid()
	other.Title = "completely different"
	other.Raw = inc.Raw
	Finalize(&other, 1900)
	if other.RawHash != inc.RawHash {
		t.Error("the same Raw must give the same RawHash")
	}
}

func TestRawHashFallbackIsStableAndSensitive(t *testing.T) {
	hash := func(mut func(*domain.Incident)) string {
		inc := valid()
		// Many keys so map iteration order would show up if the serialization were unstable.
		inc.Props = map[string]any{}
		for _, k := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
			inc.Props[k] = k
		}
		if mut != nil {
			mut(&inc)
		}
		if !Finalize(&inc, 1900) {
			t.Fatal("rejected")
		}
		return inc.RawHash
	}
	base := hash(nil)
	for range 20 {
		if got := hash(nil); got != base {
			t.Fatalf("hash changed between identical incidents: %s vs %s", got, base)
		}
	}
	// A different zone for the same instant is the same incident.
	if got := hash(func(i *domain.Incident) { i.StartTime = i.StartTime.In(time.FixedZone("x", 3*3600)) }); got != base {
		t.Error("the same instant in another zone must hash the same")
	}
	changes := map[string]func(*domain.Incident){
		"title":     func(i *domain.Incident) { i.Title += "!" },
		"magnitude": func(i *domain.Incident) { m := float32(9.2); i.Magnitude = &m },
		"lon":       func(i *domain.Incident) { i.Lon += 0.001 },
		"props":     func(i *domain.Incident) { i.Props["a"] = "changed" },
		"hints":     func(i *domain.Incident) { i.CountryHints = []string{"CHN"} },
		"end":       func(i *domain.Incident) { e := i.StartTime.Add(time.Hour); i.EndTime = &e },
		"severity":  func(i *domain.Incident) { i.Severity = 1 },
		"track":     func(i *domain.Incident) { i.TrackWKT = "LINESTRING(0 0,1 1)" },
	}
	for name, mut := range changes {
		if hash(mut) == base {
			t.Errorf("changing %s must change the hash", name)
		}
	}
}

func TestFinalizeIsIdempotent(t *testing.T) {
	inc := valid()
	inc.PlaceText = "Honshu"
	Finalize(&inc, 1900)
	first := inc
	if !Finalize(&inc, 1900) {
		t.Fatal("second Finalize rejected")
	}
	if inc.SearchText != first.SearchText || inc.RawHash != first.RawHash || inc.Year != first.Year {
		t.Errorf("Finalize twice changed the result:\n%+v\n%+v", first, inc)
	}
}

func TestFinalizeMakesTextStorable(t *testing.T) {
	inc := valid()
	inc.Title = "  Caf\xe9 flood\x00 in  Lat-1 \n" // Latin-1 byte and a NUL: Postgres text rejects both
	inc.URL = " https://example.org/x "
	if !Finalize(&inc, 1900) {
		t.Fatal("rejected")
	}
	if want := "Caf� flood in  Lat-1"; inc.Title != want {
		t.Errorf("Title = %q, want %q", inc.Title, want)
	}
	if inc.URL != "https://example.org/x" {
		t.Errorf("URL = %q", inc.URL)
	}
	if !utf8.ValidString(inc.SearchText) || strings.ContainsRune(inc.SearchText, 0) {
		t.Errorf("SearchText is not storable: %q", inc.SearchText)
	}
}

func TestCleanTrack(t *testing.T) {
	cases := []struct{ in, want string }{
		{"LINESTRING(0 0,1 1)", "LINESTRING(0 0,1 1)"},
		{"  linestring ( 10.5 -20.25 , 11 -21,12 -22 ) ", "LINESTRING(10.5 -20.25,11 -21,12 -22)"},
		{"LINESTRING(179 10,181 11,190.5 12)", "LINESTRING(179 10,-179 11,-169.5 12)"}, // IBTrACS 0..360 lon
		{"LINESTRING(1 1,1 1,2 2,2 2)", "LINESTRING(1 1,2 2)"},                         // repeated points collapse
		{"LINESTRING(-180 -90,180 90)", "LINESTRING(-180 -90,180 90)"},                 // boundary values
		{"LINESTRING(1e1 2,3 4)", "LINESTRING(10 2,3 4)"},
		{"", ""},
		{"POINT(1 2)", ""},
		{"LINESTRING EMPTY", ""},
		{"LINESTRING(0 0)", ""},                 // one point
		{"LINESTRING(1 1,1 1)", ""},             // one distinct point
		{"LINESTRING(0 0,1 91)", ""},            // lat out of range
		{"LINESTRING(0 0,361 1)", ""},           // lon out of range even after -360
		{"LINESTRING(0 0,-181 1)", ""},          // negative lon out of range
		{"LINESTRING(0 0,NaN 1)", ""},           // not finite
		{"LINESTRING(0 0,Inf 1)", ""},           // not finite
		{"LINESTRING(0 0,1 x)", ""},             // not a number
		{"LINESTRING(0 0 5,1 1 5)", ""},         // Z coordinates
		{"LINESTRING(0 0,1 1", ""},              // unclosed
		{"LINESTRING(0 0,,1 1)", ""},            // empty pair
		{"LINESTRINGZ(0 0,1 1)", ""},            // other geometry name
		{"MULTILINESTRING((0 0,1 1))", ""},      // other geometry type
		{"LINESTRING(0 0,1 1) junk", ""},        // trailing text
		{"LINESTRING(0 0,1 1); DROP TABLE", ""}, // never reaches SQL as-is
	}
	for _, c := range cases {
		if got := cleanTrack(c.in); got != c.want {
			t.Errorf("cleanTrack(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFinalizeDropsBadTrackKeepsIncident(t *testing.T) {
	inc := valid()
	inc.TrackWKT = "LINESTRING(0 0,1 95)"
	if !Finalize(&inc, 1900) {
		t.Fatal("an invalid track must not reject the incident")
	}
	if inc.TrackWKT != "" {
		t.Errorf("invalid track kept: %q", inc.TrackWKT)
	}
}

func TestFinalizeMakesPropsAndHintsStorable(t *testing.T) {
	inc := valid()
	inc.Props = map[string]any{
		"plain":   "ok",
		"nul":     "a\x00b",
		"latin1":  "Caf\xe9",
		"key\x00": 1,
		"nested": map[string]any{
			"deep": []any{"x\x00y", 2.5, map[string]any{"z": "\xff"}},
		},
		"strs": []string{"p\x00q"},
		"smap": map[string]string{"k\x00": "v\x00"},
		"num":  3,
	}
	inc.CountryHints = []string{"fr\x00a", "\xffDEU"}
	if !Finalize(&inc, 1900) {
		t.Fatal("rejected")
	}
	b, err := json.Marshal(inc.Props)
	if err != nil {
		t.Fatal(err)
	}
	js := string(b)
	if strings.Contains(js, `\u0000`) || !utf8.ValidString(js) {
		t.Errorf("props still hold NUL or invalid UTF-8: %s", js)
	}
	for _, want := range []string{`"nul":"ab"`, "\"latin1\":\"Caf\uFFFD\"", `"key":1`, "\"deep\":[\"xy\",2.5,{\"z\":\"\uFFFD\"}]", `"strs":["pq"]`, `"smap":{"k":"v"}`, `"plain":"ok"`, `"num":3`} {
		if !strings.Contains(js, want) {
			t.Errorf("props %s: missing %s", js, want)
		}
	}
	if len(inc.CountryHints) != 2 || inc.CountryHints[0] != "FRA" || inc.CountryHints[1] != "\uFFFDDEU" {
		t.Errorf("CountryHints = %q, want [FRA \uFFFDDEU]", inc.CountryHints)
	}
}
