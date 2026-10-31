# Spike 1.3: Go response streaming → SSE

**Question:** does a Go Lambda behind a Function URL in `RESPONSE_STREAM` mode deliver
Server-Sent Events to the client one by one, or all at the end?

**Answer: one by one.** Ten events sent 200 ms apart arrive 200 ms apart; the first arrives
about 110 ms after the request (warm function, from WSL2 over home internet). Findings are
recorded in the [plan](../../docs/plan.md#spike-findings).

## What's here

| Path | Purpose |
|---|---|
| `sse/` | `Stream` writes N `tick` events, one `Write` per event, at a fixed interval; tests use a fake writer that timestamps each write |
| `cmd/bootstrap/` | Lambda handler using `aws-lambda-go`'s streaming response: returns at once with a pipe that `Stream` fills in the background |
| `watch.sh` | The test: a SigV4-signed `curl -N` that times each event's arrival and decides streamed vs buffered |
| `deploy.sh`, `teardown.sh` | Idempotent deploy to the project AWS account (`stream` or `buffered` URL mode), and clean removal |

The Function URL uses `AWS_IAM` auth, so it is never publicly callable: `watch.sh` signs requests
with your SSO session's temporary credentials. A browser gets 403.

## How `watch.sh` decides

Streamed if the time from the first arrival to the last is at least 3/4 of the time from the
first send to the last (each event carries its send time). Buffering collapses arrivals to ~0 s.
A network hiccup that bunches two events together is reported, not failed: an earlier rule that
counted individual gaps failed a working stream after two hiccups. The rule was checked against
local servers that batch (fails), stream (passes) and stream with deliberate hiccups (passes).

## Run it

```bash
aws sso login --profile cferrante-dev-admin
./deploy.sh buffered && sleep 30 && ./watch.sh   # fails: buffered
./deploy.sh && sleep 30 && ./watch.sh -n 10      # passes: streamed
./teardown.sh
```

Wait ~30 s after switching modes: the change takes a few seconds to apply everywhere. Every run
is saved to `build/watch/`. Cost: $0 within the Lambda free tier.
