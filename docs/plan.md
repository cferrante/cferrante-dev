# Implementation plan

> **Status: proposed.** The step-by-step route from an empty repo to a launched receptionist.
> Implements [design.md](design.md); speed targets in [benchmarks.md](benchmarks.md); terms in
> [concepts.md](concepts.md). Deviations and code-review outcomes are recorded at the
> [end of this file](#deviations-log) as we go.

## How this plan works

**Order: riskiest first, then inside-out.** Spikes prove the assumptions most likely to sink the
architecture before anything depends on them. Then components are built from the core outward
(engine → orchestrator → UI → providers → evals → cloud), each fully tested before the next
builds on it.

**Every step follows the same loop:**
1. Branch from `main` (`feat/…`, `spike/…`, `docs/…`).
2. **Write the test first and watch it fail for the right reason.** Be suspicious of a test that
   passes immediately; break the code on purpose to prove the test catches it.
3. Make it pass with the simplest code; refactor.
4. One logical change per commit, tests green at every commit.
5. PR → review → merge. UI steps include numbered click-through steps to run by hand.

**Each step lists:** the failing test that starts it, what "done" means, and its cost. Steps
marked 🧑 need you (accounts, payments, decisions); everything else I can drive.

## Milestones

| Milestone | Proves | After phase |
|---|---|---|
| **M1 Risks retired** | cgo-on-Lambda, Go SSE streaming, CloudFront passthrough, Groq speed all work | 1 |
| **M2 Local end-to-end** | Full chat with thought stream and action cards on my laptop via Ollama | 4 |
| **M3 Safe and smart** | Real models, judge, golden + adversarial evals passing | 6 |
| **M4 Private deploy** | Running on AWS, reachable only by me | 7 |
| **M5 Public launch** | Invite-only features, logging, privacy, measured benchmarks | 10 |

## Prerequisites and pending decisions

| Item | Needed by | Status |
|---|---|---|
| 🧑 Site domain (decides transfer vs register, ≈ $15/yr) | 7.2 | ✅ `cferrante.dev` registered in Route 53 |
| 🧑 [D1] embedding model | 5.3 | proposed: Titan v2 |
| 🧑 [D2] notification channel | 8.4 | open |
| 🧑 [D3] migrations tool | 9.1 | open |
| 🧑 [D4] thought stream on fallback | 5.2 | proposed: narration only |
| 🧑 [D5] Lambda architecture | 7.6 | proposed: x86_64, revisited by measurement in 7.6 |
| 🧑 Doc-link checker in CI? | 0.2 | ✅ yes (done) |
| 🧑 JS test runner for the frontend (no npm toolchain) | 4.2 | proposed: Deno (single binary); alternative: Node's built-in `node --test` |
| 🧑 Groq account + API key (free) | 1.5 | ✅ key in SSM via `infra/bootstrap/aws/06-groq-key.sh` |
| 🧑 Neon account (free) | 9.1 | not started |

([D1]–[D5] are defined in [design §12](design.md#12-open-decisions).)

**Toolchain (checked 2026-10-30):** Go 1.27.1, g++ 15.2, CMake 4.2.3, Make 4.4.1, AWS CLI
2.37.5, Ollama 0.33.3, gh 2.46.0 are installed (and `shellcheck` for `make lint`: `sudo apt install shellcheck`; CI runners have it). OpenTofu 1.13.1 (standalone binary, user-local). **Missing:** Tailwind standalone
CLI (4.1), Zig (only if 1.2 needs it). Local glibc is **2.43**; Lambda's Amazon Linux 2023 has
**2.34**, which is why step 1.2 exists. Disk is checked (`df -h /`) before every install or
download; stop if under 5 GB free.

---

## Phase 0 — Foundations (no cloud, $0)

**✅ Done 2026-10-30** (PRs #4, #5, #7). `main` is protected: **Test** and **Doc links** must pass,
including for admins; no force pushes or deletions.

| Step | Test that must fail first | Done when |
|---|---|---|
| **0.1** Repo skeleton: `go.mod`, `Makefile` (`test`, `lint`, `fmt`), `.editorconfig`, expanded `.gitignore` | `make test` fails: no targets | `make test` runs (empty) green; `go vet` clean |
| **0.2** Doc-link checker: `scripts/check-doc-links.sh` + GitHub Actions job | CI run on a branch with a deliberately broken anchor fails | CI green on `main`; a broken link fails the PR |
| **0.3** CI baseline: Actions workflow running `make test` on every PR | Workflow red on a deliberately failing test | Required check on PRs to `main` |

Cost: $0 (Actions are free for public repos).

## Phase 1 — Spikes: retire the big risks (M1)

Spikes are **experiments**, on `spike/…` branches. Their code may be thrown away; their
**findings are kept**: recorded in this file and, for timings, in
[benchmarks.md](benchmarks.md#results).

| Step | Test that must fail first | Done when |
|---|---|---|
| **1.1** 🧑 AWS account setup: root MFA, IAM Identity Center admin user, `aws sso login`, separate project account with a **$1 Budget alarm** ([bootstrap scripts](../infra/bootstrap/aws/README.md)), region `us-east-1`; install OpenTofu | `aws sts get-caller-identity` fails (no session); `check.sh` shows ✗ | It returns the SSO admin identity; `check.sh` all ✓; budget alarm email confirmed. **✅ 2026-10-31** (OpenTofu 1.13.1, checksum-verified) |
| **1.2** **cgo binary on Lambda.** C++ `add()` in a static lib, called from Go via cgo, built on Ubuntu with `-static-libstdc++ -static-libgcc`; deployed as `provided.al2023` | A local check that the binary's highest required `GLIBC_` symbol version is ≤ 2.34 fails on a naive build | That check passes, **and** a Lambda invoke returns the C++ result on x86_64 (and arm64 if time). If static flags can't get under 2.34, try `zig cc -target x86_64-linux-gnu.2.34` and record it. **✅ 2026-10-31** (x86_64, fully static; see [findings](#spike-findings)) |
| **1.3** **Go response streaming → SSE.** Lambda Function URL in `RESPONSE_STREAM` mode emitting 10 SSE events 200 ms apart | Go unit test on the handler with a fake stream writer fails | `curl -N` shows events arriving **one by one** (timestamps 200 ms apart), not all at the end. **✅ 2026-10-31** (see [findings](#spike-findings)) |
| **1.4** **CloudFront passthrough.** CloudFront (with origin access control) in front of the 1.3 URL | Same `curl -N` timing test against the CloudFront URL | Events still arrive incrementally; direct Function URL access is refused **✅ 2026-10-31** (see [findings](#spike-findings)) |
| **1.5** 🧑 **Groq from Lambda.** langchaingo OpenAI provider → Groq gpt-oss-120b, streaming reasoning + text + one tool call, timed | Test asserting reasoning *and* text chunks arrive separately fails | TTFT and tokens/s for 20 runs recorded in benchmarks; tool call round-trips **✅ 2026-11-01** (standard-library client, not langchaingo; see [findings](#spike-findings)) |

**Exit criteria (M1):** ✅ met 2026-11-01. All five pass, *or* a failed spike triggers a recorded design change
before Phase 2. Cost: $0 (free tiers; spike resources torn down afterwards).

## Phase 2 — Retrieval engine, C++ ($0)

**✅ Done 2026-11-01** (PRs #26–#31, #33 and the 2.7 PR). Hybrid search p99 0.099 ms on the CI
runner against a 1 ms gate; see [benchmarks](benchmarks.md#results).

Built with CMake (C++20, GCC), tested with doctest, benchmarked with nanobench. Everything here
runs locally and in CI. The contract is [design §5](design.md#5-retrieval-engine-c), including
the tokenizer, file format, edge cases and builder ABI.

**Dependencies:** doctest and nanobench are single headers, copied into `engine/third_party/`
and pinned by release tag and SHA-256 (recorded in a README there). No download at build time,
so CI works offline and builds the same way every time; about 300 KB in the repo. No new
system packages: g++ 15, CMake and ninja are already installed and on CI runners.

**Portability rule:** no architecture-specific code (such as AVX2 intrinsics) without a portable
fallback that is tested and benchmarked alongside it. The hot loops are written so the compiler
can vectorize them on both x86_64 and arm64, keeping the Lambda architecture ([D5]) a build flag
rather than a rewrite.

| Step | Test that must fail first | Done when |
|---|---|---|
| **2.1** CMake project + vendored doctest; `normalize()` and the simplest `dot()` (one running sum); `make test` runs the C++ tests and CI builds them | Unit tests for known vectors (incl. zero vector) fail | Pass, with `-fsanitize=address,undefined` in the test build, locally and in CI **✅ 2026-11-01** (doctest v2.5.3, nanobench v4.6.0 vendored) |
| **2.2** Brute-force top-k with access mask | Test: a private chunk with the highest score is excluded for mask `0x1` | Correct order, ties by lower chunk id, k > count handled **✅ 2026-11-01** |
| **2.3** Tokenizer + BM25 over an inverted index | Tokenizer cases (case, punctuation, U+2011 splits, non-ASCII kept) and hand-computed scores for a 3-doc fixture fail | Tokens as specified; scores match to 1e-5 **✅ 2026-11-01** |
| **2.4** Reciprocal Rank Fusion and hybrid search, with the [design §5](design.md#5-retrieval-engine-c) edge cases | Fixture where fusion must reorder results; empty text → vector only; zero vector → BM25 only | Matches hand-computed ranking; every edge case tested **✅ 2026-11-01** |
| **2.5** Versioned index file and the full C ABI: builder (`rag_builder_*`), `rag_index_load`, `rag_search` | Round-trip test; loading a wrong-version, truncated or corrupt file must return an error, not crash | Round-trip exact; under ASan, a loop rejects every truncation length and a flipped bit at every byte; no exception escapes the ABI **✅ 2026-11-01** |
| **2.6** Benchmarks (nanobench, 2k × 512 chunks generated from a fixed seed) + a **pure-Go baseline** | CI gate "p99 < 1 ms" fails on the single-sum `dot()` (estimated ~1.3 ms on CI, to be measured) | `dot()` rewritten with independent accumulators the compiler vectorizes; gate green; C++ vs Go numbers recorded honestly in [benchmarks](benchmarks.md#results) **✅ 2026-11-01** (gate didn't fail first; see deviations) |
| **2.7** Go binding package (`engine/go`), compiling the engine sources through cgo | Go test calling `rag_search` via cgo fails (no binding) | Passes with plain `go test`, including `-race` with concurrent searches; indexer-style build → load → search round trip through Go **✅ 2026-11-01** |

If the 2.6 gate passes on the first run, the gate is proven instead with a deliberate slowdown,
and that is recorded as a deviation.

## Phase 3 — Orchestrator core, Go, fake model ($0)

Everything runs against a fake model and fake retriever until 3.5, which uses Ollama on the
laptop. Contracts: [design §3](design.md#3-streaming-protocol-sse) (stream, citations, turn
limits, `confirm_request`), [§4](design.md#4-model-layer) (models, router), [§5](design.md#5-retrieval-engine-c)
(corpus and indexer), [§6](design.md#6-tools-and-authorization) (caller, tools, pending actions)
and [§8](design.md#8-data-model-neon-postgres) (conversation history).

**Layout:** `orchestrator/{sse,model,model/fake,model/oai,agent,tools,corpus}` and
`orchestrator/cmd/{indexer,dev}`. The HTTP handler is a plain `http.Handler`, so Phase 7 only
wraps it for Lambda. No new Go dependencies.

| Step | Test that must fail first | Done when |
|---|---|---|
| **3.1** SSE writer enforcing the [contract rules](design.md#3-streaming-protocol-sse), with an injectable clock | Tests: unmatched `step_start`, event after `done`, missing keep-alive (fake clock), concurrent writers | All four contract rules enforced; `-race` clean with the keep-alive and the agent writing at once **✅ 2026-11-01** |
| **3.2** `Model` interface, fake model, router with fallback-before-first-token and a 5 s first-token timeout | Test: primary returns 429 → fallback used; error *after* first token → error reported, no fallback; primary silent past the timeout → fallback | All three behaviours proven **✅ 2026-11-01** |
| **3.3** The answer turn: `Retriever` interface (engine binding behind it), `ConversationStore` (in memory), prompt with numbered context, streaming, citations, turn limits | With fake model and retriever: events arrive as `step_start(retrieval)`, `step_end`, `thought`, `token`, `citation`, `done`; a `[2]` split across chunks yields one citation; an out-of-range `[9]` yields none; a cancelled turn still closes every step | The full turn works against fakes **✅ 2026-11-01** (engine-backed retriever moved to 3.4) |
| **3.4** Indexer CLI: corpus front matter, chunking (our own splitter: headings, paragraphs, then sentence boundaries, with overlap), embedding via interface (fake embedder in tests), index + sidecar written atomically | Test on a fixture corpus: expected chunk ids, boundaries and access bits; a missing `access` key is an error; a failed write leaves no file | Index loads in the engine and finds known chunks. Decide the deferred Unicode-space tokenizer question against real corpus text **✅ 2026-11-01** (Unicode spaces now split tokens) |
| **3.5** `make dev`: local HTTP server, SSE over Ollama via `model/oai` (the spike 1.5 client, moved and shared with Groq); a small reasoning model such as `qwen3:4b` (~2.6 GB) and `nomic-embed-text` (~0.3 GB); **disk check first** | Recorded Ollama stream replay: reasoning and text arrive as separate events; smoke test: `curl -N localhost` gets `thought`, `token`, `done` in order | Works end to end on the laptop, with click-through steps **✅ 2026-11-01** (37 s per answer on the CPU; see benchmarks) |
| **3.6** Tools: filtering, executor enforcement, confirmation through pending actions (`PendingActions`, in memory) | Test: an anonymous caller's forged `send_message` is refused; an unconfirmed action doesn't run; another caller's or an expired `action_id` is refused; a second confirm runs nothing | Refused with `authorized:false`; each action runs at most once |

Cost: $0. The only downloads are the two Ollama models in 3.5 (≈ 2.9 GB; RAM is 15 GB).

## Phase 4 — Frontend (M2, $0)

| Step | Test that must fail first | Done when |
|---|---|---|
| **4.1** Tailwind standalone CLI (pinned version, checksum verified, **disk check**), `make css` | CSS budget test (< 15 KB gzipped) and required-classes test fail (no CSS yet) | Both pass |
| **4.2** Split-pane page + SSE client (`fetch` stream reader) | JS unit test: parser handles events split across network chunks | Thought stream renders live against `make dev` |
| **4.3** Action cards with live durations, `data-state` styling, reduced motion | Test: every `step_start` creates a card; `step_end` fills duration | Cards light up and show real timings |
| **4.4** Citations + suggested-question chips (pre-verified answer path) | Test: chip request returns stored answer with no model call | Chips answer instantly; citations link to sources |

**M2:** full local experience. Click-through steps provided in the PR.

## Phase 5 — Real models (≈ $0)

| Step | Test that must fail first | Done when |
|---|---|---|
| **5.1** Groq provider (`model/groq`), recorded-response tests + opt-in live test | Recorded stream replay doesn't produce the expected events | Replays pass; live smoke passes on demand |
| **5.2** Bedrock `ConverseStream` fallback adapter; [D4] applied | Recorded replay test | Router falls back to it on a simulated Groq 429 |
| **5.3** Embeddings per [D1]; production index built | Test: query and index embedding model ids must match or startup fails | Index built with the production embedder |
| **5.4** Prompt ordering + cache point; constrained judge output | Test: second identical-prefix request reports `cached_tokens > 0` (live, opt-in); judge rejects non-schema output | Cache hits visible on the inference card; judge always schema-valid |

Cost: Groq free tier; Bedrock and Titan in cents.

## Phase 6 — Evals and safety (M3, $0 locally)

| Step | Test that must fail first | Done when |
|---|---|---|
| **6.1** Corpus v1: `faq/`, project summaries + details (🧑 your content) | Golden question with no supporting chunk fails hit@5 | Corpus reviewed by you |
| **6.2** Golden dataset + retrieval hit@5 gate in CI | Gate fails below threshold | Threshold set from the first honest baseline |
| **6.3** Answer evals with LLM judge (local Ollama, opt-in Groq) | Known-bad answer must score low | Judge agreement checked on a labelled sample |
| **6.4** Adversarial suite with canary strings | Each category has a case that fails before its defence exists | All categories pass; results stored for comparison |

## Phase 7 — Infrastructure and private deploy (M4)

| Step | Test that must fail first | Done when |
|---|---|---|
| **7.1** OpenTofu with S3 state backend; `tofu plan` in CI | `tofu validate` fails on empty config | Plan clean; state in S3 with locking |
| **7.2** 🧑 Domain + Route 53 + ACM + S3 + CloudFront flat-rate Free plan | `curl -I https://cferrante.dev` fails | HTTPS 200 with security headers; plan selection noted (may be console-only) |
| **7.3** Lambda + Function URL + origin access control, SSM secrets, DynamoDB rate limits, concurrency cap | Smoke test against deployed `/chat` fails | Streams through CloudFront; 11th anonymous message in 10 min is rate-limited; rate-limit and pending-action DynamoDB latency from Lambda recorded in [benchmarks](benchmarks.md#aws-lambda) against the 5 ms p50 / 15 ms p95 budget |
| **7.4** GitHub Actions deploy via **OIDC** (no stored AWS keys) | Deploy job fails without the role | Merge to `main` deploys; least-privilege role |
| **7.5** Keep-warm schedule, CloudWatch alarms, budget alarm at $5 | — (ops config, verified by triggering alarms) | Alarm test emails received |
| **7.6** arm64 comparison ([D5]): cross-build arm64 (fully static), CI job on GitHub's arm64 runners, deploy both architectures | Engine tests fail on the arm64 runner until the arm64 build exists | Engine latency, cold start and cost per 1k requests recorded for both in [benchmarks](benchmarks.md#aws-lambda); D5 decided from the numbers |

Site stays private (unlisted, chat behind a temporary allow-list) until M5.

## Phase 8 — Auth and invite-only features

| Step | Test that must fail first | Done when |
|---|---|---|
| **8.1** Cognito user pool: self-sign-up off, email OTP, `invitee` group | Sign-up API call must be rejected | Only admin-created users can sign in |
| **8.2** Go JWT verification (cached keys, groups → access mask) | Tests with expired, wrong-issuer, tampered tokens | All rejected; valid invitee gets mask `0x3` |
| **8.3** `invite` CLI (Cognito first, then Postgres record) | Test: Postgres failure leaves Cognito user intact and reports it | Invite email received end to end |
| **8.4** Invitee tools + confirm UI; [D2] notifications | Adversarial: anonymous + injected tool call fails | Confirmed request notifies me once (idempotent) |

## Phase 9 — Data (Neon)

| Step | Test that must fail first | Done when |
|---|---|---|
| **9.1** 🧑 Neon project in `us-east-1`; migrations per [D3] | Migration test against a throwaway branch database | Schema from [design §8](design.md#8-data-model-neon-postgres) applied |
| **9.2** Turn logging after `done` (2 s timeout, best-effort) | Test: DB down → visitor's stream unaffected, error logged | Rows written; latency unchanged |
| **9.3** 90-day retention job (scheduled Lambda) | Test with backdated rows | Old rows deleted, recent kept |
| **9.4** Eval results to Postgres | Run produces no rows | Runs comparable over time by git sha |

## Phase 10 — Launch (M5)

| Step | Done when |
|---|---|
| **10.1** Privacy notice by the chat box | Visible; links to retention policy |
| **10.2** Security review (`/security-review`) + fixes | Findings and outcomes recorded below |
| **10.3** Full benchmark run, deployed | [Results tables](benchmarks.md#results) filled from recorded runs |
| **10.4** README status → launched; remove allow-list | Public |

---

## Deviations log

Any change from this plan: what changed, why, and which step.

| Date | Step | Deviation | Reason |
|---|---|---|---|
| 2026-10-30 | 0.1 | Makefile skips Go steps until the first package exists | `go test ./...` and `go vet ./...` exit 1 on a module with no packages, which would fail CI for the wrong reason |
| 2026-10-30 | 0.1 | `.gitignore` gained `!.env.example` | Verification with `git check-ignore` showed `.env.*` also hid the committable template |
| 2026-10-30 | 0.2, 0.3 | "CI fails first" proven with throwaway draft PRs (#6 broken link, #8 failing test), closed afterwards | Keeps deliberately broken commits out of `main`'s history; force-pushing them away is blocked |
| 2026-10-30 | 0.3 | Required checks also apply to admins (`enforce_admins: true`) | Otherwise admin merges (including mine via your token) would silently bypass CI. Can be relaxed in repo settings in an emergency |
| 2026-10-31 | 7.2 | Project renamed `cferrante-dev`; site domain is `cferrante.dev` instead of `ferrante-ltd.com` | The hyphenated `.com` was never owned; repo, Go module and domain now share one name |
| 2026-10-31 | 1.1 | Project runs in its own AWS account in an Organization, not the existing account | The existing account already spends more than $1/month on unrelated resources, so its budget can't watch this project; isolation and least privilege for CI. See [design §13](design.md#13-aws-accounts-and-dns) |
| 2026-10-31 | 1.1, 7.2 | DNS zone for `cferrante.dev` moved to the project account now, not in 7.2; the registration stays in the management account | It was empty, so moving was risk-free, and OpenTofu can then manage records without cross-account access |
| 2026-10-31 | 1.1 | Account setup done by idempotent scripts in `infra/bootstrap/aws/` instead of console clicks; `make lint` now runs `shellcheck` | Repeatable and reviewable; `check.sh` acts as the step's test |
| 2026-10-31 | 1.2 | Static C++ runtime replaced by a fully static binary; the checker also rejects non-glibc shared libraries | The planned fix still needed `GLIBC_2.38`; a C++ runtime from Ubuntu won't match Lambda's even when glibc does |
| 2026-10-31 | 1.2 | Spike C++ is compiled by cgo from source in the Go package, not built as a CMake static library | The glibc question is the same either way; CMake arrives with the real engine in Phase 2 |
| 2026-10-31 | 1.3 | Function URL uses `AWS_IAM` auth and a SigV4-signed `curl`, not a public URL | No open endpoint even briefly; 1.4's CloudFront origin access control needs `AWS_IAM` anyway |
| 2026-10-31 | 1.3 | Pass rule is "arrivals spread like the sends" (≥ 3/4 of the send span), not "each gap ≈ 200 ms" | Per-gap counting failed a working stream after two network hiccups; the spread rule still fails buffering |
| 2026-10-31 | 1.4 | Also tested POST, with and without `x-amz-content-sha256` | `/chat` is a POST, and OAC for Lambda doesn't sign request bodies, so the browser must send the hash; worth proving before the frontend is designed |
| 2026-10-31 | benchmarks | "First visible thought" split into server (≤ 350 ms) and visitor (≤ 500 ms) targets, with a platform-overhead row | Spikes 1.3–1.4 measured ≈ 110–180 ms between client and Lambda that the server-side budget left out, though the row claimed to be "what the visitor feels" |
| 2026-10-31 | 2, 7.6 | Added the Phase 2 portability rule, step 7.6 and decision D5 (Lambda architecture) | arm64 is about 20% cheaper per GB-second; engine latency is far below what users notice, so the choice is made by measurement later, and portable SIMD keeps it open |
| 2026-11-01 | 1.5, 5.1 | Groq client built on Go's standard library (`net/http`, a line scanner, `encoding/json`), not langchaingo's `llms/openai`; [design §4](design.md#4-model-layer) updated | langchaingo v0.1.15 reads streamed reasoning only from DeepSeek's `reasoning_content` and drops Groq's `delta.reasoning`; shown by a scratch demo and by replaying a live Groq stream. It would also have added 290 modules |
| 2026-11-01 | 1.5 | No Function URL: `bench.sh` invokes the Lambda directly | The question is Groq from Lambda; streaming to the client was proven in 1.3 and 1.4, and no URL means nothing reachable from outside |
| 2026-11-01 | 1.5 | 1 warm-up and 20 runs, 10 s apart, not the methodology's 10 warm-ups and ≥ 50 requests | The step asks for 20 runs, and the gap keeps them inside Groq's free-tier limits; the row is labelled n = 20 |
| 2026-11-01 | 1.5 | The key is stored by `infra/bootstrap/aws/06-groq-key.sh` (PRs #20, #21), not a spike script; spike teardown leaves it | One owner for the secret's lifecycle; Phase 5 reuses it |
| 2026-11-01 | 1.5 | Tokens/s is measured on turn 2 only; turn 1's TTFT is the reported TTFT | Turn 1's output tokens include tool-call arguments that arrive in one chunk after the timed tokens; one run computed 939,006 tokens/s |
| 2026-11-01 | 3.4, 3.5 | langchaingo dropped from the project: Ollama uses the same standard-library client as Groq, and chunking is our own splitter | After spike 1.5, langchaingo would only have served local development and a text splitter. One client for both OpenAI-compatible providers is less code than an adapter per provider, keeps 290 modules out, and puts the chunk boundaries the evals depend on under our own tests. Ollama's reasoning field is checked in 3.5 |
| 2026-11-01 | 2.3, 2.5, 3.4 | The engine owns the index format *and* the tokenizer; the Go indexer builds the file through a new `rag_builder_*` C ABI instead of writing it itself ([design §5](design.md#5-retrieval-engine-c)) | Index-time and query-time tokenization must match exactly, or BM25 silently misses. One implementation makes that impossible to get wrong |
| 2026-11-01 | 2.3 | Tokenizer splits on Unicode dashes (U+2010–U+2015, U+2212) as well as ASCII punctuation | Spike 1.5: gpt-oss wrote U+2011 in 20 of 20 answers; without this, "multi‑agent" would index as one token |
| 2026-11-01 | 2.4 | Hybrid search details specified: top N = max(k, 50) per list after the mask, empty-text and zero-vector fallbacks, ties by lower chunk id | "Matches hand-computed ranking" needs a defined ranking to compute by hand |
| 2026-11-01 | 2.5 | Index file gains a CRC32; corruption tested by an exhaustive truncation and bit-flip loop under ASan, not coverage-guided fuzzing | A size check misses flipped bytes. libFuzzer needs clang (not installed, a few hundred MB); on a small file the loop covers every case |
| 2026-11-01 | 2.1 | doctest and nanobench vendored and pinned by SHA-256, not fetched by CMake | Offline, reproducible CI; no supply-chain download at build time |
| 2026-11-01 | 2.6 | "SIMD path" means a portable multi-accumulator loop the compiler vectorizes, not intrinsics | Keeps the portability rule; a single running sum can't be vectorized without `-ffast-math`, which is why the naive gate is expected to fail |
| 2026-11-01 | 2.7 | `engine/go` compiles the engine sources through cgo instead of linking a CMake-built library | `go test ./...` and the Lambda build stay one command; CMake remains for C++ tests, sanitizers and benchmarks |
| 2026-11-01 | 2.3 | BM25's IDF form, single counting of repeated query terms, and corpus-wide N and average length added to [design §5](design.md#5-retrieval-engine-c) | "Hand-computed scores" need one exact formula; BM25 has several common IDF variants, and Lucene's can't go negative |
| 2026-11-01 | 2.5 | Header has a term count and payload length instead of section offsets; [design §5](design.md#5-retrieval-engine-c) updated | Sections are read in order with sizes from the counts, so there are no offsets that could point outside the file |
| 2026-11-01 | 2.5 | `rag_search` returns -1 for a non-finite query, the loader rejects non-finite vectors, and the builder rejects access bits of 0 | A NaN score breaks `partial_sort`'s strict weak ordering (undefined behaviour); a chunk with no access bits can never be returned, so it's a caller bug |
| 2026-11-01 | 2.5 | Added a second corruption loop: every single-bit flip in the payload *with the checksum recomputed* must not crash loading or searching | The CRC catches accidental damage, which would leave the field checks behind it untested; this loop exercises them as a crafted file would |
| 2026-11-01 | 2.6 | The gate did not fail first: single-sum `dot()` passed on CI (hybrid p99 0.481 ms, throwaway draft #32). Proven instead by a deliberate 4× slowdown, which failed CI at p99 3.439 ms | My ~1.3 ms estimate was wrong. The planned fallback, recorded here as promised. `dot()` was still vectorised, because it measured 3× faster (CI p99 0.099 ms) |
| 2026-11-01 | 2.6 | Benchmark corpus generated in code from a fixed seed, not files in `engine/testdata/`; [benchmarks](benchmarks.md#methodology) methodology updated | Deterministic and nothing large committed, as the Phase 2 plan already said; the methodology text predated that |
| 2026-11-01 | 2.6 | Benchmark times `rag_search` through the C ABI on a file-loaded index, and the Go baseline covers vector top-k only, with the same 8-sum loop | Measures what the orchestrator will call; a like-for-like algorithm makes it a language comparison. Hybrid BM25 in Go would double the work for no decision it informs |
| 2026-11-01 | 2.7 | Go package is named `rag` (directory `engine/go`), and the C ABI gained `rag_index_dim()`; [design §5](design.md#5-retrieval-engine-c) updated | `go` is a keyword, so it can't be a package name. Go needed the index's dimension, and reading it from the file header would have given the format a second owner |
| 2026-11-01 | 2.7 | `Index` holds a read-write lock so `Close` during searches is safe; text with a NUL byte is rejected | Without the lock, a search racing `Close` reads freed C memory (the race detector reported it when the lock was removed on purpose). `C.CString` silently truncates at NUL |
| 2026-11-01 | 2.3 (revisited) | BM25 statistics (N, document frequency, average length) now cover only the chunks the caller can see, reversing the 2.3 choice of all chunks; [design §5](design.md#5-retrieval-engine-c) updated | Code review: with corpus-wide statistics, terms that appear only in private chunks changed the order of public results, a weak side channel. The cost was within benchmark noise |
| 2026-11-01 | 3.3, 3.6 | New step 3.3 builds the answer turn (retrieval, prompt, streaming, citations); tools moved from 3.3 to 3.6. 3.4 and 3.5 keep their numbers | No step built the turn itself, and citations are what the evals check. Keeping 3.4 and 3.5 keeps earlier log references valid |
| 2026-11-01 | 3.6, 7.3 | Confirmation uses DynamoDB-backed pending-action records (opaque `action_id`, one conditional update claims and de-duplicates), not a signed token; design §3, §6, §9 updated; 7.3 measures the latency | Running an action exactly once needs stored state either way, so a token would add a key to manage without removing the write. Postgres would put a Neon cold start mid-stream; DynamoDB's connection is already open for rate limiting |
| 2026-11-01 | 3.2 | Router gains a 5 s first-token timeout | Without it a hung primary never triggers the fallback |
| 2026-11-01 | 3.3 | Conversation history comes from a server-side `ConversationStore`, never the browser; the turn has limits (3 tool rounds, a deadline) | Browser-sent history could carry a forged assistant turn; an unbounded loop costs tokens and could break the stream contract on cancel |
| 2026-11-01 | 3.4 | Corpus format specified (front matter parsed by our own parser, `doc-slug#heading` ids, sidecar JSON), and the index is written atomically | The indexer needs an input contract, the golden files already use these ids, and the atomic write resolves review finding #5 |
| 2026-11-01 | 3.5 | Groq and Ollama share one package, `model/oai`; `Request.ResponseSchema` is `json.RawMessage` | One client for both OpenAI-compatible APIs, as decided on 2026-11-01; a schema type would add a dependency providers don't need |
| 2026-11-01 | 3.2 | The router falls back on *any* error before the first token except the caller's own cancellation, not only 429, 5xx and timeouts; `model.Result` gains `Fallback` and `PrimaryErr` | The visitor still gets an answer when the primary fails in an unexpected way, and `PrimaryErr` keeps the cause visible for the action card (`status: fallback`) and logs, so nothing is hidden |
| 2026-11-01 | 3.2 | After the first-token timeout, a late event from the primary is refused rather than forwarded | It would otherwise reach the visitor ahead of the fallback's answer, the stitched reply the router exists to prevent |
| 2026-11-01 | 3.3, 3.4 | 3.3 defines the `Retriever` interface and tests with a fake; the engine-backed retriever (embedder + engine + sidecar) moves to 3.4 | It needs the sidecar file that 3.4 defines; building it first would mean guessing the format |
| 2026-11-01 | 3.3 | Conversation ids are server-issued and owned by the caller who started them; unknown or foreign ids start a new conversation; [design §8](design.md#8-data-model-neon-postgres) updated | Accepting a client-chosen or another caller's id would let anyone who learned an id pull that history into their own prompt |
| 2026-11-01 | 3.3 | `Caller` and its access mask live in a new `orchestrator/auth` package; the turn deadline runs on the injectable clock | Tools in 3.6 need the caller too; the clock makes the 60 s deadline testable without waiting |
| 2026-11-01 | 3.4, 2.3 | Unicode spaces (U+00A0, U+2000–U+200A, U+202F, U+205F, U+3000) now split tokens; [design §5](design.md#5-retrieval-engine-c) updated | Deferred from 2.3 to wait for real corpus text, but none exists yet, so decided on principle: a no-break space from copy-paste would otherwise glue two words into one token that never matches. No index has been deployed, so nothing needs rebuilding |
| 2026-11-01 | 3.4 | Added rules beyond the plan: `#` lines inside code fences aren't headings and code blocks are never split; duplicate chunk ids stop the build; chunks are indexed with their title and heading; the sidecar records the embedding model and loading checks it; a `hash` embedder serves tests and offline builds | Portfolio pages contain code; a silent id collision would merge two chunks in the evals; titles are often what visitors ask about; vectors from different models aren't comparable; tests shouldn't need a model download |
| 2026-11-01 | 3.4 | The Go binding gained `Index.Count()` | The retriever checks the index and sidecar describe the same number of chunks |
| 2026-11-01 | 3.5 | `make dev` sends `reasoning_effort: low` by default, listens on `127.0.0.1:8787` (not 8080), and indexes the test fixture corpus until the real corpus exists | Measured: qwen3's default thinking took 373 s on this CPU, `low` 11 s while still streaming reasoning. Port 8080 was already taken by another process. Localhost-only binding keeps the dev server off the network |
| 2026-11-01 | 3.5 | The Ollama embedder sends texts without nomic-embed-text's recommended `search_document:` / `search_query:` prefixes | Keeps one `Embed` method for documents and queries. Whether prefixes improve hit@5 is a measurement for the Phase 6 evals; production uses Titan ([D1]), which has no prefixes |
| 2026-11-01 | 3.5 | `POST /chat` with `confirm` answers 501 until 3.6; `oai` refuses `ResponseSchema` until the judge (Phase 6); `model.Result` has no generation span, so tokens/s comes from the inference step's elapsed time minus TTFT | Unsupported inputs fail loudly instead of being ignored; one timing source for every provider |

## Code review log

Outcome of each review: finding, decision (fixed / won't fix / deferred), and why.

| Date | Step / PR | Finding | Outcome |
|---|---|---|---|
| 2026-10-30 | Phase 0 (#4, #5, #7) | No formal review run: config and a shell script only. Self-check caught `.env.example` being ignored | Fixed in #4 |
| 2026-10-31 | 1.1 bootstrap scripts | Found in use: re-running `05-dns.sh` created a second DNS zone and repointed the domain at it. The lookup filter `!Config.PrivateZone` parses as `(!Config).PrivateZone` in JMESPath, so it never matched | Fixed (``Config.PrivateZone==`false` ``); verified by re-running twice with no new zone; the stray zone is gone |
| 2026-10-31 | 1.1 bootstrap scripts | `shellcheck`: `lib.sh` had no shell directive; `tr A-Z` ranges | Fixed |
| 2026-10-31 | 1.2 spike | Self-found: my `#cgo LDFLAGS: -lstdc++` forced the shared C++ runtime and silently defeated `-static-libstdc++` (Go already links C++ packages with `g++`) | Fixed: line removed |
| 2026-10-31 | 1.2 spike | The first Lambda failure's log line was lost to teardown; Lambda itself only reports `exit status 1` | Fixed: `deploy.sh` prints recent log lines when an invoke fails |
| 2026-10-31 | 1.2 spike | Lambda's log also showed `GLIBC_2.36' not found`: the checker listed symbols for the newest version only | Fixed: it lists every symbol over the limit; self-test now requires two versions to be named |
| 2026-10-31 | 1.3 spike | `watch.sh` showed arrival times going backwards: Ubuntu 26.04's `date` (uutils 0.10.0) drops leading zeros from `%3N` (.061 s → 610 ms) | Fixed: bash's `$EPOCHREALTIME` instead. Upstream bug, not yet reported |
| 2026-10-31 | 1.3 spike | A user run failed while Lambda's logs showed a normal stream: the per-gap pass rule was flaky by design | Fixed: spread-based rule, validated against batching, streaming and jittery local servers; every run's output now saved |
| 2026-10-31 | 1.4 spike | `shellcheck` flagged `A && ok || bad` shortcuts and an unchecked `cd` in `check.sh` | Fixed: explicit `if` blocks, `cd … \|\| exit` |
| 2026-11-01 | 1.5 spike | Self-found: my first key script and PR #20's `06-groq-key.sh` both piped JSON to `--cli-input-json file:///dev/stdin`, but AWS CLI 2.37.5 reads nothing from pipes or `/dev/fd`. My dry run passed because its fake `aws` read stdin | Fixed on `main` in #21 (temp file in RAM); the spike's own script was dropped. A fake proves nothing about the behaviour it imitates |
| 2026-11-01 | 1.5 spike | Live run: the byte-exact codename check failed a working round trip because gpt-oss wrote U+2011 | Fixed, test first: dashes normalised before comparing. 20 of 20 bench answers needed it |
| 2026-11-01 | 1.5 spike | Self-found: `stats` printed tokens/s p95, which for throughput is the fast end | Fixed: p5 and p1 |
| 2026-11-01 | 1.5 spike | Self-found: one break-it check deleted a line so the code no longer compiled, which proves nothing about the test | Redone as a break that compiles; the test caught it |
| 2026-11-01 | 1.5 (`/code-review medium`) | Groq's two usage copies overwrote each other; a repeated tool name was doubled; tool-call chunks weren't timed, inflating turn 1's tokens/s | Fixed, each test first and broken on purpose; recorded numbers unaffected (TTFT doesn't depend on them, turn 2 has no tool calls, the usage copies agree) |
| 2026-11-01 | 1.5 (`/code-review medium`) | Turn 1 didn't check `finish_reason`, so a call cut off by `length` showed up as bad tool arguments | Fixed, test first |
| 2026-11-01 | 1.5 (`/code-review medium`) | `bench.sh` exited on one failed `aws` invoke without summarising, and a failed warm-up gave no hint; `cmd/local` had no time limit and ignored `GROQ_KEY_PARAM`; headers still said tokens/s p95 | Fixed; bench checked against a fake `aws` that fails one invoke |
| 2026-11-01 | 1.5 (`/code-review medium`) | Tokens/s divides N tokens by the N − 1 gaps between first and last arrival, which inflates it | Won't fix: that is the [definition](benchmarks.md#metric-definitions); under 1% for ~330 tokens |
| 2026-11-01 | 2.2 | Two break-it checks first failed to compile (`-Werror` on an unused parameter), proving nothing | Redone as compiling breaks; both caught. `k = 0` and empty-index cases can't be broken without identical behaviour, so they stay as crash guards under ASan |
| 2026-11-01 | 2.3 | GCC's `-Werror=bidi-chars` rejected a test containing a literal U+200F (an invisible text-direction mark, the "Trojan Source" attack) | Fixed: written as hex escapes. The warning stays on |
| 2026-11-01 | 2.3 | Self-found: a zero-total-length guard in `bm25_topk` was dead code (no tokens means no postings, so nothing divides); deleting it failed no test | Removed, with a comment saying why the division is safe |
| 2026-11-01 | 2.3 | Self-found: Unicode spaces (U+00A0 no-break space, U+2000–U+200A, U+3000) don't split tokens, as specified, so `Go\u00A0engine` is one token | Deferred: revisit with the real corpus in 3.4; adding them is one line plus tests |
| 2026-11-01 | 2.4 | Self-found: the "skip BM25 when the query has no tokens" branch was redundant (an empty query already yields an empty list, which RRF ignores); breaking it failed no test | Removed. Also added a test the plan didn't list, pinning the max(k, 50) candidate depth with a 60-chunk fixture |
| 2026-11-01 | 2.5 | My first `rag_search` design took a NaN query straight to the sort; found while writing the spec for the loader | Fixed before code: rejected at the ABI, test first |
| 2026-11-01 | 2.5 | Two test bugs crashed the run against the stubs (`strlen` on an unterminated buffer, indexing an empty file) and hid the other failures | Fixed: `memchr` before `strlen`, `REQUIRE` a real file before patching bytes |
| 2026-11-01 | 2.5 | Breaking the trailing-bytes check failed no test: appending a byte trips the payload-length check first | Fixed: a test that also updates length and checksum; it catches the break |
| 2026-11-01 | 2.5 | "No exception escapes the ABI" can't be provoked from a test without fault injection | Checked by reading: every ABI function is `noexcept` with `catch (...)`, so even a missed catch terminates instead of unwinding into Go |
| 2026-11-01 | 2.6 | Self-found: a C++ result row nearly recorded the 16-sum build as the 8-sum one; the Release binary hadn't been rebuilt after the variant comparison (0.44 ms instead of 0.12 ms) | Caught by comparing with the earlier run; every recorded number comes from `make bench-engine`, which rebuilds first |
| 2026-11-01 | 2.6 | Go baseline: breaking the trim so k + 1 hits came back failed no test, because every fixture ended with a trimming insertion | Fixed: an ascending-scores case; it catches the break |
| 2026-11-01 | 2.6 | Process: `gh pr close --delete-branch` on the throwaway also deleted the local branch | Recovered the commit from the reflog; nothing lost |
| 2026-11-01 | 2.7 | Self-found: my first `Load` read `dim` from the file header in Go, duplicating the format | Fixed: `rag_index_dim()` in the C ABI, test first |
| 2026-11-01 | 2.7 | Self-found: a Go-side NaN check duplicated the engine's rule | Removed; the engine owns it, and Go maps its -1 to a clear message since the length is already checked |
| 2026-11-01 | 2.7 | Removing the Go length check failed no test: the engine rejects a wrong length too, and the test only asked for some error | Fixed: the test now checks each error names its cause; it catches the break. Six breaks in total, all caught, including a `DATA RACE` report and a glibc double-free abort |
| 2026-11-01 | 2.7 | Verified beyond the plan: the binding built fully static (`netgo,osusergo`, `-extldflags -static`) passes its tests and spike 1.2's `check-glibc.sh` | Confirms the engine (iostreams, `<filesystem>`) keeps the Lambda build static |
| 2026-11-01 | Phase 2 (`/code-review medium engine/`) | `Search` sized its output buffer from the caller's `k` (capped only at 2³¹), so a huge `k` allocated gigabytes | Fixed, test first: new `rag_index_count()` in the C ABI, and Go caps `k` at it. Before: 80 MB allocated for `k` = 10⁷ on 3 chunks |
| 2026-11-01 | Phase 2 (`/code-review medium engine/`) | `Load` and `Write` passed paths with a NUL byte to `C.CString`, which cut them short: `Write` created a different file | Fixed, test first: rejected with "contains a NUL byte". The first version of the test passed by accident because the temp directory's name contains "NUL"; it now matches the exact wording |
| 2026-11-01 | Phase 2 (`/code-review medium engine/`) | Query text with a NUL byte was searched only up to the NUL | Fixed, test first: NUL is replaced by a space, which is how the tokenizer splits it, so the search matches the full text and odd visitor input isn't an error |
| 2026-11-01 | Phase 2 (`/code-review medium engine/`) | A -1 from `rag_search` was always reported as a non-finite vector, but the engine's catch-all also returns -1 | Fixed: the message names both causes. The internal-error path can't be provoked from a test |
| 2026-11-01 | Phase 2 (`/code-review medium engine/`) | `rag_builder_write` truncates and writes in place, so a failure part-way destroys the previous index | Deferred to 3.4: the indexer writes into a deploy build directory, not a live path, and write-then-rename belongs in the Go indexer that 3.4 creates |
| 2026-11-01 | Phase 2 (`/code-review medium engine/`) | `bm25_topk` recomputes the average chunk length on every query | Won't fix: about 2,000 integer adds, estimated at no more than 1 µs of an 86 µs search (not measured); a cached copy would be state to keep in step |
| 2026-11-01 | Phase 2 (`/code-review medium engine/`) | The builder briefly holds about 4× the vector data while writing | Won't fix: build time only, on the deploy machine, with a corpus expected under 2k chunks |
| 2026-11-01 | Phase 2 (`/code-review medium engine/`) | The loader read the file one byte at a time through `istreambuf_iterator` | Fixed: one sized `fread`. `make bench-engine` load time for 2k × 512 went from 16.5 ms to 11.3 ms median (−32%). Bulk-copying the fixed sections as well measured no gain (12.1 vs 12.3 ms in a scratch timer), so it was reverted |
| 2026-11-01 | Phase 2 (`/code-review medium engine/`) | Error messages read `errno` after iostream failures, which isn't guaranteed to be set, so a message could say "Success" | Not reproduced: writing to `/dev/full` reported "No space left on device". Switched to C stdio with the loader fix anyway, which does set `errno`, and added the `/dev/full` test as a regression guard (it passed before the change too) |
| 2026-11-01 | Phase 2 (`/code-review medium engine/`) | IDF and average length counted private chunks, so a public caller's result order depended on private text | Fixed, test first, with your agreement: statistics over visible chunks only. A new test shows identical public scores with and without private chunks; it failed before the change. Three breaks (df, average, scoring hidden chunks) all caught |
| 2026-11-01 | #37 | CI hybrid p99 read 0.222 ms against 0.099 ms at 2.6, which looked like a regression from the BM25 fix | Not a regression: vector-only (which #37 doesn't touch) had doubled since 2.7, and re-running the 2.6 commit gave 0.108 ms vs 0.062 ms. CI runners vary about 2× for the same code. `rag_bench` now prints the CPU model; benchmarks.md says to use local numbers for before/after |
| 2026-11-01 | 3.1 | Breaking step durations to count from the stream's start failed no test: the test opened its step at time zero, where both starts coincide | Fixed: the test opens the step 5 ms in; it catches the break |
| 2026-11-01 | 3.1 | The write-failure test's fake failed forever, so it couldn't tell a sticky error from a still-broken pipe | Fixed: the fake fails exactly once; breaking stickiness now fails the test |
| 2026-11-01 | 3.1 | Self-found: `Done` set `total_ms` and `finish` overwrote it after closing open steps | Removed the first; the value now always includes closing the steps |
| 2026-11-01 | 3.2 | Removing the first-token timeout was caught only by Go's 10-minute test deadline: the test waited forever on a router that never returned | Fixed: the wait has a 2 s limit and fails with a clear message. Six other breaks were caught normally; four router tests passed against the stub (they check that it does *not* fall back) and were proven by breaks that make it fall back |
| 2026-11-01 | 3.3 | Breaking the citation parser's length bound failed no test, because long bodies fail the number check anyway; the bound exists to cap memory | Fixed: a test feeds an unclosed `[` and 12,000 bytes and checks at most 64 are held. Raised the bound from 16 to 64 so a genuine `[1, 2, …, 9]` list still counts |
| 2026-11-01 | 3.3 | Two test bugs against the stub: an index into an empty request list panicked and hid other failures, and two wait loops had no limit (the 3.2 hang trap again) | Fixed: `Fatalf` guards and a 2 s `waitFor`. Eleven breaks to the turn, all caught |
| 2026-11-01 | 3.4 | **A break-it check crashed WSL.** Removing the chunker's progress guard made it loop forever appending chunks; the test ran with no time or memory limit, memory ran out and WSL restarted, ending the session and wiping the scratchpad (with the backup of the broken file) | Recovered: found the break still in the file by checking every line the breaks touch, restored it. Since then every break-it run is wrapped in `timeout` and, for Go, `ulimit -v 4000000` (the same break now dies alone at 2.5 GB). ASan reserves terabytes of virtual memory, so the sanitized C++ binary runs under `timeout` only |
| 2026-11-01 | 3.4 | Breaks not caught at first: case-insensitive hashing (similar sentences stayed close anyway), the embedding-model check (the test's other model also had another dimension), title indexing (the query also matched the heading, then a hash-vector hit landed right by chance), temp-file cleanup (the failing embedder failed before any temp file existed), and the 2-byte space end check (a string literal's NUL hid the over-read) | Each fixed with a targeted test: exact case test, same-dimension renamed model, a direct `indexText` test, a build failing at the rename, an exactly-sized heap buffer (now caught by libstdc++'s bounds assertion). One harness slip too: a break that didn't compile looked like a pass because the filter missed "build failed" |
| 2026-11-01 | 3.5 | Two handler breaks (no flush, disconnect not cancelling) were caught only by the 30 s test deadline: without a flush even `http.Client.Do` never returns, and a turn that never ends blocks the test server's shutdown | Fixed: the request and the first read each get a 2 s limit, and the fake turn has a release channel the test closes on failure; both breaks now fail in 2 s with a clear message |
| 2026-11-01 | 3.5 | Live run: the thought stream listed "Project Alpha" three times | Fixed, test first: one entry per document with a passage count; the test also caught "1 passages" |
| 2026-11-01 | 3.5 | Process: my cleanup `pkill -f 'orchestrator/cmd/dev'` matched and killed its own shell (the text was in its command line) | No harm beyond a failed command; the dev server is now tracked by its PID |

## Spike findings

| Spike | Result | Evidence | Design impact |
|---|---|---|---|
| 1.2 cgo on Lambda | ✅ **Fully static works; the planned `-static-libstdc++` fix does not.** Naive build links GCC 15's `libstdc++.so.6`; static C++ runtime still needs glibc 2.35–2.38 (`_dl_find_object`, `arc4random`, `__isoc23_strtoul` inside Ubuntu's `libgcc`/`libstdc++.a`); fully static passes. Zig not needed. x86_64 only (arm64 not attempted) | Lambda invoke (2026-10-31): static-libs build → `Runtime.ExitError`, log ``version `GLIBC_2.38' not found``, as the checker predicted; full-static → `{"sum":10,"os":"Amazon Linux 2023…"}`. [Spike README](../spikes/cgo-lambda/README.md) | Lambda builds are fully static with `netgo,osusergo`; `check-glibc.sh` gates every release build ([design §5](design.md#5-retrieval-engine-c)) |
| 1.3 Go SSE streaming | ✅ **Events arrive one by one.** `aws-lambda-go` v1.55.1 `LambdaFunctionURLStreamingResponse` with an `io.Pipe` body; Function URL in `RESPONSE_STREAM` mode with `AWS_IAM` auth. First event ≈ 110 ms after the request (warm); arrivals spread over 1.80 s for 1.80 s of sends | 10 of 10 `watch.sh` runs passed (user and Claude, 2026-10-31); BUFFERED mode failed as expected. [Spike README](../spikes/sse-lambda/README.md) | None to the protocol ([design §3](design.md#3-streaming-protocol-sse)). Wait ~30 s after changing a URL's invoke mode before testing |
| 1.4 CloudFront passthrough | ✅ **Streams through CloudFront; Function URL closed to everyone else; POST needs the body hash.** OAC (`lambda`, SigV4 always) + Lambda permissions scoped to the distribution ARN; caching and compression off; `AllViewerExceptHostHeader`. A POST without `x-amz-content-sha256` is refused | `check.sh` 4 of 4 (2026-10-31): unsigned direct → 403; GET first event 463 ms (cold), spread 1.78 s; POST with hash 180 ms, spread 1.80 s. [Spike README](../spikes/cloudfront-sse/README.md) | The browser hashes the `POST /chat` body (SHA-256, Web Crypto) into `x-amz-content-sha256` ([design §3](design.md#3-streaming-protocol-sse)) |
| 1.5 Groq from Lambda | ✅ **Reasoning and text arrive separately; one tool call round-trips; TTFT p50 198 ms, 483 tokens/s, from Lambda. Not through langchaingo**, whose provider drops Groq's `delta.reasoning`. Groq sends reasoning as `delta.reasoning` with `"channel":"analysis"`, the tool call whole in one chunk, and usage in both `x_groq` and a final usage chunk. gpt-oss wrote the codename with a Unicode hyphen (U+2011) in 20 of 20 answers. Groq's prompt cache hit unprompted on 7 of 20 second turns. Cold start with AWS SDK config and the SSM key read: 205 ms (n = 1) | `bench.sh` 20 of 20 PASS (2026-11-01, `bfd5c26`); scratch langchaingo demo (reasoning `""`); `TestStreamParsesLiveRecording` on a recorded stream; Lambda logs scanned: no key. [Spike README](../spikes/groq-lambda/README.md) | `model/groq` is a standard-library client ([design §4](design.md#4-model-layer)). Exact matches on model output normalise dashes (6.4 canaries, citations). Config and secrets take ~140 ms of the 400 ms cold-start target, so keep-warm (7.5) matters |
