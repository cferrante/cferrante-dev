// BM25 keyword scoring over an inverted index (design §5).
#pragma once

#include <cstdint>
#include <functional>
#include <map>
#include <span>
#include <string>
#include <vector>

#include "hit.h"

namespace rag {

inline constexpr double kBm25K1 = 1.2;
inline constexpr double kBm25B = 0.75;

struct Posting {
  std::uint32_t doc;  // chunk id
  std::uint32_t tf;   // times the term occurs in that chunk
};

struct Bm25Index {
  // term -> postings in increasing doc order. A sorted map keeps the index file
  // byte-for-byte deterministic; std::less<> allows lookup without a copy.
  std::map<std::string, std::vector<Posting>, std::less<>> postings;
  std::vector<std::uint32_t> doc_len;  // tokens per chunk
};

// Builds the index from each chunk's tokens; chunk ids are positions in docs.
Bm25Index bm25_build(const std::vector<std::vector<std::string>>& docs);

// Scores visible chunks (access bits intersect mask) against the query terms
// and returns at most n with a positive score, best first. Each distinct query
// term counts once. IDF is Lucene's non-negative form,
//   idf = ln(1 + (N - n_t + 0.5) / (n_t + 0.5)),
// with N and the average length taken over all chunks, visible or not.
std::vector<Hit> bm25_topk(const Bm25Index& index, std::span<const std::string> query,
                           std::span<const std::uint32_t> access, std::uint32_t mask,
                           std::uint32_t n);

}  // namespace rag
