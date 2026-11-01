package embed

import (
	"context"
	"math"
	"testing"
)

func cos(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	return dot / math.Sqrt(na*nb)
}

func TestHashEmbedder(t *testing.T) {
	h := Hash{D: 64}
	if h.Dim() != 64 || h.Model() != "hash-v1-64" {
		t.Errorf("Dim %d, Model %q", h.Dim(), h.Model())
	}
	v, err := h.Embed(context.Background(), []string{
		"The engine fuses keyword and vector search.",
		"Keyword and vector search are fused by the engine!",
		"The consulting rate is negotiable.",
		"",
	})
	if err != nil || len(v) != 4 || len(v[0]) != 64 {
		t.Fatalf("got %d vectors, err %v", len(v), err)
	}
	if same, other := cos(v[0], v[1]), cos(v[0], v[2]); same <= other+0.3 {
		t.Errorf("similar texts %.2f vs unrelated %.2f: want similar clearly closer", same, other)
	}
	for _, x := range v[3] {
		if x != 0 {
			t.Fatal("empty text: want the zero vector")
		}
	}
	again, _ := h.Embed(context.Background(), []string{"The engine fuses keyword and vector search."})
	for i := range again[0] {
		if again[0][i] != v[0][i] {
			t.Fatal("not deterministic")
		}
	}
}

func TestHashIgnoresCaseAndPunctuation(t *testing.T) {
	v, _ := Hash{D: 64}.Embed(context.Background(), []string{"Go Engine, C++!", "go engine c"})
	for i := range v[0] {
		if v[0][i] != v[1][i] {
			t.Fatal("case or punctuation changed the vector; index and query text must embed alike")
		}
	}
}
