#include "topk.h"

#include <vector>

#include "doctest/doctest.h"

using doctest::Approx;

namespace {

constexpr std::uint32_t kPublic = 0x1;
constexpr std::uint32_t kInvitee = 0x2;

// Four 2-d unit vectors. The query is (1, 0), so scores are the x components:
//   chunk 0: 0.6   public
//   chunk 1: 1.0   invitee only (the best match, but private)
//   chunk 2: 0.8   public
//   chunk 3: 0.0   public
const std::vector<float> kVectors{0.6f, 0.8f, 1.0f, 0.0f, 0.8f, 0.6f, 0.0f, 1.0f};
const std::vector<std::uint32_t> kAccess{kPublic, kInvitee, kPublic, kPublic};
const std::vector<float> kQuery{1.0f, 0.0f};

std::vector<std::uint32_t> ids(const std::vector<rag::Hit>& hits) {
  std::vector<std::uint32_t> out;
  for (const auto& h : hits) out.push_back(h.id);
  return out;
}

}  // namespace

TEST_CASE("a private chunk with the highest score is excluded for mask 0x1") {
  const auto hits = rag::vector_topk(kVectors, kAccess, 2, kQuery, kPublic, 4);
  CHECK(ids(hits) == std::vector<std::uint32_t>{2, 0, 3});
}

TEST_CASE("an invitee sees the private chunk first with mask 0x3") {
  const auto hits = rag::vector_topk(kVectors, kAccess, 2, kQuery, kPublic | kInvitee, 4);
  CHECK(ids(hits) == std::vector<std::uint32_t>{1, 2, 0, 3});
}

TEST_CASE("scores are the dot products, best first") {
  const auto hits = rag::vector_topk(kVectors, kAccess, 2, kQuery, kPublic, 2);
  REQUIRE(hits.size() == 2);
  CHECK(hits[0].score == Approx(0.8f));
  CHECK(hits[1].score == Approx(0.6f));
}

TEST_CASE("k smaller than the visible count keeps only the best k") {
  const auto hits = rag::vector_topk(kVectors, kAccess, 2, kQuery, kPublic | kInvitee, 2);
  CHECK(ids(hits) == std::vector<std::uint32_t>{1, 2});
}

TEST_CASE("k larger than the visible count returns every visible chunk") {
  const auto hits = rag::vector_topk(kVectors, kAccess, 2, kQuery, kPublic, 100);
  CHECK(hits.size() == 3);
}

TEST_CASE("k of zero returns nothing") {
  CHECK(rag::vector_topk(kVectors, kAccess, 2, kQuery, kPublic, 0).empty());
}

TEST_CASE("a mask that matches no chunk returns nothing") {
  CHECK(rag::vector_topk(kVectors, kAccess, 2, kQuery, 0x4, 4).empty());
}

TEST_CASE("equal scores are ordered by lower chunk id") {
  // Chunks 3, 1 and 2 are identical; chunk 0 is worse. Ties must come back 1, 2, 3
  // whatever order a partial sort happens to leave them in.
  const std::vector<float> vecs{0.0f, 1.0f, 1.0f, 0.0f, 1.0f, 0.0f, 1.0f, 0.0f};
  const std::vector<std::uint32_t> access(4, kPublic);
  const auto hits = rag::vector_topk(vecs, access, 2, kQuery, kPublic, 4);
  CHECK(ids(hits) == std::vector<std::uint32_t>{1, 2, 3, 0});
  // and with k cutting through the tie, the lower ids win
  CHECK(ids(rag::vector_topk(vecs, access, 2, kQuery, kPublic, 2)) ==
        std::vector<std::uint32_t>{1, 2});
}

TEST_CASE("an empty index returns nothing") {
  CHECK(rag::vector_topk({}, {}, 2, kQuery, kPublic, 4).empty());
}
