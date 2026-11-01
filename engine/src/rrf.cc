#include "rrf.h"

#include <algorithm>
#include <unordered_map>
#include <utility>

namespace rag {

std::vector<Hit> rrf(std::span<const std::vector<Hit>> lists, std::uint32_t k) {
  if (k == 0) return {};
  std::unordered_map<std::uint32_t, double> fused;
  for (const auto& list : lists) {
    for (std::size_t r = 0; r < list.size(); ++r) {
      fused[list[r].id] += 1.0 / (kRrfK + static_cast<double>(r + 1));
    }
  }
  // Rank in double, then convert: two scores that round to the same float are
  // still ordered by their true value rather than by id.
  std::vector<std::pair<std::uint32_t, double>> ranked(fused.begin(), fused.end());
  const std::size_t keep = std::min<std::size_t>(k, ranked.size());
  std::partial_sort(ranked.begin(), ranked.begin() + static_cast<std::ptrdiff_t>(keep),
                    ranked.end(), [](const auto& a, const auto& b) {
                      if (a.second != b.second) return a.second > b.second;
                      return a.first < b.first;
                    });
  std::vector<Hit> hits;
  hits.reserve(keep);
  for (std::size_t i = 0; i < keep; ++i) {
    hits.push_back({ranked[i].first, static_cast<float>(ranked[i].second)});
  }
  return hits;
}

}  // namespace rag
