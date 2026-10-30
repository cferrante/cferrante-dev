# Benchmarks

> **Status: targets only. Nothing has been measured yet.** Every number in a *Results* table must
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
| **First visible thought** | **≤ 350 ms** | **≤ 800 ms** | what the visitor feels |
| Full answer (~300 tokens) | 1.0 s | 2.0 s | ~500 tok/s on gpt-oss-120b |
| Judge verdict after answer | 300 ms | 700 ms | gpt-oss-20b |
| Suggested question (pre-verified) | 30 ms | 80 ms | no inference |

**[Cold start](concepts.md#cold-start)** (first request after idle) adds init time: Go runtime + index load + secrets.
Target **< 400 ms** added. A scheduled keep-warm ping makes this rare.

## Metric definitions

| Metric | Definition |
|---|---|
| **[TTFT](concepts.md#ttft)** (time to first token) | Request sent to provider → first reasoning *or* text token received |
| **First visible thought** | Server receives `POST /chat` → first `thought` event flushed |
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
- **Same corpus.** Benchmarks run against a fixed, versioned fixture corpus (`engine/testdata/`),
  not the live corpus that changes as I edit it.
- **Compare like with like.** Local (WSL2 laptop) and Lambda numbers go in separate tables.
- **Never edit a result by hand.** Results are pasted from tool output.

## Benchmarks and how to run them (planned)

| Benchmark | Tool | Command | CI gate |
|---|---|---|---|
| C++ retrieval, 2k chunks × 512 dims | nanobench (single header) | `make bench-engine` | p99 < 1 ms on CI runner |
| C++ retrieval scaling (1k → 50k chunks) | nanobench | `make bench-engine-scale` | none (informational) |
| cgo round-trip overhead | `go test -bench` | `make bench-cgo` | none |
| Go hybrid path (embed stubbed) | `go test -bench` | `make bench-orchestrator` | none |
| [SSE](concepts.md#sse) end-to-end, fake model | Go test client | `make bench-sse` | first event < 20 ms |
| Provider TTFT and tokens/s | Go harness, real APIs | `make bench-models` | none (costs tokens) |
| Deployed end-to-end | Go harness against the live URL | `make bench-live` | none |
| CSS size ([budget](concepts.md#performance-budget)) | `gzip -c \| wc -c` | `make css-check` | < 15 KB |

`bench-models` and `bench-live` spend free-tier quota, so they run on demand, never in CI.

## Results

### Local (WSL2, 8 cores, 15 GB RAM)

| Date | Git sha | Benchmark | p50 | p95 | p99 | Notes |
|---|---|---|---|---|---|---|
| — | — | — | — | — | — | not yet measured |

### AWS Lambda

| Date | Git sha | Memory | Arch | Benchmark | p50 | p95 | p99 | Notes |
|---|---|---|---|---|---|---|---|---|
| — | — | — | — | — | — | — | — | not yet measured |

### Model providers

| Date | Provider | Model | TTFT p50 | TTFT p95 | Tokens/s p50 | Cached tokens | Notes |
|---|---|---|---|---|---|---|---|
| — | — | — | — | — | — | — | not yet measured |

## Questions the benchmarks should answer

1. Is C++ actually faster than plain Go for this search, and by how much? (Benchmark a pure-Go
   baseline too; if C++ isn't clearly faster, say so.)
2. At what corpus size does [brute force](concepts.md#brute-force-vs-approximate-search) stop
   meeting the 1 ms target?
3. Does [int8 quantization](concepts.md#quantization) change the search results? (Measure
   retrieval [hit@5](concepts.md#hit-at-k) alongside speed.)
4. x86 ([AVX2](concepts.md#simd)) vs ARM Graviton (NEON) on Lambda: speed and cost.
5. How much does [prompt caching](concepts.md#prompt-caching) cut TTFT in practice?
6. How often does the Groq free tier push requests to the Bedrock fallback?
