package service

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/dwts/backend/internal/domain"
)

// randomRows produces random counts/totals for nCountries countries + ocean over [minY, maxY].
func randomRows(rng *rand.Rand, nCountries, minY, maxY, nRows int) ([]domain.CountRow, []domain.TotalRow) {
	var counts []domain.CountRow
	var totals []domain.TotalRow
	for i := 0; i < nRows; i++ {
		iso := ""
		if k := rng.Intn(nCountries + 1); k > 0 {
			iso = fmt.Sprintf("C%02d", k)
		}
		t := domain.AllTypes[rng.Intn(len(domain.AllTypes))]
		y := int16(minY + rng.Intn(maxY-minY+1))
		counts = append(counts, domain.CountRow{Type: t, ISO3: iso, Year: y, N: int32(rng.Intn(1000))})
		totals = append(totals, domain.TotalRow{Type: t, Year: y, N: int32(rng.Intn(1000))})
	}
	return counts, totals
}

func naiveSum(rows []domain.CountRow, types []domain.IncidentType, iso string, from, to int) int64 {
	var s int64
	for _, r := range rows {
		if normISO(r.ISO3) == iso && int(r.Year) >= from && int(r.Year) <= to && containsType(types, r.Type) {
			s += int64(r.N)
		}
	}
	return s
}

func naiveTotal(rows []domain.TotalRow, types []domain.IncidentType, from, to int) int64 {
	var s int64
	for _, r := range rows {
		if int(r.Year) >= from && int(r.Year) <= to && containsType(types, r.Type) {
			s += int64(r.N)
		}
	}
	return s
}

func containsType(ts []domain.IncidentType, t domain.IncidentType) bool {
	for _, x := range ts {
		if x == t {
			return true
		}
	}
	return false
}

func TestCubeMatchesNaiveSum(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	const minY, maxY = 1900, 2026
	counts, totals := randomRows(rng, 40, minY, maxY, 20000)
	c, err := BuildCube(9, minY, maxY, counts, totals)
	if err != nil {
		t.Fatal(err)
	}
	for trial := 0; trial < 100; trial++ {
		var types []domain.IncidentType
		for _, ty := range domain.AllTypes {
			if rng.Intn(3) == 0 {
				types = append(types, ty)
			}
		}
		a, b := minY-5+rng.Intn(140), minY-5+rng.Intn(140) // may fall outside → clamping
		if a > b {
			a, b = b, a
		}
		got := c.Sum(types, a, b)
		cf, ct, _ := c.ClampYears(a, b)
		for i := 0; i < c.Countries(); i++ {
			if want := naiveSum(counts, types, c.ISO(i), cf, ct); got[i] != want {
				t.Fatalf("trial %d country %q types %v [%d,%d]: got %d want %d", trial, c.ISO(i), types, a, b, got[i], want)
			}
		}
		if got, want := c.Total(types, a, b), naiveTotal(totals, types, cf, ct); got != want {
			t.Fatalf("total: got %d want %d", got, want)
		}
		// Series sums must equal the range sums.
		iso := c.ISO(1 + rng.Intn(c.Countries()-1))
		var s int64
		for _, p := range c.Series(types, iso, a, b) {
			s += p.N
		}
		if want := naiveSum(counts, types, iso, cf, ct); s != want {
			t.Fatalf("series %s: %d want %d", iso, s, want)
		}
	}
}

func TestCubeEdgeCases(t *testing.T) {
	counts := []domain.CountRow{
		{Type: domain.Earthquake, ISO3: "jpn", Year: 2000, N: 5},   // lower-case → JPN
		{Type: domain.Earthquake, ISO3: "-99", Year: 2000, N: 3},   // malformed → ocean
		{Type: domain.Earthquake, ISO3: "", Year: 2000, N: 2},      // ocean
		{Type: domain.Earthquake, ISO3: "JPN", Year: 1800, N: 100}, // before minYear → skipped
		{Type: domain.Earthquake, ISO3: "JPN", Year: 2100, N: 100}, // after maxYear → skipped
		{Type: "meteor", ISO3: "JPN", Year: 2000, N: 100},          // unknown type → skipped
		{Type: domain.Flood, ISO3: "PHL", Year: 2026, N: 7},        // last year included
	}
	c, err := BuildCube(1, 1900, 2026, counts, []domain.TotalRow{{Type: domain.Earthquake, Year: 2000, N: 9}})
	if err != nil {
		t.Fatal(err)
	}
	if c.Skipped != 3 || c.Ocean != 1 {
		t.Errorf("skipped=%d ocean=%d", c.Skipped, c.Ocean)
	}
	eq := []domain.IncidentType{domain.Earthquake}
	got := c.Sum(eq, 1900, 2026)
	ji, _ := c.Index("JPN")
	if got[0] != 5 || got[ji] != 5 {
		t.Errorf("ocean=%d jpn=%d", got[0], got[ji])
	}
	if c.Total(eq, 1990, 2010) != 9 || c.Total(eq, 2001, 2010) != 0 {
		t.Error("total range wrong")
	}
	if s := c.Sum(eq, 2027, 2030); s[ji] != 0 { // empty after clamping
		t.Error("out-of-range query must be 0")
	}
	if pts := c.Series([]domain.IncidentType{domain.Flood}, "PHL", 2025, 2030); len(pts) != 2 || pts[1].N != 7 {
		t.Errorf("series clamp: %+v", pts)
	}
	if pts := c.Series(eq, "XXX", 2000, 2000); len(pts) != 1 || pts[0].N != 0 {
		t.Errorf("unknown country should be zeros: %+v", pts)
	}
	if _, err := BuildCube(1, 2000, 1999, nil, nil); err == nil {
		t.Error("maxYear < minYear must fail")
	}
	if _, err := BuildCube(1, 2000, 2001, []domain.CountRow{
		{Type: domain.FireActivity, ISO3: "IDN", Year: 2000, N: 2_147_000_000},
		{Type: domain.FireActivity, ISO3: "IDN", Year: 2001, N: 2_147_000_000},
		{Type: domain.FireActivity, ISO3: "IDN", Year: 2001, N: 2_147_000_000},
	}, nil); err == nil {
		t.Error("uint32 overflow must be reported")
	}
}

// BenchmarkCubeSum: realistic size (10 types × 260 countries × 127 years), all types, full range.
func BenchmarkCubeSum(b *testing.B) {
	rng := rand.New(rand.NewSource(2))
	counts, totals := randomRows(rng, 259, 1900, 2026, 300000)
	c, err := BuildCube(1, 1900, 2026, counts, totals)
	if err != nil {
		b.Fatal(err)
	}
	types := domain.AllTypes[:4]
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = c.Sum(types, 1950, 2000)
	}
}

// BenchmarkChoropleth measures the whole service call (sum + map + breaks + coverage).
func BenchmarkChoropleth(b *testing.B) {
	rng := rand.New(rand.NewSource(3))
	counts, totals := randomRows(rng, 259, 1900, 2026, 300000)
	s := newTestService(b, &stubReader{version: 1, counts: counts, totals: totals})
	q := domain.ChoroplethQuery{Types: []domain.IncidentType{domain.Earthquake, domain.Flood}, Years: domain.YearRange{From: 1950, To: 2000}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Choropleth(b.Context(), q); err != nil {
			b.Fatal(err)
		}
	}
}
