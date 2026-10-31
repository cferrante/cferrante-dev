#!/usr/bin/env bash
# The spike's test. Calls the Function URL with a signed request and records
# when each SSE event arrives.
#
# Verdict: streamed if the time from the first arrival to the last is at
# least 3/4 of the time from the first send to the last (about 1.8 s).
# Buffering collapses arrivals to ~0 s. Single bunched gaps from network
# hiccups are reported but don't decide the verdict.
#
#   ./watch.sh [-n RUNS] [URL]   (default: 1 run, the spike function's URL)
#
# Every run is saved to build/watch/<time>.log.
set -euo pipefail
cd "$(dirname "$0")"

runs=1
if [[ ${1:-} == -n ]]; then runs=${2:?-n needs a number}; shift 2; fi
export AWS_PROFILE=${AWS_PROFILE:-cferrante-dev-admin}
export AWS_REGION=${AWS_REGION:-us-east-1}
url=${1:-$(aws lambda get-function-url-config --function-name spike-sse-lambda --query FunctionUrl --output text)}

# Temporary credentials from the SSO session, for curl's SigV4 signing.
eval "$(aws configure export-credentials --format env)"

# Milliseconds since the epoch from bash's own clock. Not `date +%s%3N`:
# Ubuntu 26.04's date (uutils 0.10.0) drops leading zeros from %3N, turning
# .061 s into 610 ms. It's also one fewer process per event.
now_ms() { local t=${EPOCHREALTIME//[.,]/}; echo $((10#$t / 1000)); }

# watch_once: prints the arrival table and a PASS/FAIL line; fails on FAIL.
watch_once() {
  local start now prev=0 n=0 bunched=0 first_at=0 last_at=0 first_sent=0 last_sent=0 sent gap
  start=$(now_ms)
  echo "arrival        gap     event"
  # The credentials go to curl through a config on a file descriptor, so they
  # never appear in the process list.
  while IFS= read -r line; do
    [[ $line == data:* ]] || continue
    now=$(now_ms)
    sent=$(sed -n 's/.*"sent_ms":\([0-9]*\).*/\1/p' <<<"$line")
    n=$((n + 1))
    if (( n == 1 )); then
      first_at=$now first_sent=$sent
      printf '%6d ms     —     %s\n' $((now - start)) "$line"
    else
      gap=$((now - prev))
      (( gap < 100 )) && bunched=$((bunched + 1))
      printf '%6d ms  %4d ms  %s\n' $((now - start)) "$gap" "$line"
    fi
    prev=$now last_at=$now last_sent=$sent
  done < <(curl -sSN --aws-sigv4 "aws:amz:$AWS_REGION:lambda" \
    -K <(printf 'user = "%s:%s"\n' "$AWS_ACCESS_KEY_ID" "$AWS_SECRET_ACCESS_KEY") \
    -H "x-amz-security-token: $AWS_SESSION_TOKEN" "$url")

  local arrived=$((last_at - first_at)) sent_span=$((last_sent - first_sent))
  echo
  echo "first event after $((first_at - start)) ms; arrivals spread over $arrived ms, sends over $sent_span ms; $bunched bunched gap(s)"
  if (( n == 10 && sent_span > 0 && arrived * 4 >= sent_span * 3 )); then
    echo "PASS: streamed one by one"
  else
    echo "FAIL: $n events, arrivals not spread like the sends: buffered, not streamed"
    return 1
  fi
}

mkdir -p build/watch
passed=0
for (( r = 1; r <= runs; r++ )); do
  log=build/watch/$(date +%Y%m%dT%H%M%S)-$r.log
  if watch_once | tee "$log"; then passed=$((passed + 1)); fi
  (( runs > 1 )) && echo "---- run $r saved to $log" && echo
done
(( runs > 1 )) && echo "$passed of $runs runs passed"
(( passed == runs ))
