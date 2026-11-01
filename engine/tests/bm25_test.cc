#include "bm25.h"

#include <vector>

#include "doctest/doctest.h"
#include "tokenize.h"

using doctest::Approx;

namespace {

// Three chunks, lengths 3, 3 and 4 (average 10/3):
//   d0 "Go engine, fast."         go:1 engine:1 fast:1
//   d1 "C++ engine engine"        c:1 engine:2
//   d2 "go GO lambda serverless"  go:2 lambda:1 serverless:1
const std::vector<std::vector<std::string>> kDocs{
    rag::tokenize("Go engine, fast."),
    rag::tokenize("C++ engine engine"),
    rag::tokenize("go GO lambda serverless"),
};
const std::vector<std::uint32_t> kAllPublic{0x1, 0x1, 0x1};

// Hand-computed, k1 = 1.2, b = 0.75, N = 3:
//   idf(go) = idf(engine) = ln(1 + (3 - 2 + 0.5) / (2 + 0.5)) = ln(1.6)  = 0.4700036
//   idf(lambda)           = ln(1 + (3 - 1 + 0.5) / (1 + 0.5)) = ln(8/3) = 0.9808293
//   term(tf, len) = tf * 2.2 / (tf + 1.2 * (0.25 + 0.75 * len / (10/3)))
//   "go engine": d0 = 2 * 0.4700036 * term(1, 3) = 0.9801024
//                d1 =     0.4700036 * term(2, 3) = 0.6649569
//                d2 =     0.4700036 * term(2, 4) = 0.6118390
//   "lambda":    d2 =     0.9808293 * term(1, 4) = 0.9066489
constexpr double kEps = 1e-5;

}  // namespace

TEST_CASE("build records postings in doc order and chunk lengths") {
  const auto idx = rag::bm25_build(kDocs);
  CHECK(idx.doc_len == std::vector<std::uint32_t>{3, 3, 4});
  REQUIRE(idx.postings.count("engine") == 1);
  const auto& engine = idx.postings.at("engine");
  REQUIRE(engine.size() == 2);
  CHECK(engine[0].doc == 0);
  CHECK(engine[0].tf == 1);
  CHECK(engine[1].doc == 1);
  CHECK(engine[1].tf == 2);
  CHECK(idx.postings.size() == 6);  // go engine fast c lambda serverless
}

TEST_CASE("scores match hand-computed values to 1e-5") {
  const auto idx = rag::bm25_build(kDocs);
  const auto q = rag::tokenize("go engine");
  const auto hits = rag::bm25_topk(idx, q, kAllPublic, 0x1, 10);
  REQUIRE(hits.size() == 3);
  CHECK(hits[0].id == 0);
  CHECK(hits[0].score == Approx(0.9801024).epsilon(kEps));
  CHECK(hits[1].id == 1);
  CHECK(hits[1].score == Approx(0.6649569).epsilon(kEps));
  CHECK(hits[2].id == 2);
  CHECK(hits[2].score == Approx(0.6118390).epsilon(kEps));

  const auto lambda = rag::bm25_topk(idx, rag::tokenize("Lambda"), kAllPublic, 0x1, 10);
  REQUIRE(lambda.size() == 1);
  CHECK(lambda[0].id == 2);
  CHECK(lambda[0].score == Approx(0.9066489).epsilon(kEps));
}

TEST_CASE("a repeated query term counts once") {
  const auto idx = rag::bm25_build(kDocs);
  const auto once = rag::bm25_topk(idx, rag::tokenize("lambda"), kAllPublic, 0x1, 10);
  const auto twice = rag::bm25_topk(idx, rag::tokenize("lambda LAMBDA"), kAllPublic, 0x1, 10);
  REQUIRE(once.size() == 1);
  REQUIRE(twice.size() == 1);
  CHECK(twice[0].score == Approx(once[0].score).epsilon(kEps));
}

TEST_CASE("hidden chunks are not returned, but still count in N and average length") {
  const auto idx = rag::bm25_build(kDocs);
  const std::vector<std::uint32_t> access{0x1, 0x2, 0x1};  // d1 invitee-only
  const auto hits = rag::bm25_topk(idx, rag::tokenize("go engine"), access, 0x1, 10);
  REQUIRE(hits.size() == 2);
  CHECK(hits[0].id == 0);
  CHECK(hits[0].score == Approx(0.9801024).epsilon(kEps));  // unchanged statistics
  CHECK(hits[1].id == 2);
}

TEST_CASE("n limits the number of hits") {
  const auto idx = rag::bm25_build(kDocs);
  const auto hits = rag::bm25_topk(idx, rag::tokenize("go engine"), kAllPublic, 0x1, 1);
  REQUIRE(hits.size() == 1);
  CHECK(hits[0].id == 0);
}

TEST_CASE("terms not in the index, and empty queries, return nothing") {
  const auto idx = rag::bm25_build(kDocs);
  CHECK(rag::bm25_topk(idx, rag::tokenize("python"), kAllPublic, 0x1, 10).empty());
  CHECK(rag::bm25_topk(idx, {}, kAllPublic, 0x1, 10).empty());
}

TEST_CASE("equal scores are ordered by lower chunk id") {
  const std::vector<std::vector<std::string>> same{{"x", "y"}, {"z"}, {"x", "y"}};
  const auto idx = rag::bm25_build(same);
  const auto hits = rag::bm25_topk(idx, rag::tokenize("x"), kAllPublic, 0x1, 10);
  REQUIRE(hits.size() == 2);
  CHECK(hits[0].id == 0);
  CHECK(hits[1].id == 2);
}

TEST_CASE("an index of empty chunks scores nothing and doesn't divide by zero") {
  const auto idx = rag::bm25_build({{}, {}});
  CHECK(rag::bm25_topk(idx, rag::tokenize("go"), std::vector<std::uint32_t>{0x1, 0x1}, 0x1, 10).empty());
}
