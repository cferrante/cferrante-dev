/* Retrieval engine C ABI (design §5): the only boundary cgo crosses.
 *
 * Memory: the caller allocates every buffer (err, out); the engine never hands
 * heap memory back except the opaque index and builder, freed by their _free.
 * Errors: functions return NULL or -1 and, when err is non-NULL and err_len > 0,
 * write a NUL-terminated message. No C++ exception ever crosses this boundary.
 * Threads: rag_search is read-only and safe to call concurrently on one index;
 * a builder is used by one thread. */
#ifndef RAG_H
#define RAG_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
#define RAG_NOEXCEPT noexcept
extern "C" {
#else
#define RAG_NOEXCEPT
#endif

typedef struct rag_index rag_index; /* opaque */
typedef struct {
  uint32_t chunk_id;
  float score;
} rag_hit;

/* Loads an index file. Returns NULL with a message for a missing, wrong-version,
   truncated or corrupt file. */
rag_index* rag_index_load(const char* path, char* err, size_t err_len) RAG_NOEXCEPT;
void rag_index_free(rag_index* idx) RAG_NOEXCEPT;

/* The vector length the index was built with, or 0 when idx is NULL. */
uint32_t rag_index_dim(const rag_index* idx) RAG_NOEXCEPT;

/* The number of chunks in the index, or 0 when idx is NULL. No search returns more hits. */
uint32_t rag_index_count(const rag_index* idx) RAG_NOEXCEPT;

/* Hybrid vector + BM25 search. Only chunks whose access bits intersect
   access_mask are considered, so private chunks never leave the engine for
   public callers. Writes up to k hits to out (sorted by fused score) and returns
   the hit count, or -1 when idx, query_vec or out is NULL, dim differs from the
   index's, or query_vec has a non-finite value. query_text may be NULL (no
   keyword search); an all-zero query_vec means keyword search only. */
int rag_search(const rag_index* idx, const float* query_vec, uint32_t dim,
               const char* query_text, uint32_t access_mask, uint32_t k,
               rag_hit* out) RAG_NOEXCEPT;

/* Index builder, used by the Go indexer through cgo. Chunk ids are assigned in
   add order, starting at 0. Each returns 0 on success, -1 on error. add copies
   and normalizes vec (dim floats), rejecting a zero or non-finite vector and
   access_bits of 0 (a chunk nobody could see). */
typedef struct rag_builder rag_builder; /* opaque */

rag_builder* rag_builder_new(uint32_t dim, char* err, size_t err_len) RAG_NOEXCEPT;
int rag_builder_add(rag_builder* b, const char* text, const float* vec, uint32_t access_bits,
                    char* err, size_t err_len) RAG_NOEXCEPT;
int rag_builder_write(const rag_builder* b, const char* path, char* err,
                      size_t err_len) RAG_NOEXCEPT;
void rag_builder_free(rag_builder* b) RAG_NOEXCEPT;

#ifdef __cplusplus
}
#endif

#endif /* RAG_H */
