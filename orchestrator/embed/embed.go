// Package embed turns text into vectors (design §4). The index and the query
// must use the same model, so the index records which one built it.
package embed

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"
	"unicode"
)

// Embedder embeds texts. Model names the embedding model and its settings, so
// an index built with one model is never queried with another.
type Embedder interface {
	Model() string
	Dim() int
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// Hash is a deterministic bag-of-words embedder with no model to download:
// each word adds ±1 to a dimension chosen by hashing it. Texts sharing words
// get similar vectors, which is enough for tests and for running without
// Ollama; it knows nothing about meaning.
type Hash struct{ D int }

func (h Hash) Model() string { return fmt.Sprintf("hash-v1-%d", h.D) }
func (h Hash) Dim() int      { return h.D }

// Embed returns one vector per text; a text with no words gets the zero vector.
func (h Hash) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, h.D)
		words := strings.FieldsFunc(strings.ToLower(t), func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		})
		for _, w := range words {
			f := fnv.New64a()
			f.Write([]byte(w))
			x := f.Sum64()
			sign := float32(1)
			if x>>63 == 1 {
				sign = -1
			}
			v[x%uint64(h.D)] += sign
		}
		out[i] = v
	}
	return out, nil
}
