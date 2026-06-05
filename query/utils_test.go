package query

import (
	"testing"
	"time"
)

func TestIsZeroValUsesTimeIsZero(t *testing.T) {
	zero := time.Time{}
	nonZero := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	var nilTime *time.Time

	tests := []struct {
		name string
		val  any
		want bool
	}{
		{name: "zero time", val: zero, want: true},
		{name: "non-zero time", val: nonZero, want: false},
		{name: "nil time pointer", val: nilTime, want: true},
		{name: "zero time pointer", val: &zero, want: true},
		{name: "non-zero time pointer", val: &nonZero, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isZeroVal(tt.val); got != tt.want {
				t.Fatalf("isZeroVal(%s) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}
