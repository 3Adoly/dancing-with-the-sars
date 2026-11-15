package sources

import (
	"slices"
	"testing"
	"time"
)

func TestClampFIRMSStart(t *testing.T) {
	min := time.Date(2000, 11, 1, 0, 0, 0, 0, time.UTC)
	if got := clampFIRMSStart(time.Time{}); !got.Equal(min) {
		t.Fatalf("zero: %v", got)
	}
	if got := clampFIRMSStart(time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)); !got.Equal(min) {
		t.Fatalf("1900: %v", got)
	}
	late := time.Date(2020, 3, 4, 0, 0, 0, 0, time.UTC)
	if got := clampFIRMSStart(late); !got.Equal(late) {
		t.Fatalf("late: %v", got)
	}
}

func TestDFODedupeOrder(t *testing.T) {
	got := dfoDedupe([]string{"USA", "CAN", "USA"})
	if !slices.Equal(got, []string{"USA", "CAN"}) {
		t.Fatalf("got %v", got)
	}
}
