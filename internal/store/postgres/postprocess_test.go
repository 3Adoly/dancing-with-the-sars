package postgres

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/dwts/backend/internal/domain"
)

func TestStorableText(t *testing.T) {
	got := storableText("a\x00b Caf\xe9 \x00")
	if got != "ab Caf� " || !utf8.ValidString(got) {
		t.Errorf("storableText = %q", got)
	}
	if s := "plain é"; storableText(s) != s {
		t.Errorf("valid text must be unchanged")
	}
}

func TestCoverageNotesCoverEveryType(t *testing.T) {
	types := domain.AllTypes
	for _, typ := range types {
		if strings.TrimSpace(coverageNotes[string(typ)]) == "" {
			t.Errorf("no coverage note for %s", typ)
		}
	}
	if len(coverageNotes) != len(types) {
		t.Errorf("%d notes for %d types: a key is misspelled", len(coverageNotes), len(types))
	}
	args := coverageNoteArgs()
	keys, notes := args[0].([]string), args[1].([]string)
	if len(keys) != len(notes) || len(keys) != len(coverageNotes) {
		t.Fatalf("coverageNoteArgs: %d keys, %d notes", len(keys), len(notes))
	}
	for i, k := range keys {
		if coverageNotes[k] != notes[i] {
			t.Errorf("note %d for %s is misaligned", i, k)
		}
		if i > 0 && keys[i-1] >= k {
			t.Errorf("keys must be sorted for a stable statement: %v", keys)
		}
	}
}

func TestPostprocessStepOrder(t *testing.T) {
	var names []string
	for _, s := range postprocessSteps() {
		names = append(names, s.name)
		if len(s.stmts) == 0 {
			t.Errorf("%s has no statements", s.name)
		}
	}
	if got := strings.Join(names, ","); got != "P0,P1,P2,P3,P4,P5,P6,P7,P8,P9,P10,P11" {
		t.Errorf("steps = %s", got)
	}
}

func TestCountryGeoSQL(t *testing.T) {
	parts := countryPartsSQL("p", true)
	if len(parts) != 1 || !strings.Contains(parts[0], "CREATE TEMP TABLE p ON COMMIT DROP AS") || !strings.Contains(parts[0], "ST_Subdivide(c.geom, 128)") {
		t.Errorf("parts:\n%s", parts)
	}
	if s := countryPartsSQL("p", false)[0]; strings.Contains(s, "ON COMMIT DROP") {
		t.Errorf("session parts table must not drop on commit:\n%s", s)
	}
	q := countryBoxesSQL("b", "p", 200000)
	if len(q) != 3 {
		t.Fatalf("%d statements", len(q))
	}
	// dy = 200 km / 110 km per degree
	for _, want := range []string{"CREATE TEMP TABLE b ON COMMIT DROP AS", "FROM p\n", "1.8181818181818181"} {
		if !strings.Contains(q[0], want) {
			t.Errorf("missing %q in\n%s", want, q[0])
		}
	}
	if s := countryBoxesSQL("b", "p", 50000)[0]; !strings.Contains(s, "0.45454545454545453") {
		t.Errorf("50 km radius:\n%s", s)
	}
}
