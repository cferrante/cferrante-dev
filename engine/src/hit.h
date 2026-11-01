// A ranked search result and the ordering every result list uses.
#pragma once

#include <cstdint>

namespace rag {

struct Hit {
  std::uint32_t id;
  float score;
};

// Best first: higher score, then lower id, so equal scores always come back in
// the same order (design §5).
inline bool better(const Hit& a, const Hit& b) noexcept {
  if (a.score != b.score) return a.score > b.score;
  return a.id < b.id;
}

}  // namespace rag
