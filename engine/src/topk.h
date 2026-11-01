// Brute-force vector search: score every visible chunk, keep the best k.
#pragma once

#include <cstdint>
#include <span>
#include <vector>

#include "hit.h"

namespace rag {

// vectors holds access.size() chunks of dim floats each, row-major and
// normalized, so the dot product is the cosine similarity. Only chunks whose
// access bits intersect mask are scored, so a hidden chunk can never take a
// visible chunk's place. Returns at most k hits, best first (see better()).
std::vector<Hit> vector_topk(std::span<const float> vectors,
                             std::span<const std::uint32_t> access,
                             std::uint32_t dim, std::span<const float> query,
                             std::uint32_t mask, std::uint32_t k);

}  // namespace rag
