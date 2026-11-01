#!/usr/bin/env bash
# The spike's test and benchmark. Invokes the Lambda once to warm it up
# (reporting its cold start), then RUNS more times, each one tool call round
# trip against Groq. Prints every run, TTFT p50/p95/p99, tokens/s p50/p5/p1
# (for throughput the slow tail is the low end), and
# PASS only if every run streamed reasoning and text separately and
# round-tripped the tool call.
#
#   ./bench.sh [-n RUNS] [-g GAP_SECONDS]     (defaults: 20 runs, 10 s apart)
#
# The gap keeps the runs inside Groq's free-tier rate limits and spreads
# them over a few minutes. Everything is saved to build/bench/<time>.*
set -euo pipefail
cd "$(dirname "$0")"

runs=20 gap=10
while (( $# > 0 )); do
  case $1 in
    -n) runs=${2:?-n needs a number}; shift 2 ;;
    -g) gap=${2:?-g needs seconds}; shift 2 ;;
    *) echo "usage: $0 [-n RUNS] [-g GAP_SECONDS]" >&2; exit 2 ;;
  esac
done
export AWS_PROFILE=${AWS_PROFILE:-cferrante-dev-admin}
export AWS_REGION=${AWS_REGION:-us-east-1}
fn=spike-groq-lambda

aws sts get-caller-identity >/dev/null 2>&1 \
  || { echo "no valid session; run: aws sso login --profile $AWS_PROFILE" >&2; exit 1; }

mkdir -p build/bench
base=build/bench/$(date +%Y%m%dT%H%M%S)
resp=$(mktemp)
trap 'rm -f "$resp"' EXIT

# invoke [--log-type Tail]: one invoke; the report goes to $resp; prints
# the FunctionError ("None" if there was none) and, with Tail, the
# base64 log tail, tab-separated.
invoke() {
  aws lambda invoke --function-name "$fn" --cli-binary-format raw-in-base64-out \
    --payload '{}' "$@" --query '[FunctionError, LogResult]' --output text "$resp"
}

{
  echo "date:    $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "git sha: $(git rev-parse --short HEAD)$(git diff --quiet HEAD -- . || echo ' (uncommitted changes)')"
  aws lambda get-function-configuration --function-name "$fn" \
    --query '[MemorySize, Architectures[0], Environment.Variables.GROQ_MODEL]' --output text \
    | awk '{print "lambda:  " $1 " MB, " $2 ", model " $3 ", region '"$AWS_REGION"'"}'
} | tee "$base.txt"

echo "== warm-up (discarded)" | tee -a "$base.txt"
if ! IFS=$'\t' read -r ferr logs < <(invoke --log-type Tail); then
  echo "FAIL: the warm-up invoke didn't run (see the aws error above). Has ./deploy.sh run? Is the SSO session still valid?" >&2
  exit 1
fi
report=$(base64 -d <<<"$logs" | grep '^REPORT' || true)
if [[ $report =~ Init\ Duration:\ ([0-9.]+\ ms) ]]; then
  echo "cold start: ${BASH_REMATCH[1]} Init Duration (Go runtime + AWS config + SSM key read)" | tee -a "$base.txt"
else
  echo "warm already (no Init Duration in this invoke)" | tee -a "$base.txt"
fi
if [[ $ferr != None ]]; then
  echo "FAIL: warm-up invoke failed ($ferr); log tail:" >&2
  base64 -d <<<"$logs" >&2
  exit 1
fi
cp "$resp" "$base-warmup.json"
grep -o '"ok":[a-z]*' "$resp" | tee -a "$base.txt"

echo "== $runs runs, ${gap}s apart" | tee -a "$base.txt"
for (( i = 1; i <= runs; i++ )); do
  sleep "$gap"
  # A CLI-level failure (throttling, an expired session) is a failed run,
  # not the end of the bench: earlier runs still get summarised.
  ferr=$(invoke | cut -f1) || ferr="aws CLI error, see above"
  if [[ $ferr == None ]]; then
    cat "$resp" >>"$base.jsonl"
  else
    printf '{"ok":false,"problems":["invoke failed: %s"]}' "$ferr" >>"$base.jsonl"
  fi
  echo >>"$base.jsonl"
  echo "run $i: $(tail -n1 "$base.jsonl" | grep -o '"ok":[a-z]*')"
done

echo | tee -a "$base.txt"
status=0
go run ./cmd/stats -want "$runs" "$base.jsonl" | tee -a "$base.txt" || status=1
echo "saved $base.txt and $base.jsonl"
exit "$status"
