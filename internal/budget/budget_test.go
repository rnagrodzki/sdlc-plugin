package budget

import (
	"math"
	"testing"
)

func TestStaticCap(t *testing.T) {
	cases := []struct {
		name           string
		totalRemaining int
		want           int
	}{
		{"lower bound of 1-3 range", 1, math.MaxInt},
		{"middle of 1-3 range", 2, math.MaxInt},
		{"upper bound of 1-3 range", 3, math.MaxInt},
		{"lower bound of 4-8 range", 4, 4},
		{"upper bound of 4-8 range", 8, 4},
		{"lower bound of 9-15 range", 9, 5},
		{"upper bound of 9-15 range", 15, 5},
		{"lower bound of 16+ range", 16, 6},
		{"well above 16+ range", 100, 6},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := StaticCap(tc.totalRemaining)
			if got != tc.want {
				t.Errorf("StaticCap(%d) = %d, want %d", tc.totalRemaining, got, tc.want)
			}
		})
	}
}
