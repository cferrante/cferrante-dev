# Spike 1.2: cgo binary on Lambda

**Question:** can a Go binary that calls C++ through cgo, built on Ubuntu 26.04 (glibc 2.43),
run on Lambda's `provided.al2023` runtime (Amazon Linux 2023, glibc 2.34)?

**Answer: yes, if it is linked fully static.** Findings and their design impact are recorded in
the [plan](../../docs/plan.md#spike-findings) and [design §5](../../docs/design.md#5-retrieval-engine-c).

## What's here

| Path | Purpose |
|---|---|
| `engine/` | C++ `engine_sum()` behind a C interface, called from Go via cgo; `go test` covers it |
| `cmd/bootstrap/` | Minimal Lambda custom runtime: reads events from the Runtime API, returns the C++ sum plus the OS and glibc it ran on |
| `check-glibc.sh` | The spike's test: fails a binary that needs glibc newer than 2.34, or that links a non-glibc shared library |
| `check-glibc_test.sh`, `testdata/` | Self-test proving the checker passes an old-glibc binary and rejects one needing `GLIBC_2.38` |
| `Makefile` | Three build variants: `naive`, `static-libs`, `full-static`; `make check-<variant>` builds and checks one |
| `deploy.sh`, `teardown.sh` | Idempotent deploy-and-invoke to the project AWS account, and clean removal |

## Results (x86_64)

| Build | `check-glibc.sh` | On Lambda |
|---|---|---|
| `naive` | ✗ links `libstdc++.so.6` from GCC 15 | not deployed |
| `static-libs` (`-static-libstdc++ -static-libgcc`) | ✗ needs `_dl_find_object` (2.35, libgcc), `arc4random` (2.36) and `__isoc23_strtoul` (2.38, both libstdc++) | `Runtime.ExitError`; log: ``version `GLIBC_2.38' not found`` and ``version `GLIBC_2.36' not found`` |
| `full-static` (`-static`, tags `netgo,osusergo`) | ✓ needs no system libraries | `{"sum":10,"libc":"2.43","os":"Amazon Linux 2023.12.20261021",…}` |

`libc` reports 2.43 for the static build because it carries its own glibc.

## Run it

```bash
make self-test            # the checker can fail
make check-full-static    # build and check
aws sso login --profile cferrante-dev-admin
./deploy.sh               # or: ./deploy.sh static-libs to watch it fail on Lambda
./teardown.sh             # removes the function, its logs and its role
```

Cost: $0 within the Lambda free tier. Nothing is left running after `teardown.sh`.
