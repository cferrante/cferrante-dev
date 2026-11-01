// Hybrid search over an in-memory index: vector + BM25, fused by RRF.
#pragma once

#include <cstdint>
#include <span>
#include <string_view>
#include <vector>

#include "bm25.h"
#include "hit.h"

namespace rag {

// Each list contributes its best max(k, kCandidates) visible chunks to fusion.
inline constexpr std::uint32_t kCandidates = 50;

struct Index {
  std::uint32_t dim = 0;
  std::vector<float> vectors;          // access.size() x dim, row-major, normalized
  std::vector<std::uint32_t> access;   // access bits per chunk
  Bm25Index bm25;
};

// Precondition: query_vec.size() == index.dim (the C ABI checks this).
// Empty query text, or text with no tokens, means vector search only; an
// all-zero query_vec means BM25 only; both means no hits. Returns at most k
// hits, best first, scored by RRF.
std::vector<Hit> hybrid_search(const Index& index, std::span<const float> query_vec,
                               std::string_view query_text, std::uint32_t mask,
                               std::uint32_t k);

}  // namespace rag
