# Benchmarks

> **Status: mostly targets.** The first measurements, from the Phase 1 spikes, are in
> [Results](#results). Every number in a *Results* table must
> come from a recorded run (command, git sha, machine, date). Targets are hypotheses to test,
> not claims. See [design.md](design.md) for the components and [concepts.md](concepts.md) for the
> terms.

## Why this file exists

The project's goal is to *prove* speed. Proof needs three things: a fixed definition of each
metric, a repeatable way to measure it, and a recorded result. Several targets are also enforced
as CI tests, so a regression fails the build instead of quietly shipping.

## Latency budget (one live chat turn, warm Lambda)

How the time a visitor waits should break down. Targets are p50 / p95.

| Step | Target p50 | Target p95 | Notes |
|---|---|---|---|
| Rate limit (DynamoDB) | 5 ms | 15 ms | one conditional update |
| Auth ([JWT](concepts.md#jwt) verify) | < 0.1 ms | < 0.1 ms | local signature check, cached keys |
| Rewrite follow-up | 0 / 250 ms | 0 / 400 ms | skipped on first message |
| [Embed](concepts.md#embedding) question | 40 ms | 90 ms | network call; see [[D1]](design.md#d1-embedding-model) in design |
| **Retrieval (C++)** | **0.3 ms** | **1 ms** | in-process, enforced in CI |
| Inference TTFT | 250 ms | 600 ms | Groq, includes reasoning start |
| **First visible thought (server)** | **≤ 350 ms** | **≤ 800 ms** | sum of the steps above |
| Platform overhead | 150 ms | 300 ms | browser ↔ CloudFront ↔ Lambda; not under our code's control |
| **First visible thought (visitor)** | **≤ 500 ms** | **≤ 1 s** | what the visitor feels: server + platform overhead |
| Full answer (~300 tokens) | 1.0 s | 2.0 s | ~500 tok/s on gpt-oss-120b |
| Judge verdict after answer | 300 ms | 700 ms | gpt-oss-20b |
| Suggested question (pre-verified) | 30 ms | 80 ms | no inference |

**[Cold start](concepts.md#cold-start)** (first request after idle) adds init time: Go runtime + index load + secrets.
Target **< 400 ms** added. A scheduled keep-warm ping makes this rare.

## Metric definitions

| Metric | Definition |
|---|---|
| **[TTFT](concepts.md#ttft)** (time to first token) | Request sent to provider → first reasoning *or* text token received |
| **First visible thought (server)** | Server receives `POST /chat` → first `thought` event flushed |
| **First visible thought (visitor)** | Browser sends `POST /chat` → first `thought` event received by the page. Measured in the browser with `performance.now()` |
| **Platform overhead** | Client sends a request → first byte of a response whose server-side work is ~0 ms. Network, CloudFront and the Lambda invoke; spikes measure it with `watch.sh` |
| **[Tokens/s](concepts.md#tokens-per-second)** | Output tokens ÷ (last token time − first token time). Excludes TTFT. |
| **Full answer** | Request received → `token` stream ends |
| **Retrieval latency** | Wall time of one `rag_search` call, measured in-process |
| **[cgo](concepts.md#cgo) overhead** | Go→C round trip for an empty C function |
| **Cold start** | Lambda `Init Duration` from the REPORT log line |
| **Cache hit** | Provider-reported cached input tokens > 0 ([prompt caching](concepts.md#prompt-caching)) |

All server-side timings use Go's monotonic clock (`time.Since`) and are the same numbers shown on
the action cards and stored in the `steps` table, so the UI, the database and this file can never
disagree.

## Methodology

- **[Percentiles](concepts.md#percentiles), not averages.** Report p50, p95 and p99. A mean hides the slow requests people
  actually notice.
- **Warm-up first.** Discard the first 10 iterations (caches, CPU frequency scaling, JIT in the
  providers).
- **Enough samples.** At least 1,000 iterations for in-process benchmarks; at least 50 requests for
  network benchmarks, spread over time so one quiet minute doesn't flatter the numbers.
- **Record the environment.** Machine/CPU, Lambda memory size, region, model id, git sha, date.
- **Same corpus.** Benchmarks run against a fixed synthetic corpus generated in code from a fixed
  seed (`engine/bench/bench.cc`: Zipf-distributed words, random unit vectors), not the live
  corpus that changes as I edit it.
- **Compare like with like.** Local (WSL2 laptop) and Lambda numbers go in separate tables.
- **Never edit a result by hand.** Results are pasted from tool output.

## Benchmarks and how to run them (planned)

| Benchmark | Tool | Command | CI gate |
|---|---|---|---|
| C++ retrieval, 2k chunks × 512 dims | nanobench (single header) | `make bench-engine` | p99 < 1 ms on CI runner |
| C++ retrieval scaling (1k → 50k chunks) | nanobench | `make bench-engine-scale` | none (informational) |
| Pure-Go vector baseline, 2k × 512 | Go harness | `make bench-go` | none (informational) |
| cgo round-trip overhead | `go test -bench` | `make bench-cgo` | none |
| Go hybrid path (embed stubbed) | `go test -bench` | `make bench-orchestrator` | none |
| [SSE](concepts.md#sse) end-to-end, fake model | Go test client | `make bench-sse` | first event < 20 ms |
| Provider TTFT and tokens/s | Go harness, real APIs | `make bench-models` | none (costs tokens) |
| Deployed end-to-end | Go harness against the live URL | `make bench-live` | none |
| CSS size ([budget](concepts.md#performance-budget)) | `gzip -c \| wc -c` | `make css-check` | < 15 KB |

`bench-models` and `bench-live` spend free-tier quota, so they run on demand, never in CI.

## Results

### Local (WSL2, Intel Core Ultra 7 258V, 8 cores, 15 GB RAM)

Retrieval rows: one `rag_search` call per sample through the C ABI on a loaded index, k = 10,
mask `0x1` (one chunk in eight private), 100 warm-up calls then 2,000 timed (500 for scaling),
GCC 15.2 `-O3`, no `-march`.

| Date | Git sha | Benchmark | p50 | p95 | p99 | Notes |
|---|---|---|---|---|---|---|
| 2026-11-01 | #33 | C++ hybrid, 2k × 512, single-sum `dot()` | 0.371 ms | 0.459 ms | 0.551 ms | Before vectorising; three runs gave p99 0.52–0.70 ms |
| 2026-11-01 | #33 | C++ hybrid, 2k × 512, 16-sum `dot()` | 0.461 ms | 0.601 ms | 1.017 ms | Slower than one sum; not used |
| 2026-11-01 | #33 | **C++ hybrid, 2k × 512, 8-sum `dot()`** | **0.122 ms** | **0.157 ms** | **0.225 ms** | Shipped. GCC reports the loop vectorised with 16-byte (SSE2) vectors; three runs gave p99 0.23–0.32 ms |
| 2026-11-01 | #33 | C++ vector-only, 2k × 512, 8-sum | 0.097 ms | 0.117 ms | 0.182 ms | Same-day comparison with the Go row below |
| 2026-11-01 | #33 | Pure-Go vector top-k, 2k × 512 | 0.408 ms | 0.492 ms | 0.692 ms | Same contract and 8 independent sums; Go 1.27.1 doesn't vectorise. Not through cgo |
| 2026-11-01 | #33 | C++ hybrid scaling, 1k × 512 | 0.065 ms | 0.076 ms | 0.091 ms | `make bench-engine-scale` |
| 2026-11-01 | #33 | C++ hybrid scaling, 5k × 512 | 0.312 ms | 0.463 ms | 0.549 ms | |
| 2026-11-01 | #33 | C++ hybrid scaling, 10k × 512 | 1.004 ms | 1.240 ms | 1.578 ms | Over the gate. The 5k → 10k step is worse than linear; vectors are 20 MB here, likely past the CPU cache (not verified) |
| 2026-11-01 | #33 | C++ hybrid scaling, 20k × 512 | 2.275 ms | 2.749 ms | 3.727 ms | |
| 2026-11-01 | #33 | C++ hybrid scaling, 50k × 512 | 5.600 ms | 6.351 ms | 8.013 ms | |
| 2026-11-01 | before #36 | Index load (`rag_index_load`), 2k × 512, 5.5 MB file | 16.4 ms | — | — | Median of 11 (two runs: 16.4, 16.6 ms). Byte-at-a-time iostream read |
| 2026-11-01 | #36 | Index load (`rag_index_load`), 2k × 512, 5.5 MB file | 11.2 ms | — | — | Median of 11 (two runs: 11.2, 11.5 ms). One sized `fread`; the rest is parsing and building postings. Part of every cold start |

### CI runner (GitHub Actions `ubuntu-latest`)

Same benchmark as the local table, run by `make bench-engine` in the Test job. Shared runners
vary, so the gate has wide headroom. CPU model not recorded.

| Date | Git sha | Benchmark | p50 | p95 | p99 | Notes |
|---|---|---|---|---|---|---|
| 2026-11-01 | #32 (throwaway) | C++ hybrid, 2k × 512, single-sum `dot()` | 0.454 ms | 0.472 ms | 0.481 ms | Passed the gate before vectorising; see the plan's deviations log |
| 2026-11-01 | #32 (throwaway) | C++ hybrid, single-sum `dot()` slowed 4× on purpose | 3.352 ms | 3.381 ms | 3.439 ms | Gate failed CI, as intended |
| 2026-11-01 | #33 | **C++ hybrid, 2k × 512, 8-sum `dot()`** | **0.086 ms** | **0.094 ms** | **0.099 ms** | Shipped; gate green with 10× headroom |
| 2026-11-01 | #33 | C++ vector-only, 2k × 512, 8-sum | 0.060 ms | 0.067 ms | 0.069 ms | |

### AWS Lambda

| Date | Git sha | Memory | Arch | Benchmark | p50 | p95 | p99 | Notes |
|---|---|---|---|---|---|---|---|---|
| 2026-10-31 | spike 1.3 | 128 MB | x86_64 | Platform overhead, Function URL direct, warm | 110 ms | 132 ms | — | n = 10; first SSE event, client in WSL2 over home internet, includes SigV4; spike code, not the orchestrator |
| 2026-10-31 | spike 1.4 | 128 MB | x86_64 | Platform overhead, through CloudFront + OAC, warm | 180 ms | — | — | n = 1 (POST with body hash); too few runs for p95 |
| 2026-10-31 | spike 1.4 | 128 MB | x86_64 | First event through CloudFront, cold | 463 ms | — | — | n = 1, right after deploy; includes Lambda init and a new CloudFront connection |
| 2026-10-31 | spike 1.3 | 128 MB | x86_64 | Cold start (`Init Duration`), Go runtime only | 68 ms | 72 ms | — | n = 6; no index load or secrets yet. 128 MB is Lambda's smallest CPU share; the orchestrator's memory size is chosen in Phase 7 |
| 2026-11-01 | spike 1.5 | 128 MB | x86_64 | Cold start (`Init Duration`), Go + AWS SDK config + SSM SecureString read | 205 ms | — | — | n = 1; against 1.3's 68 ms for Go alone, loading AWS config and reading the key adds about 140 ms of the 400 ms cold-start target. Max memory used 33 MB |

### Model providers

| Date | Provider | Model | TTFT p50 | TTFT p95 | Tokens/s p50 | Cached tokens | Notes |
|---|---|---|---|---|---|---|---|
| 2026-11-01 | Groq, from Lambda (128 MB, x86_64, us-east-1) | `openai/gpt-oss-120b` | 198 ms | 464 ms | 483 | 256 on 7 of 20 turn-2 requests; 0 on turn 1 | n = 20 after 1 warm-up, 10 s apart; spike 1.5 at `bfd5c26`; default reasoning effort. TTFT is turn 1 (question + one tool, 185 input tokens), p99 811 ms; turn 2 (with the tool result, 304–560 input tokens) p50 215 ms, p95 354 ms. Tokens/s from turn 2 (271–447 output tokens incl. reasoning); slowest 5% 354 |

## Questions the benchmarks should answer

1. Is C++ actually faster than plain Go for this search, and by how much? (Benchmark a pure-Go
   baseline too; if C++ isn't clearly faster, say so.)
   **Answer (2026-11-01, local):** yes, about 4× for vector top-k (p50 0.097 ms vs 0.408 ms), because
   GCC vectorises the loop and Go's compiler doesn't. In absolute terms both are under 0.5 ms
   against a ~200 ms model TTFT, so the gain is headroom for a larger corpus, not something a
   visitor would notice.
2. At what corpus size does [brute force](concepts.md#brute-force-vs-approximate-search) stop
   meeting the 1 ms target?
   **Answer (2026-11-01, local):** between 5k chunks (p99 0.55 ms) and 10k (p99 1.58 ms) at 512
   dimensions. A portfolio corpus is expected to be well under 2k chunks.
3. Does [int8 quantization](concepts.md#quantization) change the search results? (Measure
   retrieval [hit@5](concepts.md#hit-at-k) alongside speed.)
4. x86 ([AVX2](concepts.md#simd)) vs ARM Graviton (NEON) on Lambda: speed and cost.
5. How much does [prompt caching](concepts.md#prompt-caching) cut TTFT in practice?
6. How often does the Groq free tier push requests to the Bedrock fallback?
