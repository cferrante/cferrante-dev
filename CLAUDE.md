# cferrante-dev: working agreement for Claude Code

This project is built with [Claude Code](https://claude.com/claude-code) using spec-driven,
test-driven development. This file tells each session how the project is run, and doubles as a
record of the process for anyone reading the repo.

## Where things live

- `docs/design.md` is the spec; `docs/plan.md` the step-by-step plan. Each plan step names the
  test that must fail first. Read the relevant section of both before proposing work.
- `docs/plan.md` also holds the **deviations log**, **code-review log** and **spike findings**.
  Every change from the plan and every review outcome gets a row there, in the same PR.
- `docs/benchmarks.md` holds targets and measured results, with the method used.
- `docs/concepts.md` explains terms; link to it rather than re-explaining.
- `infra/bootstrap/aws/` holds idempotent account setup scripts; `check.sh` verifies them read-only.

## How we work

- Agree a short design and test list before code; record any deviation from it in the plan.
- One plan step (or one fix) per branch and PR. Merge only with CI green.
- Test-first: show the test failing for the right reason, make it pass, then break the code on
  purpose to prove the test catches it.
- Answer first, then the evidence (a table if comparing), then a recommendation, then one clear
  question or next step. Recommend; don't list options that won't be pursued.
- Say what is verified and what is assumed. Measure before claiming a number.
- When something fails, reproduce it, say plainly why the earlier tests missed it, then fix it
  test-first.
- Spikes answer one question: a README opening with **Question** and **Answer**, a test script
  that prints ✓/✗ per expectation, and `deploy.sh` / `teardown.sh`. Tear down when done.
- End a piece of work with numbered steps for me to run, each with the expected output.

## Decisions already made (don't re-propose without new evidence)

- Go for anything a visitor waits on; C++ engine via cgo, fully static build. No Python for now.
- No LLM framework (no langchaingo, no LangChain): standard-library clients behind our own
  `Model` interface. See the plan's deviations log, 2026-11-01.
- Groq gpt-oss-120b primary, Bedrock fallback, Ollama locally.

## AWS and secrets

- I run anything that creates or changes AWS resources. Claude writes idempotent scripts and
  gives numbered steps; read-only checks by Claude are fine. Profile: `cferrante-dev-admin`.
- Secrets live only in SSM SecureString (or GitHub secrets). Never echo them, put them on a
  command line, or write them to disk; tmpfs is acceptable for a file that is deleted at once.

## Public repo: privacy check before every push, PR or comment

Scan the diff for emails, local or home paths, AWS account IDs, zone IDs, Function or CloudFront
URLs, API keys and tokens. Never commit the budget alert email or any domain other than
`cferrante.dev`. Commits use the GitHub noreply address. No force-push: fix with new commits.

## Environment notes

Development is on Ubuntu 26.04 under WSL2, with no Docker.

- `date` is uutils: `%3N` drops leading zeros. Use bash `$EPOCHREALTIME` or Go's clock.
- AWS CLI 2.37.5 can't read `file://` from a pipe (`/dev/stdin`, `<(...)`): use a real file.
- `gh` is old: `gh pr edit` fails (use `gh api -X PATCH`); `gh pr checks` has no `--json`.
- `make lint` runs gofmt, go vet and shellcheck on every `*.sh`; `make test` runs Go and shell tests.

## Parallel sessions

A second session works in its own git worktree on its own branch. Agree which files each
session owns (often `docs/plan.md` and `docs/benchmarks.md`) and stay out of the other's.
