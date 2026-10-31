# Spike 1.4: CloudFront passthrough

**Question:** with CloudFront and origin access control (OAC) in front of the spike 1.3
Function URL, do SSE events still arrive one by one, and is the Function URL closed to
everyone else?

**Answer: yes to both, and POST needs the body's hash.** Findings are recorded in the
[plan](../../docs/plan.md#spike-findings); the POST rule is in
[design §3](../../docs/design.md#3-streaming-protocol-sse).

## What's here

| Path | Purpose |
|---|---|
| `check.sh` | The test: four expectations, ✓ or ✗ each (below) |
| `deploy.sh` | OAC, distribution and Lambda permissions, idempotent; waits for the distribution to deploy |
| `teardown.sh` | Removes them in the order CloudFront requires (permissions, disable, wait, delete, OAC) |

It reuses the function from [`../sse-lambda`](../sse-lambda/README.md) and its `watch.sh`.

## Configuration that matters

- **OAC** of type `lambda`, signing every request with SigV4; the Function URL keeps `AWS_IAM`
  auth, so only signed callers get in.
- **Lambda permissions** for `cloudfront.amazonaws.com`, `lambda:InvokeFunctionUrl` and
  `lambda:InvokeFunction`, each limited to this distribution's ARN.
- **Caching disabled** and **compression off** (compressing a stream can buffer it).
- **Origin request policy `AllViewerExceptHostHeader`**: a Function URL rejects any `Host`
  header but its own.
- **All methods allowed**, so `POST /chat` can pass.

## Results (2026-10-31)

| # | Expectation | Result |
|---|---|---|
| 1 | Unsigned request straight to the Function URL is refused | ✓ 403 |
| 2 | GET through CloudFront streams one by one | ✓ first event 463 ms (cold), arrivals spread 1.78 s of 1.80 s |
| 3 | POST through CloudFront **without** `x-amz-content-sha256` is refused | ✓ 4xx |
| 4 | POST through CloudFront **with** `x-amz-content-sha256` streams one by one | ✓ first event 180 ms, arrivals spread 1.80 s of 1.80 s |

## Run it

```bash
aws sso login --profile cferrante-dev-admin
../sse-lambda/deploy.sh      # the function and its URL
./check.sh                   # fails: no distribution yet
./deploy.sh                  # 3-10 minutes for the distribution to deploy
./check.sh                   # all 4 pass
./teardown.sh && ../sse-lambda/teardown.sh
```

The `*.cloudfront.net` address is public while it exists, so tear down after testing.
Cost: $0 within the CloudFront and Lambda free tiers.
