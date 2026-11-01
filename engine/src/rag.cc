// The C ABI. Every function catches all exceptions: one unwinding into Go
// would crash the process (design §5).
#include "rag.h"

#include <algorithm>
#include <cerrno>
#include <cmath>
#include <cstring>
#include <exception>
#include <filesystem>
#include <fstream>
#include <limits>
#include <string>
#include <string_view>
#include <vector>

#include "index_file.h"
#include "search.h"
#include "tokenize.h"
#include "vec.h"

struct rag_index {
  rag::Index index;
};

struct rag_builder {
  std::uint32_t dim;
  std::vector<float> vectors;
  std::vector<std::uint32_t> access;
  std::vector<std::vector<std::string>> docs;
};

namespace {

void set_err(char* err, size_t err_len, std::string_view msg) noexcept {
  if (err == nullptr || err_len == 0) return;
  const size_t n = std::min(err_len - 1, msg.size());
  std::memcpy(err, msg.data(), n);
  err[n] = '\0';
}

std::string os_error(const std::string& what, const char* path) {
  return what + " " + path + ": " + std::strerror(errno);
}

}  // namespace

extern "C" {

rag_index* rag_index_load(const char* path, char* err, size_t err_len) noexcept {
  try {
    if (path == nullptr) {
      set_err(err, err_len, "path is NULL");
      return nullptr;
    }
    std::ifstream in(path, std::ios::binary);
    if (!in) {
      set_err(err, err_len, os_error("cannot open", path));
      return nullptr;
    }
    std::vector<std::uint8_t> bytes((std::istreambuf_iterator<char>(in)),
                                    std::istreambuf_iterator<char>());
    if (in.bad()) {
      set_err(err, err_len, os_error("cannot read", path));
      return nullptr;
    }
    auto* idx = new rag_index;
    std::string msg;
    if (!rag::deserialize(bytes, idx->index, msg)) {
      delete idx;
      set_err(err, err_len, std::string(path) + ": " + msg);
      return nullptr;
    }
    return idx;
  } catch (const std::exception& e) {
    set_err(err, err_len, e.what());
  } catch (...) {
    set_err(err, err_len, "unknown error");
  }
  return nullptr;
}

void rag_index_free(rag_index* idx) noexcept { delete idx; }

uint32_t rag_index_dim(const rag_index* idx) noexcept { return idx ? idx->index.dim : 0; }

int rag_search(const rag_index* idx, const float* query_vec, uint32_t dim,
               const char* query_text, uint32_t access_mask, uint32_t k,
               rag_hit* out) noexcept {
  try {
    if (idx == nullptr || query_vec == nullptr || out == nullptr) return -1;
    if (dim != idx->index.dim) return -1;
    const std::span<const float> q(query_vec, dim);
    // A NaN score would break the sort's ordering rules (undefined behaviour).
    if (!std::all_of(q.begin(), q.end(), [](float x) { return std::isfinite(x); })) return -1;
    const auto hits = rag::hybrid_search(idx->index, q, query_text ? query_text : "",
                                         access_mask, std::min<uint32_t>(k, INT32_MAX));
    for (std::size_t i = 0; i < hits.size(); ++i) out[i] = {hits[i].id, hits[i].score};
    return static_cast<int>(hits.size());
  } catch (...) {
    return -1;
  }
}

rag_builder* rag_builder_new(uint32_t dim, char* err, size_t err_len) noexcept {
  try {
    if (dim == 0) {
      set_err(err, err_len, "dim must be at least 1");
      return nullptr;
    }
    return new rag_builder{dim, {}, {}, {}};
  } catch (const std::exception& e) {
    set_err(err, err_len, e.what());
  } catch (...) {
    set_err(err, err_len, "unknown error");
  }
  return nullptr;
}

int rag_builder_add(rag_builder* b, const char* text, const float* vec, uint32_t access_bits,
                    char* err, size_t err_len) noexcept {
  try {
    if (b == nullptr || text == nullptr || vec == nullptr) {
      set_err(err, err_len, "builder, text and vec must not be NULL");
      return -1;
    }
    if (access_bits == 0) {
      set_err(err, err_len, "access_bits is 0: no caller could ever see this chunk");
      return -1;
    }
    if (b->access.size() >= std::numeric_limits<std::uint32_t>::max()) {
      set_err(err, err_len, "too many chunks");
      return -1;
    }
    std::vector<float> v(vec, vec + b->dim);
    if (!rag::normalize(v)) {
      set_err(err, err_len, "vector is all zeros or not finite");
      return -1;
    }
    b->docs.push_back(rag::tokenize(text));
    b->vectors.insert(b->vectors.end(), v.begin(), v.end());
    b->access.push_back(access_bits);
    return 0;
  } catch (const std::exception& e) {
    set_err(err, err_len, e.what());
  } catch (...) {
    set_err(err, err_len, "unknown error");
  }
  return -1;
}

int rag_builder_write(const rag_builder* b, const char* path, char* err,
                      size_t err_len) noexcept {
  try {
    if (b == nullptr || path == nullptr) {
      set_err(err, err_len, "builder and path must not be NULL");
      return -1;
    }
    rag::Index idx;
    idx.dim = b->dim;
    idx.vectors = b->vectors;
    idx.access = b->access;
    idx.bm25 = rag::bm25_build(b->docs);
    const auto bytes = rag::serialize(idx);
    std::ofstream out(path, std::ios::binary | std::ios::trunc);
    if (!out) {
      set_err(err, err_len, os_error("cannot create", path));
      return -1;
    }
    out.write(reinterpret_cast<const char*>(bytes.data()),
              static_cast<std::streamsize>(bytes.size()));
    out.close();
    if (!out) {
      set_err(err, err_len, os_error("cannot write", path));
      return -1;
    }
    return 0;
  } catch (const std::exception& e) {
    set_err(err, err_len, e.what());
  } catch (...) {
    set_err(err, err_len, "unknown error");
  }
  return -1;
}

void rag_builder_free(rag_builder* b) noexcept { delete b; }

}  // extern "C"
