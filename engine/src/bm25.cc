#include "bm25.h"

#include <algorithm>
#include <cmath>
#include <string_view>

namespace rag {

Bm25Index bm25_build(const std::vector<std::vector<std::string>>& docs) {
  Bm25Index idx;
  idx.doc_len.reserve(docs.size());
  for (std::uint32_t d = 0; d < docs.size(); ++d) {
    std::map<std::string_view, std::uint32_t> tf;
    for (const auto& t : docs[d]) ++tf[t];
    // Docs are visited in id order, so each postings list stays sorted by doc.
    for (const auto& [term, count] : tf) idx.postings[std::string(term)].push_back({d, count});
    idx.doc_len.push_back(static_cast<std::uint32_t>(docs[d].size()));
  }
  return idx;
}

std::vector<Hit> bm25_topk(const Bm25Index& index, std::span<const std::string> query,
                           std::span<const std::uint32_t> access, std::uint32_t mask,
                           std::uint32_t n) {
  std::vector<Hit> hits;
  const std::size_t num_docs = index.doc_len.size();
  if (n == 0 || num_docs == 0 || query.empty()) return hits;

  // Statistics cover only the chunks this caller can see, so hidden chunks have
  // no effect at all on the scores (design §5).
  const auto visible = [&](std::uint32_t d) { return (access[d] & mask) != 0; };
  double num_visible = 0, total_len = 0;
  for (std::uint32_t d = 0; d < num_docs; ++d) {
    if (visible(d)) {
      ++num_visible;
      total_len += index.doc_len[d];
    }
  }
  // No visible tokens means no visible postings, so avg_len is never a divisor.
  if (num_visible == 0) return hits;
  const double avg_len = total_len / num_visible;

  // Each distinct query term counts once.
  std::vector<std::string_view> terms(query.begin(), query.end());
  std::sort(terms.begin(), terms.end());
  terms.erase(std::unique(terms.begin(), terms.end()), terms.end());

  std::vector<double> scores(num_docs, 0.0);
  for (auto term : terms) {
    const auto it = index.postings.find(term);
    if (it == index.postings.end()) continue;
    double df = 0;
    for (const auto& p : it->second) df += visible(p.doc) ? 1 : 0;
    if (df == 0) continue;
    const double idf = std::log(1.0 + (num_visible - df + 0.5) / (df + 0.5));
    for (const auto& p : it->second) {
      if (!visible(p.doc)) continue;  // a hidden chunk is never scored
      const double tf = p.tf;
      const double norm = kBm25K1 * (1.0 - kBm25B + kBm25B * index.doc_len[p.doc] / avg_len);
      scores[p.doc] += idf * tf * (kBm25K1 + 1.0) / (tf + norm);
    }
  }

  for (std::uint32_t d = 0; d < num_docs; ++d) {
    if (scores[d] > 0.0) {  // only visible chunks were scored
      hits.push_back({d, static_cast<float>(scores[d])});
    }
  }
  const std::size_t keep = std::min<std::size_t>(n, hits.size());
  std::partial_sort(hits.begin(), hits.begin() + static_cast<std::ptrdiff_t>(keep), hits.end(),
                    better);
  hits.resize(keep);
  return hits;
}

}  // namespace rag
