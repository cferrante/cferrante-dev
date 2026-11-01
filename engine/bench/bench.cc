// Retrieval benchmark (plan step 2.6, benchmarks.md "Retrieval latency").
//
// Builds a synthetic corpus from a fixed seed through the C ABI (builder ->
// file -> load) and times rag_search one call at a time, as Go will call it.
// Words follow a Zipf distribution so common terms have long postings, as in
// real text. Prints p50/p95/p99 and, by default, exits 1 if hybrid p99 is not
// under the gate (1 ms).
//
// usage: rag_bench            2k x 512 with the gate
//        rag_bench --scale    1k..50k chunks, informational, no gate
#define ANKERL_NANOBENCH_IMPLEMENT
#include "nanobench/nanobench.h"

#include <algorithm>
#include <chrono>
#include <cstdio>
#include <cstring>
#include <filesystem>
#include <fstream>
#include <random>
#include <string>
#include <vector>

#include <unistd.h>

#include "rag.h"

namespace {

constexpr std::uint32_t kDim = 512;
constexpr std::uint32_t kK = 10;
constexpr int kVocab = 5000;
constexpr int kQueries = 100;
constexpr double kGateMs = 1.0;
constexpr int kLoads = 11;

struct Query {
  std::vector<float> vec;
  std::string text;
};

struct Corpus {
  rag_index* index = nullptr;
  double load_ms = 0;  // median of kLoads rag_index_load calls (cold-start cost)
  std::vector<Query> queries;
};

std::vector<float> random_vec(std::mt19937& rng) {
  std::normal_distribution<float> n(0.0f, 1.0f);
  std::vector<float> v(kDim);
  for (auto& x : v) x = n(rng);
  return v;
}

Corpus make_corpus(std::uint32_t chunks) {
  std::mt19937 rng(42);
  std::vector<double> weights(kVocab);
  for (int r = 0; r < kVocab; ++r) weights[r] = 1.0 / (r + 1);
  std::discrete_distribution<int> word(weights.begin(), weights.end());
  std::uniform_int_distribution<int> length(80, 160);
  auto words = [&](int n) {
    std::string s;
    for (int i = 0; i < n; ++i) s += "w" + std::to_string(word(rng)) + " ";
    return s;
  };

  char err[256] = "";
  rag_builder* b = rag_builder_new(kDim, err, sizeof err);
  for (std::uint32_t i = 0; i < chunks; ++i) {
    const auto v = random_vec(rng);
    // One chunk in eight is invitee-only, so the mask check is exercised.
    if (rag_builder_add(b, words(length(rng)).c_str(), v.data(), i % 8 == 0 ? 0x2 : 0x1, err,
                        sizeof err) != 0) {
      std::fprintf(stderr, "add: %s\n", err);
      std::exit(2);
    }
  }
  const auto path = std::filesystem::temp_directory_path() /
                    ("rag_bench_" + std::to_string(::getpid()) + ".idx");
  if (rag_builder_write(b, path.c_str(), err, sizeof err) != 0) {
    std::fprintf(stderr, "write: %s\n", err);
    std::exit(2);
  }
  rag_builder_free(b);
  Corpus c;
  std::vector<double> load_ms;
  for (int i = 0; i < kLoads; ++i) {
    if (c.index != nullptr) rag_index_free(c.index);
    const auto t0 = std::chrono::steady_clock::now();
    c.index = rag_index_load(path.c_str(), err, sizeof err);
    load_ms.push_back(
        std::chrono::duration<double, std::milli>(std::chrono::steady_clock::now() - t0).count());
  }
  std::sort(load_ms.begin(), load_ms.end());
  c.load_ms = load_ms[kLoads / 2];
  std::printf("%-12s %6u x %u   median %.2f ms, %.1f MB file (n = %d)\n", "load", chunks, kDim,
              c.load_ms, static_cast<double>(std::filesystem::file_size(path)) / 1e6, kLoads);
  std::filesystem::remove(path);
  if (c.index == nullptr) {
    std::fprintf(stderr, "load: %s\n", err);
    std::exit(2);
  }
  for (int q = 0; q < kQueries; ++q) c.queries.push_back({random_vec(rng), words(6)});
  return c;
}

struct Percentiles {
  double p50, p95, p99;  // milliseconds
};

// Nearest-rank percentiles over per-call times.
Percentiles measure(const char* name, const Corpus& c, bool with_text, int calls) {
  std::size_t next = 0;
  rag_hit out[kK];
  ankerl::nanobench::Bench bench;
  bench.output(nullptr).warmup(100).epochs(static_cast<std::size_t>(calls)).epochIterations(1);
  bench.run(name, [&] {
    const auto& q = c.queries[next++ % c.queries.size()];
    const int n =
        rag_search(c.index, q.vec.data(), kDim, with_text ? q.text.c_str() : nullptr, 0x1, kK, out);
    ankerl::nanobench::doNotOptimizeAway(n);
  });
  const auto& r = bench.results().back();
  std::vector<double> ms;
  for (std::size_t i = 0; i < r.size(); ++i) {
    ms.push_back(r.get(i, ankerl::nanobench::Result::Measure::elapsed) * 1e3);
  }
  std::sort(ms.begin(), ms.end());
  auto at = [&](double p) {
    const auto rank = static_cast<std::size_t>(std::ceil(p * static_cast<double>(ms.size())));
    return ms[std::max<std::size_t>(rank, 1) - 1];
  };
  return {at(0.50), at(0.95), at(0.99)};
}

// CI runners vary by about 2x between hosts (see benchmarks.md), so every run
// names the CPU it measured on.
std::string cpu_model() {
  std::ifstream in("/proc/cpuinfo");
  for (std::string line; std::getline(in, line);) {
    if (line.rfind("model name", 0) == 0) return line.substr(line.find(':') + 2);
  }
  return "unknown";
}

void row(const char* name, std::uint32_t chunks, const Percentiles& p) {
  std::printf("%-12s %6u x %u   p50 %7.3f ms   p95 %7.3f ms   p99 %7.3f ms\n", name, chunks, kDim,
              p.p50, p.p95, p.p99);
}

}  // namespace

int main(int argc, char** argv) {
  std::printf("cpu: %s\n", cpu_model().c_str());
  if (argc > 1 && std::strcmp(argv[1], "--scale") == 0) {
    for (std::uint32_t n : {1000u, 2000u, 5000u, 10000u, 20000u, 50000u}) {
      auto c = make_corpus(n);
      row("hybrid", n, measure("hybrid", c, true, 500));
      rag_index_free(c.index);
    }
    return 0;
  }
  auto c = make_corpus(2000);
  const auto vec = measure("vector-only", c, false, 2000);
  const auto hyb = measure("hybrid", c, true, 2000);
  row("vector-only", 2000, vec);
  row("hybrid", 2000, hyb);
  rag_index_free(c.index);
  const bool ok = hyb.p99 < kGateMs;
  std::printf("%s hybrid p99 %.3f ms %s %.1f ms gate\n", ok ? "✓" : "✗", hyb.p99, ok ? "<" : ">=",
              kGateMs);
  return ok ? 0 : 1;
}
