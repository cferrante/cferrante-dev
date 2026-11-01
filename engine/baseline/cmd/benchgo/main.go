// Command benchgo times the pure-Go vector top-k on the same shape of corpus
// as engine/bench (2k x 512, one chunk in eight private, k = 10) and prints
// p50/p95/p99. Informational: compare with the C++ "vector-only" row.
//
//	go run ./engine/baseline/cmd/benchgo
package main

import (
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"time"

	"github.com/cferrante/cferrante-dev/engine/baseline"
)

const (
	chunks  = 2000
	dim     = 512
	k       = 10
	queries = 100
	warmup  = 100
	calls   = 2000
)

func unitVec(rng *rand.Rand) []float32 {
	v := make([]float32, dim)
	var sum float64
	for i := range v {
		x := rng.NormFloat64()
		v[i] = float32(x)
		sum += x * x
	}
	inv := float32(1 / math.Sqrt(sum))
	for i := range v {
		v[i] *= inv
	}
	return v
}

func main() {
	rng := rand.New(rand.NewPCG(42, 0))
	vectors := make([]float32, 0, chunks*dim)
	access := make([]uint32, chunks)
	for i := range access {
		vectors = append(vectors, unitVec(rng)...)
		access[i] = 0x1
		if i%8 == 0 {
			access[i] = 0x2
		}
	}
	qs := make([][]float32, queries)
	for i := range qs {
		qs[i] = unitVec(rng)
	}

	var sink int
	for i := range warmup {
		sink += len(baseline.TopK(vectors, access, dim, qs[i%queries], 0x1, k))
	}
	ms := make([]float64, calls)
	for i := range calls {
		start := time.Now()
		sink += len(baseline.TopK(vectors, access, dim, qs[i%queries], 0x1, k))
		ms[i] = float64(time.Since(start).Nanoseconds()) / 1e6
	}
	slices.Sort(ms)
	at := func(p float64) float64 { return ms[max(int(math.Ceil(p*calls)), 1)-1] } // nearest rank
	fmt.Printf("%-12s %6d x %d   p50 %7.3f ms   p95 %7.3f ms   p99 %7.3f ms   (%d hits)\n",
		"go-vector", chunks, dim, at(0.50), at(0.95), at(0.99), sink/(warmup+calls))
}
