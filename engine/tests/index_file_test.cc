#include "index_file.h"

#include <cmath>
#include <cstring>
#include <string>
#include <vector>

#include "doctest/doctest.h"
#include "tokenize.h"
#include "vec.h"

namespace {

rag::Index fixture() {
  rag::Index idx;
  idx.dim = 4;
  idx.vectors = {1, 0, 0, 0, 0.5f, 0.5f, 0.5f, 0.5f, 0, 0, 0.6f, 0.8f};
  idx.access = {0x1, 0x2, 0x1};
  idx.bm25 = rag::bm25_build({rag::tokenize("Go engine, fast."),
                              rag::tokenize("private notes — engine"),
                              rag::tokenize("Lambda lambda serverless")});
  return idx;
}

void put_u32(std::vector<std::uint8_t>& b, std::size_t at, std::uint32_t v) {
  std::memcpy(b.data() + at, &v, 4);
}

void fix_crc(std::vector<std::uint8_t>& b) {
  put_u32(b, 32, rag::crc32(std::span(b).subspan(rag::kHeaderSize)));
}

}  // namespace

TEST_CASE("crc32 matches the standard check value") {
  const std::string s = "123456789";
  CHECK(rag::crc32(std::span(reinterpret_cast<const std::uint8_t*>(s.data()), s.size())) ==
        0xCBF43926u);
}

TEST_CASE("round trip is exact, byte for byte and field by field") {
  const auto idx = fixture();
  const auto bytes = rag::serialize(idx);
  REQUIRE(bytes.size() > rag::kHeaderSize);
  CHECK(std::memcmp(bytes.data(), "RAGINDEX", 8) == 0);

  rag::Index back;
  std::string err;
  REQUIRE_MESSAGE(rag::deserialize(bytes, back, err), err);
  CHECK(back.dim == idx.dim);
  CHECK(back.vectors == idx.vectors);
  CHECK(back.access == idx.access);
  CHECK(back.bm25.doc_len == idx.bm25.doc_len);
  REQUIRE(back.bm25.postings.size() == idx.bm25.postings.size());
  for (const auto& [term, list] : idx.bm25.postings) {
    REQUIRE(back.bm25.postings.count(term) == 1);
    const auto& got = back.bm25.postings.at(term);
    REQUIRE(got.size() == list.size());
    for (std::size_t i = 0; i < list.size(); ++i) {
      CHECK(got[i].doc == list[i].doc);
      CHECK(got[i].tf == list[i].tf);
    }
  }
  CHECK(rag::serialize(back) == bytes);
}

TEST_CASE("an empty index round-trips") {
  rag::Index idx;
  idx.dim = 8;
  rag::Index back;
  std::string err;
  REQUIRE_MESSAGE(rag::deserialize(rag::serialize(idx), back, err), err);
  CHECK(back.dim == 8);
  CHECK(back.access.empty());
}

TEST_CASE("a wrong magic or version is rejected with a clear message") {
  auto bytes = rag::serialize(fixture());
  REQUIRE(bytes.size() > rag::kHeaderSize);
  rag::Index out;
  std::string err;
  auto bad_magic = bytes;
  bad_magic[0] = 'X';
  CHECK_FALSE(rag::deserialize(bad_magic, out, err));
  CHECK(err.find("magic") != std::string::npos);

  auto v2 = bytes;
  put_u32(v2, 8, 2);
  CHECK_FALSE(rag::deserialize(v2, out, err));
  CHECK(err.find("version 2") != std::string::npos);
}

TEST_CASE("every truncation is rejected") {
  const auto bytes = rag::serialize(fixture());
  REQUIRE(bytes.size() > rag::kHeaderSize);
  for (std::size_t len = 0; len < bytes.size(); ++len) {
    rag::Index out;
    std::string err;
    CAPTURE(len);
    CHECK_FALSE(rag::deserialize(std::span(bytes).first(len), out, err));
    CHECK_FALSE(err.empty());
  }
}

TEST_CASE("trailing bytes are rejected") {
  auto bytes = rag::serialize(fixture());
  REQUIRE(bytes.size() > rag::kHeaderSize);
  bytes.push_back(0);
  rag::Index out;
  std::string err;
  CHECK_FALSE(rag::deserialize(bytes, out, err));  // header length no longer matches

  // Header length and checksum updated to match: only the parser can notice.
  std::uint64_t len = 0;
  std::memcpy(&len, bytes.data() + 24, 8);
  ++len;
  std::memcpy(bytes.data() + 24, &len, 8);
  fix_crc(bytes);
  CHECK_FALSE(rag::deserialize(bytes, out, err));
  CHECK(err.find("trailing") != std::string::npos);
}

TEST_CASE("every single flipped bit is rejected") {
  const auto bytes = rag::serialize(fixture());
  REQUIRE(bytes.size() > rag::kHeaderSize);
  for (std::size_t i = 0; i < bytes.size(); ++i) {
    for (int bit = 0; bit < 8; ++bit) {
      auto bad = bytes;
      bad[i] ^= static_cast<std::uint8_t>(1u << bit);
      rag::Index out;
      std::string err;
      CAPTURE(i);
      CAPTURE(bit);
      CHECK_FALSE(rag::deserialize(bad, out, err));
    }
  }
}

TEST_CASE("flipped bits behind a recomputed checksum never crash the loader or a search") {
  // The CRC stops accidental damage; this exercises the field checks behind it,
  // as a deliberately crafted file would. ASan/UBSan fail the test on any bad
  // read. A file that still parses must be safe to search.
  const auto bytes = rag::serialize(fixture());
  REQUIRE(bytes.size() > rag::kHeaderSize);
  const std::vector<float> q{1, 0, 0, 0};
  int accepted = 0;
  for (std::size_t i = rag::kHeaderSize; i < bytes.size(); ++i) {
    for (int bit = 0; bit < 8; ++bit) {
      auto bad = bytes;
      bad[i] ^= static_cast<std::uint8_t>(1u << bit);
      fix_crc(bad);
      rag::Index out;
      std::string err;
      if (rag::deserialize(bad, out, err)) {
        ++accepted;
        (void)rag::hybrid_search(out, q, "engine lambda", 0x3, 10);
      } else {
        CHECK_FALSE(err.empty());
      }
    }
  }
  MESSAGE("accepted after checksum fix-up: " << accepted);
}

TEST_CASE("non-finite vectors, out-of-range postings and inconsistent lengths are rejected") {
  rag::Index out;
  std::string err;
  const auto good = rag::serialize(fixture());
  REQUIRE(good.size() > rag::kHeaderSize);
  const std::size_t vec0 = rag::kHeaderSize;  // first float of the first vector

  auto nan = good;
  const float qnan = std::nanf("");
  std::memcpy(nan.data() + vec0, &qnan, 4);
  fix_crc(nan);
  CHECK_FALSE(rag::deserialize(nan, out, err));
  CHECK(err.find("finite") != std::string::npos);

  // doc_len of chunk 0 sits after 12 floats and 3 access words.
  auto len = good;
  put_u32(len, rag::kHeaderSize + 12 * 4 + 3 * 4, 99);
  fix_crc(len);
  CHECK_FALSE(rag::deserialize(len, out, err));
  CHECK(err.find("length") != std::string::npos);
}
