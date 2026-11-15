package httpapi

import (
	"errors"
	"net/url"
	"reflect"
	"testing"

	"github.com/dwts/backend/internal/domain"
)

func q(s string) url.Values { v, _ := url.ParseQuery(s); return v }

// wantParam asserts err is a 400 invalid_param naming param.
func wantParam(t *testing.T, err error, param string) {
	t.Helper()
	var ae *APIError
	if !errors.As(err, &ae) || ae.Code != CodeInvalidParam || ae.Status != 400 || ae.Details["param"] != param {
		t.Fatalf("want invalid_param on %q, got %#v", param, err)
	}
}

func TestParseTypes(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		required bool
		want     []domain.IncidentType
		errParam string
	}{
		{"two", "types=earthquake,flood", true, []domain.IncidentType{"earthquake", "flood"}, ""},
		{"dedup+case+spaces", "types=Flood, flood ,,earthquake", true, []domain.IncidentType{"flood", "earthquake"}, ""},
		{"missing optional", "", false, nil, ""},
		{"missing required", "", true, nil, "types"},
		{"only commas required", "types=,,", true, nil, "types"},
		{"unknown", "types=earthquake,meteor", false, nil, "types"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseTypes(q(c.in), c.required)
			if c.errParam != "" {
				wantParam(t, err, c.errParam)
				return
			}
			if err != nil || !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %v, %v want %v", got, err, c.want)
			}
		})
	}
}

func TestEnsureSingleUnit(t *testing.T) {
	if err := ensureSingleUnit([]domain.IncidentType{domain.FireActivity}); err != nil {
		t.Errorf("alone should pass: %v", err)
	}
	if err := ensureSingleUnit([]domain.IncidentType{domain.Earthquake, domain.Flood}); err != nil {
		t.Errorf("incident types should pass: %v", err)
	}
	wantParam(t, ensureSingleUnit([]domain.IncidentType{domain.Flood, domain.FireActivity}), "types")
}

func TestParseYearRange(t *testing.T) {
	cases := []struct {
		in       string
		want     domain.YearRange
		errParam string
	}{
		{"", domain.YearRange{From: 1900, To: 2026}, ""},
		{"from=1950&to=2000", domain.YearRange{From: 1950, To: 2000}, ""},
		{"from=1800&to=3000", domain.YearRange{From: 1900, To: 2026}, ""}, // clamped
		{"from=2000&to=2000", domain.YearRange{From: 2000, To: 2000}, ""},
		{"from=2001&to=2000", domain.YearRange{}, "from"},
		{"from=abc", domain.YearRange{}, "from"},
		{"to=19.5", domain.YearRange{}, "to"},
	}
	for _, c := range cases {
		got, err := parseYearRange(q(c.in), 1900, 2026)
		if c.errParam != "" {
			wantParam(t, err, c.errParam)
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%q: got %+v, %v want %+v", c.in, got, err, c.want)
		}
	}
}

func TestParseBBox(t *testing.T) {
	cases := []struct {
		in       string
		required bool
		want     *domain.BBox
		errParam string
	}{
		{"bbox=120,20,150,50", true, &domain.BBox{MinLon: 120, MinLat: 20, MaxLon: 150, MaxLat: 50}, ""},
		{"bbox=170,-10,-170,10", true, &domain.BBox{MinLon: 170, MinLat: -10, MaxLon: -170, MaxLat: 10}, ""}, // antimeridian
		{"", false, nil, ""},
		{"", true, nil, "bbox"},
		{"bbox=1,2,3", true, nil, "bbox"},
		{"bbox=a,2,3,4", true, nil, "bbox"},
		{"bbox=-181,0,10,10", true, nil, "bbox"},
		{"bbox=0,-91,10,10", true, nil, "bbox"},
		{"bbox=0,10,10,10", true, nil, "bbox"},
		{"bbox=5,0,5,10", true, nil, "bbox"},
		{"bbox=NaN,0,10,10", true, nil, "bbox"},
	}
	for _, c := range cases {
		got, err := parseBBox(q(c.in), c.required)
		if c.errParam != "" {
			wantParam(t, err, c.errParam)
			continue
		}
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %+v, %v want %+v", c.in, got, err, c.want)
		}
	}
}

func TestParseLimit(t *testing.T) {
	cases := []struct {
		in   string
		want int
		bad  bool
	}{
		{"", 500, false}, {"limit=1", 1, false}, {"limit=5000", 5000, false},
		{"limit=0", 0, true}, {"limit=5001", 0, true}, {"limit=x", 0, true}, {"limit=-3", 0, true},
	}
	for _, c := range cases {
		got, err := parseLimit(q(c.in), 500, 5000)
		if c.bad {
			wantParam(t, err, "limit")
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%q: got %d, %v want %d", c.in, got, err, c.want)
		}
	}
}

func TestParseCursor(t *testing.T) {
	good := domain.EncodeCursor(domain.Cursor{I: 42})
	if got, err := parseCursor(q("cursor=" + good)); err != nil || got != good {
		t.Errorf("good cursor: %q %v", got, err)
	}
	if got, err := parseCursor(q("")); err != nil || got != "" {
		t.Errorf("empty cursor: %q %v", got, err)
	}
	_, err := parseCursor(q("cursor=garbage!!"))
	wantParam(t, err, "cursor")
}

func TestParseMisc(t *testing.T) {
	if id, err := parseID("118233"); err != nil || id != 118233 {
		t.Errorf("parseID: %d %v", id, err)
	}
	for _, bad := range []string{"0", "-1", "abc", ""} {
		_, err := parseID(bad)
		wantParam(t, err, "id")
	}
	if b, err := parseBool(q("refresh=true"), "refresh", false); err != nil || !b {
		t.Errorf("parseBool: %v %v", b, err)
	}
	_, err := parseBool(q("refresh=maybe"), "refresh", false)
	wantParam(t, err, "refresh")
	if n, err := parseIntRange(q("windowDays=60"), "windowDays", 45, 1, 180); err != nil || n != 60 {
		t.Errorf("parseIntRange: %d %v", n, err)
	}
	_, err = parseIntRange(q("windowDays=181"), "windowDays", 45, 1, 180)
	wantParam(t, err, "windowDays")
	if c, err := parseCell(q("cell=0.25")); err != nil || c != 0.25 {
		t.Errorf("parseCell: %v %v", c, err)
	}
	if c, err := parseCell(q("")); err != nil || c != 1 {
		t.Errorf("parseCell default: %v %v", c, err)
	}
	_, err = parseCell(q("cell=3"))
	wantParam(t, err, "cell")
	if s, err := parseSort(q("")); err != nil || s != "severity" {
		t.Errorf("parseSort default: %v %v", s, err)
	}
	_, err = parseSort(q("sort=name"))
	wantParam(t, err, "sort")
}
