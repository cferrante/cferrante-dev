package engine

import "testing"

func TestSum(t *testing.T) {
	tests := []struct {
		name string
		in   []int64
		want int64
	}{
		{"empty", nil, 0},
		{"one", []int64{7}, 7},
		{"several", []int64{1, 2, 3, 4}, 10},
		{"negative", []int64{5, -8}, -3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Sum(tt.in); got != tt.want {
				t.Errorf("Sum(%v) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}
