#include "search.h"

#include <algorithm>

#include "rrf.h"
#include "tokenize.h"
#include "topk.h"

namespace rag {

std::vector<Hit> hybrid_search(const Index& index, std::span<const float> query_vec,
                               std::string_view query_text, std::uint32_t mask,
                               std::uint32_t k) {
  if (k == 0) return {};
  const std::uint32_t depth = std::max(k, kCandidates);
  std::vector<std::vector<Hit>> lists;

  const bool has_vector =
      std::any_of(query_vec.begin(), query_vec.end(), [](float x) { return x != 0.0f; });
  if (has_vector) {
    lists.push_back(vector_topk(index.vectors, index.access, index.dim, query_vec, mask, depth));
  }
  // No tokens gives an empty BM25 list, which adds nothing to the fusion.
  lists.push_back(bm25_topk(index.bm25, tokenize(query_text), index.access, mask, depth));
  return rrf(lists, k);
}

}  // namespace rag
