// Command stats summarises bench.sh's run reports (one JSON object per
// line): a row per run, then TTFT p50/p95/p99 and tokens/s p50/p5/p1 (for
// throughput the slow tail is the low end), and a PASS or FAIL verdict.
// Percentiles use the nearest-rank method on the runs given.
//
//	go run ./spikes/groq-lambda/cmd/stats [-want N] FILE.jsonl
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"slices"
	"strings"

	"github.com/cferrante/cferrante-dev/spikes/groq-lambda/roundtrip"
)

// percentile returns the nearest-rank p-th percentile (0 < p ≤ 1) of xs
// without reordering xs.
func percentile(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	rank := int(math.Ceil(p * float64(len(s))))
	return s[max(rank, 1)-1]
}

// summarize renders the report table and decides the verdict: every run OK
// and at least want runs.
func summarize(reps []roundtrip.Report, want int) (string, bool) {
	var b strings.Builder
	fmt.Fprintf(&b, "%3s  %-4s  %9s  %9s  %9s  %9s  %6s  %6s  %s\n",
		"run", "ok", "ttft1 ms", "tok/s 1", "ttft2 ms", "tok/s 2", "out 2", "cached", "events r/t (turn 2)")
	var ttft1, ttft2, tps2 []float64
	bad := 0
	for i, r := range reps {
		okStr := "ok"
		if !r.OK {
			okStr, bad = "FAIL", bad+1
		}
		fmt.Fprintf(&b, "%3d  %-4s  %9.1f  %9.1f  %9.1f  %9.1f  %6d  %6d  %d/%d\n", i+1, okStr,
			r.Turn1.TTFTms, r.Turn1.TokensPerSecond, r.Turn2.TTFTms, r.Turn2.TokensPerSecond,
			r.Turn2.TokensOut, r.Turn2.CachedTokens, r.Turn2.ReasoningEvents, r.Turn2.TextEvents)
		for _, p := range r.Problems {
			fmt.Fprintf(&b, "     ↳ %s\n", p)
		}
		if r.OK {
			ttft1 = append(ttft1, r.Turn1.TTFTms)
			ttft2 = append(ttft2, r.Turn2.TTFTms)
			tps2 = append(tps2, r.Turn2.TokensPerSecond)
		}
	}
	line := func(name, unit string, xs []float64) {
		fmt.Fprintf(&b, "%-16s p50 %.0f  p95 %.0f  p99 %.0f %s\n", name, percentile(xs, .50),
			percentile(xs, .95), percentile(xs, .99), unit)
	}
	fmt.Fprintf(&b, "\n%d runs, %d ok (statistics over ok runs)\n", len(reps), len(reps)-bad)
	line("TTFT turn 1", "ms", ttft1)
	line("TTFT turn 2", "ms", ttft2)
	// Higher is better, so the slow tail is the low end: p5 and p1.
	fmt.Fprintf(&b, "%-16s p50 %.0f  p5 %.0f  p1 %.0f\n", "tokens/s turn 2",
		percentile(tps2, .50), percentile(tps2, .05), percentile(tps2, .01))

	pass := bad == 0 && len(reps) >= want
	switch {
	case pass:
		b.WriteString("PASS: every run streamed reasoning and text separately and round-tripped the tool call\n")
	case bad > 0:
		fmt.Fprintf(&b, "FAIL: %d run(s) failed\n", bad)
	default:
		fmt.Fprintf(&b, "FAIL: %d runs, want %d\n", len(reps), want)
	}
	return b.String(), pass
}

func main() {
	want := flag.Int("want", 20, "runs required for PASS")
	flag.Parse()
	if flag.NArg() != 1 {
		log.Fatal("usage: stats [-want N] FILE.jsonl")
	}
	f, err := os.Open(flag.Arg(0))
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	var reps []roundtrip.Report
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var r roundtrip.Report
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			log.Fatalf("bad report line: %v", err)
		}
		reps = append(reps, r)
	}
	if err := sc.Err(); err != nil {
		log.Fatal(err)
	}
	out, pass := summarize(reps, *want)
	fmt.Print(out)
	if !pass {
		os.Exit(1)
	}
}
