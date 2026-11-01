# Spike 1.5: Groq from Lambda

**Question:** can a Go Lambda stream gpt-oss-120b from Groq with its reasoning and its answer
text arriving as separate events, round-trip one tool call, and do it fast enough for the
[latency budget](../../docs/benchmarks.md#latency-budget-one-live-chat-turn-warm-lambda)?

**Answer: yes, but not through langchaingo.** From a 128 MB Lambda, 20 of 20 runs streamed
reasoning and text separately and round-tripped the tool call. Time to first token p50 was
198 ms (p95 464 ms), and the answer streamed at 483 tokens/s p50 (slowest 5%: 354). Findings are
recorded in the [plan](../../docs/plan.md#spike-findings) and the numbers in
[benchmarks](../../docs/benchmarks.md#model-providers).

## Why not langchaingo

The design called for langchaingo's OpenAI provider pointed at Groq. Its latest release,
v0.1.15, reads streamed reasoning only from `delta.reasoning_content`, the field DeepSeek
uses. Groq's gpt-oss models send `delta.reasoning`, which langchaingo has no field for, so
the JSON decoder silently drops it. There is no way to add raw request fields or read unknown
response fields, and tool-call deltas reach the streaming callback mixed in with the answer
text.

A scratch program against a local fake server showed it: streamed reasoning `""`, text `"The
answer is 4."`. `TestStreamParsesLiveRecording` shows the same on a real Groq stream: with
langchaingo's field name, 30 reasoning events become 0. Importing langchaingo would also have
pulled in 290 modules. The client here uses only Go's standard library: about 330 lines, half of them types.

## What's here

| Path | Purpose |
|---|---|
| `groq/` | Streaming client for Groq's OpenAI-compatible API: `Reasoning`, `Text` and `ToolCall` events; TTFT and tokens/s as defined in the benchmarks; typed HTTP errors (429, 5xx) for a future router. Tests replay hand-written and recorded streams |
| `roundtrip/` | The measured scenario: one question that needs one tool call (`lookup_project`), answered over two streamed turns. The tool returns a codename found nowhere else, so an answer that repeats it proves the result reached the model |
| `ssmkey/` | Reads the key from the SSM SecureString `/cferrante-dev/groq-api-key` with decryption |
| `cmd/bootstrap/` | The Lambda. Reads the key once at cold start, runs one round trip per invoke and returns its report; a failed request is a failed report, not a Lambda error |
| `cmd/local/` | The same run from the laptop, reading the key from SSM with the SSO session; `-record DIR` saves raw response streams (responses only: requests carry the key) |
| `cmd/stats/` | Summarises a bench: one row per run, nearest-rank percentiles, PASS/FAIL |
| `deploy.sh`, `bench.sh`, `teardown.sh` | Idempotent deploy (no Function URL; the role may read only the key parameter), the test and benchmark, clean removal |

The key is stored by [`infra/bootstrap/aws/06-groq-key.sh`](../../infra/bootstrap/aws/README.md),
which owns it; `teardown.sh` leaves it in place for Phase 5.

## What a real stream looks like

From the recording in `groq/testdata/live_tool_call.sse`:

- Reasoning arrives as `"delta":{"reasoning":"…","channel":"analysis"}`, one or a few words
  per chunk. The answer arrives as `"delta":{"content":"…"}`.
- The tool call arrives **whole in one chunk**, id, name and arguments together, followed by
  `finish_reason: "tool_calls"`. The client still joins argument fragments, as OpenAI sends
  them.
- Usage arrives twice: in `x_groq.usage` on the last choice chunk and in a final usage-only
  chunk (`stream_options.include_usage` is accepted). Groq also reports `queue_time` and
  `completion_time`.
- Groq's prompt cache hit on its own: 256 cached tokens on 7 of 20 second-turn requests.

## Two surprises

- **gpt-oss writes Unicode hyphens.** Every answer in the bench (20 of 20) wrote the codename
  `heron-42` with U+2011, a non-breaking hyphen. The first live run failed the byte-exact check
  for that reason. Dashes are now normalised before comparing, and any exact match on model
  output later (canary strings, citations) must do the same.
- **Turn 1's tokens/s is meaningless.** Its output tokens include the tool-call arguments, which
  arrive in one chunk after the timed tokens; one run computed 939,006 tokens/s. Tokens/s is
  taken from turn 2, the ~350-token answer.

## Run it

```bash
aws sso login --profile cferrante-dev-admin
../../infra/bootstrap/aws/06-groq-key.sh          # once: stores the key
go run ./cmd/local -record build/streams           # optional: one run from the laptop
./deploy.sh
./bench.sh                                         # 1 warm-up + 20 runs, 10 s apart
./teardown.sh
```

`bench.sh` prints the cold start, every run and the percentiles, and passes only if every run
streamed reasoning and text separately and round-tripped the tool call. Output is saved to
`build/bench/`. On `HTTP 429`, re-run with `-g 20` to space the runs out.

Cost: $0. The bench makes 42 Groq requests within the free tier; Lambda and SSM are within
their free tiers.
