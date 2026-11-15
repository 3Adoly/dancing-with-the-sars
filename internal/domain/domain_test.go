package domain

import (
	"errors"
	"testing"
	"time"
)

func TestParseType(t *testing.T) {
	cases := []struct {
		in   string
		want IncidentType
		ok   bool
	}{
		{"earthquake", Earthquake, true},
		{" Flood ", Flood, true},
		{"FIRE_ACTIVITY", FireActivity, true},
		{"fire", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := ParseType(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("ParseType(%q) = %q,%v want %q,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestTypeCatalogMatchesAllTypes(t *testing.T) {
	cat := TypeCatalog()
	if len(cat) != len(AllTypes) {
		t.Fatalf("catalog has %d entries, AllTypes %d", len(cat), len(AllTypes))
	}
	for i, ti := range cat {
		if ti.ID != AllTypes[i] {
			t.Errorf("catalog[%d]=%s want %s", i, ti.ID, AllTypes[i])
		}
		if ti.Unit != ti.ID.Unit() {
			t.Errorf("%s unit %q want %q", ti.ID, ti.Unit, ti.ID.Unit())
		}
	}
}

func TestCursorRoundTrip(t *testing.T) {
	s, m := int16(3), 1.0
	ts := time.Date(1991, 6, 15, 0, 0, 0, 0, time.UTC)
	for _, c := range []Cursor{{S: &s, M: &m, I: 118201}, {T: &ts, I: 7}} {
		got, err := DecodeCursor(EncodeCursor(c))
		if err != nil {
			t.Fatal(err)
		}
		if got.I != c.I {
			t.Errorf("id %d want %d", got.I, c.I)
		}
	}
	// The example cursor in api/examples/incidents.json (std base64) must decode.
	if c, err := DecodeCursor("eyJzIjozLCJtIjoxLCJpIjoxMTgyMDF9"); err != nil || c.I != 118201 || *c.S != 3 {
		t.Errorf("example cursor: %+v %v", c, err)
	}
	for _, bad := range []string{"%%%", "bm90anNvbg", "e30"} { // invalid b64, "notjson", "{}"
		if _, err := DecodeCursor(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("DecodeCursor(%q) err=%v want ErrInvalid", bad, err)
		}
	}
}
