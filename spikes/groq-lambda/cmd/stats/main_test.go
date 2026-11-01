package main

import (
	"strings"
	"testing"

	"github.com/cferrante/cferrante-dev/spikes/groq-lambda/roundtrip"
)

func TestPercentileNearestRank(t *testing.T) {
	var xs []float64
	for i := 20; i >= 1; i-- { // unsorted on purpose
		xs = append(xs, float64(i))
	}
	for _, c := range []struct{ p, want float64 }{{0.50, 10}, {0.95, 19}, {0.99, 20}, {1, 20}} {
		if got := percentile(xs, c.p); got != c.want {
			t.Errorf("p%v of 1..20 = %v, want %v", c.p*100, got, c.want)
		}
	}
	if got := percentile([]float64{7}, 0.95); got != 7 {
		t.Errorf("p95 of one value = %v, want 7", got)
	}
	if xs[0] != 20 {
		t.Error("percentile sorted its input in place")
	}
}

func ok(ttft, tps float64) roundtrip.Report {
	return roundtrip.Report{OK: true, Problems: []string{},
		Turn1: roundtrip.Turn{TTFTms: ttft}, Turn2: roundtrip.Turn{TTFTms: ttft * 2, TokensPerSecond: tps}}
}

func TestSummarizePassesWhenAllRunsOK(t *testing.T) {
	out, pass := summarize([]roundtrip.Report{ok(200, 500), ok(300, 400), ok(100, 600)}, 3)
	if !pass {
		t.Errorf("pass = false for 3 good runs of 3:\n%s", out)
	}
	// Throughput's slow tail is its low end: tokens/s reports p5, not p95.
	for _, want := range []string{"TTFT turn 1", "p50 200", "tokens/s turn 2", "p50 500  p5 400", "PASS"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary lacks %q:\n%s", want, out)
		}
	}
}

func TestSummarizeFailsOnBadOrMissingRuns(t *testing.T) {
	bad := ok(100, 100)
	bad.OK, bad.Problems = false, []string{"turn 2: no reasoning events"}
	out, pass := summarize([]roundtrip.Report{ok(200, 500), bad}, 2)
	if pass || !strings.Contains(out, "no reasoning events") {
		t.Errorf("pass = %v, want false and the problem shown:\n%s", pass, out)
	}
	if out, pass := summarize([]roundtrip.Report{ok(200, 500)}, 20); pass {
		t.Errorf("pass = true with 1 of 20 runs:\n%s", out)
	}
}
