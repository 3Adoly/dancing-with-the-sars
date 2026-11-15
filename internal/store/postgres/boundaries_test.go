package postgres

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestDecodeFeaturesFixture(t *testing.T) {
	f, err := os.Open("testdata/countries_fixture.geojson")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var got []countryFeature
	err = decodeFeatures(f, func(i int, ft countryFeature) error {
		if i != len(got) {
			t.Errorf("index = %d, want %d", i, len(got))
		}
		got = append(got, ft)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("decoded %d features, want 3", len(got))
	}

	fra := got[0].Properties
	if fra.ADM0A3 != "FRA" || fra.ISOA2 != "-99" || fra.ISOA2EH != "FR" || fra.Name != "France" || fra.Continent != "Europe" {
		t.Errorf("FRA properties = %+v", fra)
	}
	if fra.LabelX == nil || *fra.LabelX != 2.552275 || fra.LabelY == nil || *fra.LabelY != 46.696113 {
		t.Errorf("FRA label = %v, %v", fra.LabelX, fra.LabelY)
	}
	if !strings.Contains(string(got[0].Geometry), `"MultiPolygon"`) {
		t.Errorf("FRA geometry = %s", got[0].Geometry)
	}
	if bow := got[2].Properties; bow.ADM0A3 != "BOW" || bow.LabelX != nil || bow.LabelY != nil {
		t.Errorf("null LABEL_X/LABEL_Y must decode to nil, got %+v", bow)
	}
}

func TestDecodeFeaturesPropertyCase(t *testing.T) {
	// Older Natural Earth files use lowercase property names.
	in := `{"type":"FeatureCollection","features":[{"type":"Feature","properties":{"adm0_a3":"NOR","iso_a2":"-99","name":"Norway"},"geometry":{"type":"Polygon","coordinates":[]}}]}`
	var got countryFeature
	err := decodeFeatures(strings.NewReader(in), func(_ int, ft countryFeature) error {
		got = ft
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Properties.ADM0A3 != "NOR" || got.Properties.Name != "Norway" {
		t.Errorf("lowercase properties not read: %+v", got.Properties)
	}
}

func TestDecodeFeaturesStopsOnCallbackError(t *testing.T) {
	in := `{"features":[{"properties":{"ADM0_A3":"AAA"}},{"properties":{"ADM0_A3":"BBB"}}]}`
	boom := errors.New("boom")
	calls := 0
	err := decodeFeatures(strings.NewReader(in), func(int, countryFeature) error {
		calls++
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if calls != 1 {
		t.Errorf("callback ran %d times after failing, want 1", calls)
	}
}

func TestDecodeFeaturesErrors(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"empty input", ``, "not a GeoJSON object"},
		{"array", `[]`, "not a GeoJSON object"},
		{"no features member", `{"type":"FeatureCollection"}`, `no "features" member`},
		{"features not an array", `{"features":{}}`, "not an array"},
		{"truncated", `{"features":[{"properties":{"ADM0_A3":"AAA"}},`, "decode feature 1"},
		{"bad property type", `{"features":[{"properties":{"ADM0_A3":5}}]}`, "decode feature 0"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := decodeFeatures(strings.NewReader(tc.in), func(int, countryFeature) error { return nil })
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestDecodeFeaturesMembersAfterFeatures(t *testing.T) {
	in := `{"type":"FeatureCollection","features":[{"properties":{"ADM0_A3":"AAA"}}],"bbox":[1,2,3,4],"extra":{"a":[1,2]}}`
	n := 0
	if err := decodeFeatures(strings.NewReader(in), func(int, countryFeature) error { n++; return nil }); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("decoded %d features, want 1", n)
	}
}
