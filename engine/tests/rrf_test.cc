#include "rrf.h"

#include <vector>

#include "doctest/doctest.h"

using doctest::Approx;
using rag::Hit;

namespace {

// Scores in the input lists are ignored by RRF: only positions matter.
std::vector<Hit> ranked(std::vector<std::uint32_t> ids) {
  std::vector<Hit> out;
  float s = 100.0f;
  for (auto id : ids) out.push_back({id, s--});
  return out;
}

std::vector<std::uint32_t> ids(const std::vector<Hit>& hits) {
  std::vector<std::uint32_t> out;
  for (const auto& h : hits) out.push_back(h.id);
  return out;
}

}  // namespace

TEST_CASE("fusion reorders: a chunk ranked well in both lists beats a single first place") {
  // vector: 0, 1, 2   BM25: 2, 1
  //   0: 1/61               = 0.0163934
  //   1: 1/62 + 1/62        = 0.0322581
  //   2: 1/63 + 1/61        = 0.0322664   (just ahead of 1)
  const std::vector<std::vector<Hit>> lists{ranked({0, 1, 2}), ranked({2, 1})};
  const auto hits = rag::rrf(lists, 10);
  CHECK(ids(hits) == std::vector<std::uint32_t>{2, 1, 0});
  REQUIRE(hits.size() == 3);
  CHECK(hits[0].score == Approx(1.0 / 63 + 1.0 / 61));
  CHECK(hits[1].score == Approx(2.0 / 62));
  CHECK(hits[2].score == Approx(1.0 / 61));
}

TEST_CASE("a single list keeps its order, scored 1/(60 + rank)") {
  const std::vector<std::vector<Hit>> lists{ranked({7, 3, 5})};
  const auto hits = rag::rrf(lists, 10);
  CHECK(ids(hits) == std::vector<std::uint32_t>{7, 3, 5});
  REQUIRE(hits.size() == 3);
  CHECK(hits[0].score == Approx(1.0 / 61));
  CHECK(hits[2].score == Approx(1.0 / 63));
}

TEST_CASE("equal fused scores are ordered by lower id") {
  const std::vector<std::vector<Hit>> lists{ranked({5}), ranked({3})};
  CHECK(ids(rag::rrf(lists, 10)) == std::vector<std::uint32_t>{3, 5});
}

TEST_CASE("k limits the result; k of zero and no lists return nothing") {
  const std::vector<std::vector<Hit>> lists{ranked({0, 1, 2}), ranked({2, 1})};
  CHECK(ids(rag::rrf(lists, 1)) == std::vector<std::uint32_t>{2});
  CHECK(rag::rrf(lists, 0).empty());
  CHECK(rag::rrf({}, 10).empty());
}
