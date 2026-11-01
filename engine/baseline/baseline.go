// Package baseline is a pure-Go version of the engine's vector search, used
// only to answer "is the C++ engine actually faster than plain Go?" (plan step
// 2.6, benchmarks.md open question 1). It is not called by the service.
package baseline

// Hit is a chunk id and its score.
type Hit struct {
	ID    uint32
	Score float32
}

// Dot returns the dot product of two equal-length vectors. It uses the same
// eight independent sums as the C++ engine, so the comparison is between
// compilers and languages, not algorithms. (Go's compiler doesn't vectorise,
// but independent sums still let the CPU overlap the additions.)
func Dot(a, b []float32) float32 {
	var acc [8]float32
	n := len(a)
	b = b[:n] // one bounds check here instead of one per element
	i := 0
	for ; i+8 <= n; i += 8 {
		acc[0] += a[i] * b[i]
		acc[1] += a[i+1] * b[i+1]
		acc[2] += a[i+2] * b[i+2]
		acc[3] += a[i+3] * b[i+3]
		acc[4] += a[i+4] * b[i+4]
		acc[5] += a[i+5] * b[i+5]
		acc[6] += a[i+6] * b[i+6]
		acc[7] += a[i+7] * b[i+7]
	}
	var sum float32
	for _, x := range acc {
		sum += x
	}
	for ; i < n; i++ {
		sum += a[i] * b[i]
	}
	return sum
}

func better(a, b Hit) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	return a.ID < b.ID
}

// TopK scores every chunk whose access bits intersect mask and returns the
// best k, highest score first, ties by lower id: the same contract as the
// engine's vector_topk.
func TopK(vectors []float32, access []uint32, dim int, query []float32, mask uint32, k int) []Hit {
	if k <= 0 {
		return nil
	}
	// Keep the best k so far in order; for small k an insertion is cheaper
	// than sorting every score.
	best := make([]Hit, 0, k+1)
	for i, bits := range access {
		if bits&mask == 0 {
			continue
		}
		h := Hit{uint32(i), Dot(vectors[i*dim:(i+1)*dim], query)}
		if len(best) == k && !better(h, best[k-1]) {
			continue
		}
		pos := len(best)
		for pos > 0 && better(h, best[pos-1]) {
			pos--
		}
		best = append(best, Hit{})
		copy(best[pos+1:], best[pos:])
		best[pos] = h
		if len(best) > k {
			best = best[:k]
		}
	}
	return best
}
