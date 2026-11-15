// Package service holds the business logic (Cube, StatsService, …). Rule R7: it never imports
// chat or httpapi. It depends on store interfaces only, never on store/postgres.
package service

import (
	"fmt"
	"math"
	"strings"

	"github.com/dwts/backend/internal/domain"
)

// Cube is the dense in-memory count cube (Plan_Detailed §7).
//
// Layout: pre[((t*C)+c)*(NY+1)+k] = Σ counts of type t, country c, years [MinYear, MinYear+k).
// Country index 0 is "ocean/unassigned". tpre[t*(NY+1)+k] is the same for DISTINCT totals.
// A range query is two array reads per (type, country): O(T×C).
type Cube struct {
	Version int64
	MinYear int
	MaxYear int
	nYears  int
	iso     []string       // iso[0] = "" (ocean)
	idx     map[string]int // ISO3 → index
	pre     []uint32
	tpre    []uint32
	Skipped int // rows ignored (unknown type / year outside range)
	Ocean   int // rows with an empty or malformed ISO3, folded into index 0
}

// BuildCube builds a cube from mv_counts rows and mv_totals rows. Rows with an unknown type or
// a year outside [minYear, maxYear] are skipped (counted in Skipped). Empty or malformed ISO3
// codes are folded into the ocean/unassigned bucket so no count is lost.
func BuildCube(version int64, minYear, maxYear int, counts []domain.CountRow, totals []domain.TotalRow) (*Cube, error) {
	if maxYear < minYear {
		return nil, fmt.Errorf("cube: maxYear %d < minYear %d", maxYear, minYear)
	}
	c := &Cube{Version: version, MinYear: minYear, MaxYear: maxYear, nYears: maxYear - minYear + 1,
		iso: []string{""}, idx: map[string]int{"": 0}}

	tIdx := typeIndex()
	// Pass 1: country axis.
	for i := range counts {
		iso := normISO(counts[i].ISO3)
		if iso == "" {
			continue
		}
		if _, ok := c.idx[iso]; !ok {
			c.idx[iso] = len(c.iso)
			c.iso = append(c.iso, iso)
		}
	}
	T, C, W := len(domain.AllTypes), len(c.iso), c.nYears+1
	acc := make([]uint64, T*C*W) // accumulate in uint64, check overflow once
	tacc := make([]uint64, T*W)

	for _, r := range counts {
		t, ok := tIdx[r.Type]
		y := int(r.Year) - minYear
		if !ok || y < 0 || y >= c.nYears || r.N < 0 {
			c.Skipped++
			continue
		}
		iso := normISO(r.ISO3)
		if iso == "" && strings.TrimSpace(r.ISO3) != "" {
			c.Ocean++ // malformed code → unassigned bucket
		}
		acc[(t*C+c.idx[iso])*W+y+1] += uint64(r.N)
	}
	for _, r := range totals {
		t, ok := tIdx[r.Type]
		y := int(r.Year) - minYear
		if !ok || y < 0 || y >= c.nYears || r.N < 0 {
			c.Skipped++
			continue
		}
		tacc[t*W+y+1] += uint64(r.N)
	}

	var err error
	if c.pre, err = prefix(acc, W); err != nil {
		return nil, err
	}
	if c.tpre, err = prefix(tacc, W); err != nil {
		return nil, err
	}
	return c, nil
}

// prefix turns per-year values (slot k+1 = year k) into running sums per block of width w.
func prefix(a []uint64, w int) ([]uint32, error) {
	out := make([]uint32, len(a))
	for b := 0; b < len(a); b += w {
		var s uint64
		for k := 1; k < w; k++ {
			s += a[b+k]
			if s > math.MaxUint32 {
				return nil, fmt.Errorf("cube: cumulative count %d overflows uint32", s)
			}
			out[b+k] = uint32(s)
		}
	}
	return out, nil
}

func typeIndex() map[domain.IncidentType]int {
	m := make(map[domain.IncidentType]int, len(domain.AllTypes))
	for i, t := range domain.AllTypes {
		m[t] = i
	}
	return m
}

// normISO upper-cases a code and returns "" if it is not 3 ASCII letters/digits.
func normISO(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	if len(s) != 3 {
		return ""
	}
	for i := 0; i < 3; i++ {
		if ch := s[i]; !(ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9') {
			return ""
		}
	}
	return s
}

// ClampYears clamps [from, to] to the cube range. ok=false if the range is empty after clamping.
func (c *Cube) ClampYears(from, to int) (int, int, bool) {
	from, to = max(from, c.MinYear), min(to, c.MaxYear)
	return from, to, from <= to
}

// Countries returns the number of country slots including ocean (index 0).
func (c *Cube) Countries() int { return len(c.iso) }

// ISO returns the ISO3 at index i ("" for ocean).
func (c *Cube) ISO(i int) string { return c.iso[i] }

// Index returns the index of an ISO3 code.
func (c *Cube) Index(iso3 string) (int, bool) {
	i, ok := c.idx[normISO(iso3)]
	return i, ok && i != 0
}

func (c *Cube) typeIdx(types []domain.IncidentType) []int {
	ti := make([]int, 0, len(types))
	for _, t := range types {
		for i, k := range domain.AllTypes {
			if k == t {
				ti = append(ti, i)
				break
			}
		}
	}
	return ti
}

// Sum returns per-country counts (index 0 = ocean) for the types over [from, to] (clamped).
func (c *Cube) Sum(types []domain.IncidentType, from, to int) []int64 {
	out := make([]int64, len(c.iso))
	from, to, ok := c.ClampYears(from, to)
	if !ok {
		return out
	}
	W, C := c.nYears+1, len(c.iso)
	lo, hi := from-c.MinYear, to-c.MinYear+1
	for _, t := range c.typeIdx(types) {
		base := t * C * W
		for ci := 0; ci < C; ci++ {
			row := c.pre[base+ci*W : base+ci*W+W]
			out[ci] += int64(row[hi] - row[lo])
		}
	}
	return out
}

// Total returns DISTINCT incidents (mv_totals) for the types over [from, to] (clamped).
func (c *Cube) Total(types []domain.IncidentType, from, to int) int64 {
	from, to, ok := c.ClampYears(from, to)
	if !ok {
		return 0
	}
	W := c.nYears + 1
	var s int64
	for _, t := range c.typeIdx(types) {
		s += int64(c.tpre[t*W+to-c.MinYear+1] - c.tpre[t*W+from-c.MinYear])
	}
	return s
}

// Series returns one point per year in [from, to] (clamped). iso3 "" = world (DISTINCT totals);
// otherwise the per-country counts (an unknown country yields zeros).
func (c *Cube) Series(types []domain.IncidentType, iso3 string, from, to int) []domain.YearCount {
	from, to, ok := c.ClampYears(from, to)
	if !ok {
		return []domain.YearCount{}
	}
	W, C := c.nYears+1, len(c.iso)
	ti := c.typeIdx(types)
	ci, known := c.Index(iso3)
	pts := make([]domain.YearCount, 0, to-from+1)
	for y := from; y <= to; y++ {
		k := y - c.MinYear
		var n int64
		for _, t := range ti {
			if iso3 == "" {
				n += int64(c.tpre[t*W+k+1] - c.tpre[t*W+k])
			} else if known {
				b := (t*C+ci)*W + k
				n += int64(c.pre[b+1] - c.pre[b])
			}
		}
		pts = append(pts, domain.YearCount{Year: y, N: n})
	}
	return pts
}
