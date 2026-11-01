#include "search.h"

#include <cmath>
#include <string>
#include <vector>

#include "doctest/doctest.h"
#include "tokenize.h"
#include "vec.h"

using doctest::Approx;

namespace {

rag::Index make_index(std::uint32_t dim, std::vector<float> vecs,
                      const std::vector<std::string>& texts,
                      std::vector<std::uint32_t> access) {
  rag::Index idx;
  idx.dim = dim;
  for (std::size_t i = 0; i < texts.size(); ++i) {
    REQUIRE(rag::normalize(std::span<float>(vecs).subspan(i * dim, dim)));
  }
  idx.vectors = std::move(vecs);
  idx.access = std::move(access);
  std::vector<std::vector<std::string>> docs;
  for (const auto& t : texts) docs.push_back(rag::tokenize(t));
  idx.bm25 = rag::bm25_build(docs);
  return idx;
}

std::vector<std::uint32_t> ids(const std::vector<rag::Hit>& hits) {
  std::vector<std::uint32_t> out;
  for (const auto& h : hits) out.push_back(h.id);
  return out;
}

// Query vector (1, 0) ranks chunks 0, 1, 2 by vector; only chunk 2 says "lambda".
//   vector: 0, 1, 2   BM25 "lambda": 2
//   fused:  2 = 1/63 + 1/61, 0 = 1/61, 1 = 1/62
const std::vector<float> kQuery{1.0f, 0.0f};
const std::vector<float> kZero{0.0f, 0.0f};

rag::Index three() {
  return make_index(2, {1.0f, 0.0f, 0.8f, 0.6f, 0.6f, 0.8f},
                    {"go engine", "serverless go", "lambda functions"}, {0x1, 0x1, 0x1});
}

}  // namespace

TEST_CASE("hybrid search fuses vector and keyword ranks") {
  const auto idx = three();
  const auto hits = rag::hybrid_search(idx, kQuery, "Lambda?", 0x1, 10);
  CHECK(ids(hits) == std::vector<std::uint32_t>{2, 0, 1});
  REQUIRE(hits.size() == 3);
  CHECK(hits[0].score == Approx(1.0 / 63 + 1.0 / 61));
}

TEST_CASE("empty query text means vector search only") {
  const auto idx = three();
  const auto hits = rag::hybrid_search(idx, kQuery, "", 0x1, 10);
  CHECK(ids(hits) == std::vector<std::uint32_t>{0, 1, 2});
  REQUIRE(hits.size() == 3);
  CHECK(hits[0].score == Approx(1.0 / 61));
}

TEST_CASE("query text with no tokens means vector search only") {
  const auto idx = three();
  CHECK(ids(rag::hybrid_search(idx, kQuery, " ?! ", 0x1, 10)) ==
        std::vector<std::uint32_t>{0, 1, 2});
}

TEST_CASE("an all-zero query vector means BM25 only") {
  const auto idx = three();
  const auto hits = rag::hybrid_search(idx, kZero, "lambda", 0x1, 10);
  CHECK(ids(hits) == std::vector<std::uint32_t>{2});
  REQUIRE(hits.size() == 1);
  CHECK(hits[0].score == Approx(1.0 / 61));
}

TEST_CASE("zero vector and empty text return nothing") {
  const auto idx = three();
  CHECK(rag::hybrid_search(idx, kZero, "", 0x1, 10).empty());
}

TEST_CASE("a hidden chunk is excluded from both lists") {
  auto idx = three();
  idx.access[2] = 0x2;  // the only "lambda" chunk becomes invitee-only
  CHECK(ids(rag::hybrid_search(idx, kQuery, "lambda", 0x1, 10)) ==
        std::vector<std::uint32_t>{0, 1});
  CHECK(rag::hybrid_search(idx, kZero, "lambda", 0x1, 10).empty());
  CHECK(ids(rag::hybrid_search(idx, kZero, "lambda", 0x3, 10)) == std::vector<std::uint32_t>{2});
}

TEST_CASE("k limits the hits") {
  const auto idx = three();
  CHECK(ids(rag::hybrid_search(idx, kQuery, "lambda", 0x1, 1)) == std::vector<std::uint32_t>{2});
  CHECK(rag::hybrid_search(idx, kQuery, "lambda", 0x1, 0).empty());
}

TEST_CASE("each list contributes max(k, 50) candidates to fusion") {
  // 60 chunks at increasing angles from the query, so vector rank = id + 1.
  // Only chunk 54 (vector rank 55) says "needle", so it is BM25 rank 1.
  constexpr std::uint32_t n = 60;
  std::vector<float> vecs;
  std::vector<std::string> texts;
  for (std::uint32_t i = 0; i < n; ++i) {
    vecs.push_back(std::cos(0.01f * static_cast<float>(i)));
    vecs.push_back(std::sin(0.01f * static_cast<float>(i)));
    texts.push_back(i == 54 ? "needle" : "filler");
  }
  const auto idx = make_index(2, vecs, texts, std::vector<std::uint32_t>(n, 0x1));

  // k = 1: depth 50, so chunk 54's vector rank 55 is cut off. Chunk 0 (vector
  // rank 1) and chunk 54 (BM25 rank 1) both score 1/61; the lower id wins.
  CHECK(ids(rag::hybrid_search(idx, kQuery, "needle", 0x1, 1)) == std::vector<std::uint32_t>{0});

  // k = 60: depth 60, so chunk 54 also gets 1/115 from its vector rank and wins.
  const auto deep = rag::hybrid_search(idx, kQuery, "needle", 0x1, 60);
  REQUIRE(deep.size() == 60);
  CHECK(deep[0].id == 54);
  CHECK(deep[0].score == Approx(1.0 / 61 + 1.0 / 115));
}
