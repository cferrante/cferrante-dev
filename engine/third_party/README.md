# Vendored third-party headers

Single-header libraries copied into the repo so builds need no download and
CI works offline. Each is pinned to a release tag; the SHA-256 below is of
the file as committed. To upgrade, download the new tag's header, update this
table, and record the change in the plan's deviations log.

| Library | Tag | Source | SHA-256 | Licence |
|---|---|---|---|---|
| doctest | v2.5.3 | `doctest/doctest.h` in github.com/doctest/doctest | `cfd518a3ef90f67e1f3ba514df23fb3627437de1a2feeba78cf5062a40021421` | MIT |
| nanobench | v4.6.0 | `src/include/nanobench.h` in github.com/martinus/nanobench | `2cfcdd57044fd0ee11d1be2b5dc545781b05c36049613c0089a73b332afd8c4d` | MIT |

Verify: `sha256sum engine/third_party/*/*.h` (run from the repo root).
