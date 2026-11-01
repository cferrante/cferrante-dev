// Reciprocal Rank Fusion: merges ranked lists using ranks only (design §5).
#pragma once

#include <cstdint>
#include <span>
#include <vector>

#include "hit.h"

namespace rag {

inline constexpr double kRrfK = 60.0;

// Each list is best first. A chunk's fused score is the sum over the lists it
// appears in of 1 / (60 + rank), with rank starting at 1. Returns at most k
// hits, best first (ties by lower id).
std::vector<Hit> rrf(std::span<const std::vector<Hit>> lists, std::uint32_t k);

}  // namespace rag
