#include "topk.h"

#include <algorithm>

#include "vec.h"

namespace rag {

std::vector<Hit> vector_topk(std::span<const float> vectors,
                             std::span<const std::uint32_t> access,
                             std::uint32_t dim, std::span<const float> query,
                             std::uint32_t mask, std::uint32_t k) {
  std::vector<Hit> hits;
  if (k == 0) return hits;
  hits.reserve(access.size());
  for (std::uint32_t i = 0; i < access.size(); ++i) {
    // The mask is checked before scoring: a hidden chunk is never ranked.
    if ((access[i] & mask) == 0) continue;
    hits.push_back({i, dot(vectors.subspan(std::size_t{i} * dim, dim), query)});
  }
  const std::size_t n = std::min<std::size_t>(k, hits.size());
  // partial_sort orders only the best n; better() breaks ties by id, so the
  // result doesn't depend on the order partial_sort happens to visit them.
  std::partial_sort(hits.begin(), hits.begin() + static_cast<std::ptrdiff_t>(n), hits.end(),
                    better);
  hits.resize(n);
  return hits;
}

}  // namespace rag
