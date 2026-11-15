package service

import (
	"reflect"
	"testing"
)

func TestBreaks(t *testing.T) {
	cases := []struct {
		name string
		in   []int64
		want []int64
	}{
		{"empty", nil, []int64{0}},
		{"all zero", []int64{0, 0, 0}, []int64{0}},
		{"single", []int64{0, 7}, []int64{0, 7}},
		{"all equal", []int64{5, 5, 5, 5}, []int64{0, 5}},
		{"two values", []int64{1, 1000}, []int64{0, 1, 1000}},
		{"skewed (example-like)", []int64{6120, 5833, 3022, 2410, 1980, 450, 120, 95, 30, 12, 3, 1},
			[]int64{0, 10, 100, 2000, 6120}},
	}
	for _, c := range cases {
		if got := Breaks(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestBreaksInvariants(t *testing.T) {
	inputs := [][]int64{
		{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
		{1, 1, 1, 1, 2, 2, 1000000},
		{3, 9, 27, 81, 243, 729, 2187, 6561},
		{100, 101, 102, 103},
	}
	for _, in := range inputs {
		b := Breaks(in)
		if b[0] != 0 || len(b) > maxBreaks || b[len(b)-1] != maxOf(in) {
			t.Errorf("%v → %v: bad ends/length", in, b)
		}
		for i := 1; i < len(b); i++ {
			if b[i] <= b[i-1] {
				t.Errorf("%v → %v: not strictly increasing", in, b)
			}
		}
	}
}

func maxOf(v []int64) int64 {
	m := v[0]
	for _, x := range v {
		m = max(m, x)
	}
	return m
}

func TestNiceRound(t *testing.T) {
	for in, want := range map[int64]int64{0: 0, 1: 1, 3: 2, 4: 5, 7: 5, 8: 10, 140: 100, 150: 200, 3022: 2000, 3500: 5000} {
		if got := niceRound(in); got != want {
			t.Errorf("niceRound(%d)=%d want %d", in, got, want)
		}
	}
}
