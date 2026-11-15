package service

import (
	"math"
	"slices"
)

// maxBreaks is the maximum number of break values returned (0 … max).
const maxBreaks = 6

// Breaks computes legend class breaks for a choropleth (Plan_Detailed §7):
// take the non-zero values, use the q20/q40/q60/q80 quantiles, round each to a "nice"
// 1-2-5 × 10^k number (log-friendly for skewed counts), drop breaks below the smallest value,
// and return a strictly increasing
// [0, …, max] with at most 6 values. All-zero (or empty) input returns [0].
func Breaks(values []int64) []int64 {
	nz := make([]int64, 0, len(values))
	for _, v := range values {
		if v > 0 {
			nz = append(nz, v)
		}
	}
	if len(nz) == 0 {
		return []int64{0}
	}
	slices.Sort(nz)
	maxV := nz[len(nz)-1]
	out := []int64{0}
	for _, q := range []float64{0.2, 0.4, 0.6, 0.8} {
		v := niceRound(quantile(nz, q))
		// keep strictly increasing, below max, and not below the smallest value (no empty first class)
		if v > out[len(out)-1] && v < maxV && v >= nz[0] {
			out = append(out, v)
		}
	}
	out = append(out, maxV)
	if len(out) > maxBreaks { // cannot happen with 4 quantiles, kept as a guard
		out = append(out[:maxBreaks-1], maxV)
	}
	return out
}

// quantile uses the nearest-rank method on sorted data.
func quantile(sorted []int64, q float64) int64 {
	i := int(math.Ceil(q*float64(len(sorted)))) - 1
	return sorted[max(0, min(i, len(sorted)-1))]
}

// niceRound rounds v to the nearest number of the form {1,2,5}×10^k in log space (v ≥ 1).
func niceRound(v int64) int64 {
	if v < 1 {
		return 0
	}
	exp := math.Floor(math.Log10(float64(v)))
	base := math.Pow(10, exp)
	best, bestD := int64(0), math.Inf(1)
	for _, m := range []float64{1, 2, 5, 10} {
		cand := m * base
		if d := math.Abs(math.Log(cand) - math.Log(float64(v))); d < bestD {
			best, bestD = int64(math.Round(cand)), d
		}
	}
	return best
}
