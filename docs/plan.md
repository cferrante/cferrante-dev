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
| 🧑 Groq account + API key (free) | 1.5 | not started |
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
| **1.5** 🧑 **Groq from Lambda.** langchaingo OpenAI provider → Groq gpt-oss-120b, streaming reasoning + text + one tool call, timed | Test asserting reasoning *and* text chunks arrive separately fails | TTFT and tokens/s for 20 runs recorded in benchmarks; tool call round-trips |

**Exit criteria (M1):** all five pass, *or* a failed spike triggers a recorded design change
before Phase 2. Cost: $0 (free tiers; spike resources torn down afterwards).

## Phase 2 — Retrieval engine, C++ ($0)

Built with CMake, tested with doctest, benchmarked with nanobench. Everything here runs locally
and in CI.

**Portability rule:** no architecture-specific code (such as AVX2 intrinsics) without a portable
fallback that is tested and benchmarked alongside it. The hot loops are written so the compiler
can vectorize them on both x86_64 and arm64, keeping the Lambda architecture ([D5]) a build flag
rather than a rewrite.

| Step | Test that must fail first | Done when |
|---|---|---|
| **2.1** CMake project + doctest; `normalize()` and `dot()` | Unit tests for known vectors (incl. zero vector) fail | Pass, with `-fsanitize=address,undefined` in the test build |
| **2.2** Brute-force top-k with access mask | Test: a private chunk with the highest score is excluded for mask `0x1` | Correct order, ties stable, k > count handled |
| **2.3** BM25 over an inverted index | Hand-computed scores for a 3-doc fixture don't match | Matches to 1e-5 |
| **2.4** Reciprocal Rank Fusion | Fixture where fusion must reorder results | Matches hand-computed ranking |
| **2.5** Versioned index file (write + load) and the C ABI from [design §5](design.md#5-retrieval-engine-c) | Round-trip test; loading a wrong-version or truncated file must return an error, not crash | Round-trip exact; fuzzed corrupt files rejected safely |
| **2.6** Benchmarks (nanobench) + a **pure-Go baseline** | CI gate "p99 < 1 ms" fails until the SIMD path exists | Gate green; C++ vs Go numbers recorded honestly |
| **2.7** Go binding package (`engine/go`) | Go test calling `rag_search` via cgo fails (no binding) | Passes, including under `go test -race` with concurrent searches |

## Phase 3 — Orchestrator core, Go, fake model ($0)

| Step | Test that must fail first | Done when |
|---|---|---|
| **3.1** SSE writer enforcing the [contract rules](design.md#3-streaming-protocol-sse) | Tests: unmatched `step_start`, event after `done`, missing keep-alive | All four contract rules enforced |
| **3.2** `Model` interface, fake model, router with fallback-before-first-token | Test: primary returns 429 → fallback used; error *after* first token → error reported, no fallback | Both behaviours proven |
| **3.3** Agent loop + tool executor with two-layer authorization and confirmation | Test: anonymous caller's forged `send_message` call is refused; unconfirmed side effect doesn't run | Refused with `authorized:false`; idempotency key de-duplicates |
| **3.4** Indexer CLI: chunking (langchaingo text splitter), embedding via interface, writes index | Test on a fixture corpus: expected chunk ids and access bits | Index loads in the engine and finds known chunks |
| **3.5** `make dev`: local HTTP server, SSE over Ollama (small 3–4B model; **disk check first**) | Smoke test: `curl -N localhost` gets `thought`, `token`, `done` in order | Works end to end on the laptop |

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
| **7.3** Lambda + Function URL + origin access control, SSM secrets, DynamoDB rate limits, concurrency cap | Smoke test against deployed `/chat` fails | Streams through CloudFront; 11th anonymous message in 10 min is rate-limited |
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
| 2026-10-31 | 2, 7.6 | Added the Phase 2 portability rule, step 7.6 and decision D5 (Lambda architecture) | arm64 is about 20% cheaper per GB-second; engine latency is far below what users notice, so the choice is made by measurement later, and portable SIMD keeps it open |

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

## Spike findings

| Spike | Result | Evidence | Design impact |
|---|---|---|---|
| 1.2 cgo on Lambda | ✅ **Fully static works; the planned `-static-libstdc++` fix does not.** Naive build links GCC 15's `libstdc++.so.6`; static C++ runtime still needs glibc 2.35–2.38 (`_dl_find_object`, `arc4random`, `__isoc23_strtoul` inside Ubuntu's `libgcc`/`libstdc++.a`); fully static passes. Zig not needed. x86_64 only (arm64 not attempted) | Lambda invoke (2026-10-31): static-libs build → `Runtime.ExitError`, log ``version `GLIBC_2.38' not found``, as the checker predicted; full-static → `{"sum":10,"os":"Amazon Linux 2023…"}`. [Spike README](../spikes/cgo-lambda/README.md) | Lambda builds are fully static with `netgo,osusergo`; `check-glibc.sh` gates every release build ([design §5](design.md#5-retrieval-engine-c)) |
| 1.3 Go SSE streaming | ✅ **Events arrive one by one.** `aws-lambda-go` v1.55.1 `LambdaFunctionURLStreamingResponse` with an `io.Pipe` body; Function URL in `RESPONSE_STREAM` mode with `AWS_IAM` auth. First event ≈ 110 ms after the request (warm); arrivals spread over 1.80 s for 1.80 s of sends | 10 of 10 `watch.sh` runs passed (user and Claude, 2026-10-31); BUFFERED mode failed as expected. [Spike README](../spikes/sse-lambda/README.md) | None to the protocol ([design §3](design.md#3-streaming-protocol-sse)). Wait ~30 s after changing a URL's invoke mode before testing |
| 1.4 CloudFront passthrough | ✅ **Streams through CloudFront; Function URL closed to everyone else; POST needs the body hash.** OAC (`lambda`, SigV4 always) + Lambda permissions scoped to the distribution ARN; caching and compression off; `AllViewerExceptHostHeader`. A POST without `x-amz-content-sha256` is refused | `check.sh` 4 of 4 (2026-10-31): unsigned direct → 403; GET first event 463 ms (cold), spread 1.78 s; POST with hash 180 ms, spread 1.80 s. [Spike README](../spikes/cloudfront-sse/README.md) | The browser hashes the `POST /chat` body (SHA-256, Web Crypto) into `x-amz-content-sha256` ([design §3](design.md#3-streaming-protocol-sse)) |
| 1.5 Groq from Lambda | pending | — | — |
