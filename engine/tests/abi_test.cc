#include <cmath>
#include <cstdio>
#include <cstring>
#include <filesystem>
#include <fstream>
#include <string>
#include <vector>

#include <unistd.h>

#include "doctest/doctest.h"
#include "rag.h"

namespace fs = std::filesystem;

namespace {

struct TempFile {
  fs::path path;
  explicit TempFile(const std::string& name)
      : path(fs::temp_directory_path() / ("rag_abi_" + std::to_string(::getpid()) + "_" + name)) {}
  ~TempFile() { fs::remove(path); }
};

// Builds and writes the 3-chunk fixture used by the search tests in 2.4:
// query (1, 0, 0, 0) ranks 0, 1, 2; only chunk 2 says "lambda".
void write_fixture(const fs::path& path, std::uint32_t private_bits = 0x1) {
  char err[256] = "";
  rag_builder* b = rag_builder_new(4, err, sizeof err);
  REQUIRE_MESSAGE(b != nullptr, err);
  const float v0[] = {1, 0, 0, 0}, v1[] = {0.8f, 0.6f, 0, 0}, v2[] = {0.6f, 0.8f, 0, 0};
  CHECK(rag_builder_add(b, "go engine", v0, 0x1, err, sizeof err) == 0);
  CHECK(rag_builder_add(b, "serverless go", v1, 0x1, err, sizeof err) == 0);
  CHECK(rag_builder_add(b, "lambda functions", v2, private_bits, err, sizeof err) == 0);
  REQUIRE_MESSAGE(rag_builder_write(b, path.c_str(), err, sizeof err) == 0, err);
  rag_builder_free(b);
}

const float kQuery[] = {1, 0, 0, 0};

}  // namespace

TEST_CASE("build, write, load and search through the C ABI") {
  TempFile f("roundtrip");
  write_fixture(f.path);
  char err[256] = "";
  rag_index* idx = rag_index_load(f.path.c_str(), err, sizeof err);
  REQUIRE_MESSAGE(idx != nullptr, err);
  CHECK(rag_index_dim(idx) == 4);
  CHECK(rag_index_dim(nullptr) == 0);
  rag_hit out[10];
  const int n = rag_search(idx, kQuery, 4, "Lambda?", 0x1, 10, out);
  REQUIRE(n == 3);
  CHECK(out[0].chunk_id == 2);
  CHECK(out[0].score == doctest::Approx(1.0 / 63 + 1.0 / 61));
  CHECK(out[1].chunk_id == 0);
  CHECK(out[2].chunk_id == 1);
  rag_index_free(idx);
}

TEST_CASE("unnormalized input vectors are normalized by the builder") {
  TempFile f("normalize");
  char err[256] = "";
  rag_builder* b = rag_builder_new(2, err, sizeof err);
  const float big[] = {30, 40};
  REQUIRE(rag_builder_add(b, "x", big, 0x1, err, sizeof err) == 0);
  REQUIRE(rag_builder_write(b, f.path.c_str(), err, sizeof err) == 0);
  rag_builder_free(b);
  rag_index* idx = rag_index_load(f.path.c_str(), err, sizeof err);
  REQUIRE(idx != nullptr);
  // Single list, rank 1, whatever the raw scale: proves it loads and searches.
  const float q[] = {0.6f, 0.8f};
  rag_hit out[1];
  CHECK(rag_search(idx, q, 2, nullptr, 0x1, 1, out) == 1);
  rag_index_free(idx);
}

TEST_CASE("a public caller never receives a private chunk through the ABI") {
  TempFile f("private");
  write_fixture(f.path, 0x2);
  char err[256] = "";
  rag_index* idx = rag_index_load(f.path.c_str(), err, sizeof err);
  REQUIRE(idx != nullptr);
  rag_hit out[10];
  const int n = rag_search(idx, kQuery, 4, "lambda", 0x1, 10, out);
  REQUIRE(n == 2);
  for (int i = 0; i < n; ++i) CHECK(out[i].chunk_id != 2);
  CHECK(rag_search(idx, kQuery, 4, "lambda", 0x3, 10, out) == 3);
  rag_index_free(idx);
}

TEST_CASE("search rejects bad arguments with -1") {
  TempFile f("args");
  write_fixture(f.path);
  rag_index* idx = rag_index_load(f.path.c_str(), nullptr, 0);
  REQUIRE(idx != nullptr);
  rag_hit out[10];
  const float nan_q[] = {std::nanf(""), 0, 0, 0};
  const float inf_q[] = {INFINITY, 0, 0, 0};
  CHECK(rag_search(nullptr, kQuery, 4, "x", 0x1, 10, out) == -1);
  CHECK(rag_search(idx, nullptr, 4, "x", 0x1, 10, out) == -1);
  CHECK(rag_search(idx, kQuery, 4, "x", 0x1, 10, nullptr) == -1);
  CHECK(rag_search(idx, kQuery, 3, "x", 0x1, 10, out) == -1);  // wrong dim
  CHECK(rag_search(idx, nan_q, 4, "x", 0x1, 10, out) == -1);
  CHECK(rag_search(idx, inf_q, 4, "x", 0x1, 10, out) == -1);
  CHECK(rag_search(idx, kQuery, 4, "x", 0x1, 0, out) == 0);
  rag_index_free(idx);
}

TEST_CASE("builder rejects bad input with a message") {
  char err[256] = "";
  CHECK(rag_builder_new(0, err, sizeof err) == nullptr);
  CHECK(std::string(err).find("dim") != std::string::npos);

  rag_builder* b = rag_builder_new(2, err, sizeof err);
  REQUIRE(b != nullptr);
  const float zero[] = {0, 0}, ok[] = {1, 0}, nan_v[] = {std::nanf(""), 1};
  err[0] = '\0';
  CHECK(rag_builder_add(b, "t", zero, 0x1, err, sizeof err) == -1);
  CHECK(std::string(err).find("vector") != std::string::npos);
  CHECK(rag_builder_add(b, "t", nan_v, 0x1, err, sizeof err) == -1);
  CHECK(rag_builder_add(b, "t", ok, 0, err, sizeof err) == -1);
  CHECK(std::string(err).find("access") != std::string::npos);
  CHECK(rag_builder_add(b, nullptr, ok, 0x1, err, sizeof err) == -1);
  CHECK(rag_builder_add(b, "t", nullptr, 0x1, err, sizeof err) == -1);
  CHECK(rag_builder_add(nullptr, "t", ok, 0x1, err, sizeof err) == -1);
  CHECK(rag_builder_write(b, "/nonexistent-dir/x.idx", err, sizeof err) == -1);
  CHECK_FALSE(std::string(err).empty());
  rag_builder_free(b);
  rag_builder_free(nullptr);
  rag_index_free(nullptr);
}

TEST_CASE("loading a missing, wrong-version or truncated file returns NULL with a message") {
  char err[256] = "";
  CHECK(rag_index_load("/nonexistent-dir/x.idx", err, sizeof err) == nullptr);
  CHECK_FALSE(std::string(err).empty());
  CHECK(rag_index_load(nullptr, err, sizeof err) == nullptr);

  TempFile f("trunc");
  write_fixture(f.path);
  const auto size = fs::file_size(f.path);
  fs::resize_file(f.path, size - 1);
  err[0] = '\0';
  CHECK(rag_index_load(f.path.c_str(), err, sizeof err) == nullptr);
  CHECK_FALSE(std::string(err).empty());

  write_fixture(f.path);
  {
    std::fstream io(f.path, std::ios::in | std::ios::out | std::ios::binary);
    io.seekp(8);
    const char v[4] = {7, 0, 0, 0};
    io.write(v, 4);
  }
  CHECK(rag_index_load(f.path.c_str(), err, sizeof err) == nullptr);
  CHECK(std::string(err).find("version 7") != std::string::npos);
}

TEST_CASE("a short error buffer is truncated and still NUL-terminated") {
  char err[8];
  std::memset(err, 'X', sizeof err);
  CHECK(rag_index_load("/nonexistent-dir/x.idx", err, sizeof err) == nullptr);
  REQUIRE(std::memchr(err, '\0', sizeof err) != nullptr);  // terminated inside the buffer
  CHECK(std::strlen(err) == 7);
  CHECK(rag_builder_new(0, err, 0) == nullptr);  // err_len 0: nothing written, no crash
}
