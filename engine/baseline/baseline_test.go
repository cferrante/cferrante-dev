package baseline

import (
	"math"
	"slices"
	"testing"
)

func TestDot(t *testing.T) {
	if got := Dot([]float32{1, 2, 3}, []float32{4, -5, 6}); got != 12 {
		t.Errorf("Dot = %v, want 12", got)
	}
	// 19 elements: catches an unrolled loop that drops or double-counts the tail.
	a, b := make([]float32, 19), make([]float32, 19)
	for i := range a {
		a[i], b[i] = float32(i+1), 1
	}
	if got := Dot(a, b); math.Abs(float64(got)-190) > 1e-4 {
		t.Errorf("Dot(1..19, ones) = %v, want 190", got)
	}
}

// Same fixture as engine/tests/topk_test.cc: query (1, 0) scores the x
// components; chunk 1 is the best match but invitee-only.
var (
	vectors = []float32{0.6, 0.8, 1.0, 0.0, 0.8, 0.6, 0.0, 1.0}
	access  = []uint32{0x1, 0x2, 0x1, 0x1}
	query   = []float32{1, 0}

	allPublic = []uint32{0x1, 0x1, 0x1, 0x1}
)

func ids(hits []Hit) []uint32 {
	out := []uint32{}
	for _, h := range hits {
		out = append(out, h.ID)
	}
	return out
}

func TestTopK(t *testing.T) {
	tests := []struct {
		name string
		vecs []float32
		acc  []uint32
		mask uint32
		k    int
		want []uint32
	}{
		{"private chunk excluded for 0x1", vectors, access, 0x1, 4, []uint32{2, 0, 3}},
		{"invitee sees it first", vectors, access, 0x3, 4, []uint32{1, 2, 0, 3}},
		{"k keeps the best k", vectors, access, 0x3, 2, []uint32{1, 2}},
		{"k larger than visible", vectors, access, 0x1, 100, []uint32{2, 0, 3}},
		{"k zero", vectors, access, 0x1, 0, []uint32{}},
		{"no chunk matches mask", vectors, access, 0x4, 4, []uint32{}},
		{"ties by lower id", []float32{0, 1, 1, 0, 1, 0, 1, 0}, allPublic, 0x1, 4, []uint32{1, 2, 3, 0}},
		{"ties cut by k", []float32{0, 1, 1, 0, 1, 0, 1, 0}, allPublic, 0x1, 2, []uint32{1, 2}},
		// Scores rise with id, so every chunk is inserted at the front and the
		// last insertion must still trim back to k.
		{"ascending scores cut by k", []float32{0, 1, 0.6, 0.8, 0.8, 0.6}, allPublic[:3], 0x1, 2, []uint32{2, 1}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ids(TopK(tc.vecs, tc.acc, 2, query, tc.mask, tc.k))
			if !slices.Equal(got, tc.want) {
				t.Errorf("ids = %v, want %v", got, tc.want)
			}
		})
	}
}
