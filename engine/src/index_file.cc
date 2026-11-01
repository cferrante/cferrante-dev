#include "index_file.h"

#include <array>
#include <bit>
#include <cmath>
#include <cstring>

namespace rag {

// Both supported Lambda architectures (x86_64, arm64) are little-endian, so
// values are copied as-is; this refuses to build anywhere they wouldn't be.
static_assert(std::endian::native == std::endian::little, "index file format is little-endian");

namespace {

constexpr char kMagic[8] = {'R', 'A', 'G', 'I', 'N', 'D', 'E', 'X'};

constexpr std::array<std::uint32_t, 256> make_crc_table() {
  std::array<std::uint32_t, 256> t{};
  for (std::uint32_t i = 0; i < 256; ++i) {
    std::uint32_t c = i;
    for (int k = 0; k < 8; ++k) c = (c & 1) ? 0xEDB88320u ^ (c >> 1) : c >> 1;
    t[i] = c;
  }
  return t;
}
constexpr auto kCrcTable = make_crc_table();

template <typename T>
void put(std::vector<std::uint8_t>& b, T v) {
  std::uint8_t raw[sizeof(T)];
  std::memcpy(raw, &v, sizeof(T));
  b.insert(b.end(), raw, raw + sizeof(T));
}

// Bounds-checked cursor: every read says whether the bytes were there.
class Reader {
 public:
  explicit Reader(std::span<const std::uint8_t> b) : b_(b) {}
  std::size_t left() const { return b_.size() - pos_; }
  template <typename T>
  bool get(T& v) {
    if (left() < sizeof(T)) return false;
    std::memcpy(&v, b_.data() + pos_, sizeof(T));
    pos_ += sizeof(T);
    return true;
  }
  bool take(std::size_t n, std::span<const std::uint8_t>& out) {
    if (left() < n) return false;
    out = b_.subspan(pos_, n);
    pos_ += n;
    return true;
  }

 private:
  std::span<const std::uint8_t> b_;
  std::size_t pos_ = 0;
};

bool fail(std::string& err, std::string msg) {
  err = std::move(msg);
  return false;
}

}  // namespace

std::uint32_t crc32(std::span<const std::uint8_t> data) noexcept {
  std::uint32_t c = 0xFFFFFFFFu;
  for (auto byte : data) c = kCrcTable[(c ^ byte) & 0xFFu] ^ (c >> 8);
  return c ^ 0xFFFFFFFFu;
}

std::vector<std::uint8_t> serialize(const Index& index) {
  const auto count = static_cast<std::uint32_t>(index.access.size());
  std::vector<std::uint8_t> payload;
  for (float f : index.vectors) put(payload, f);
  for (auto a : index.access) put(payload, a);
  for (auto l : index.bm25.doc_len) put(payload, l);
  for (const auto& [term, list] : index.bm25.postings) {
    put(payload, static_cast<std::uint32_t>(term.size()));
    payload.insert(payload.end(), term.begin(), term.end());
    put(payload, static_cast<std::uint32_t>(list.size()));
    for (const auto& p : list) {
      put(payload, p.doc);
      put(payload, p.tf);
    }
  }

  std::vector<std::uint8_t> out(kMagic, kMagic + 8);
  put(out, kIndexVersion);
  put(out, index.dim);
  put(out, count);
  put(out, static_cast<std::uint32_t>(index.bm25.postings.size()));
  put(out, static_cast<std::uint64_t>(payload.size()));
  put(out, crc32(payload));
  put(out, std::uint32_t{0});
  out.insert(out.end(), payload.begin(), payload.end());
  return out;
}

bool deserialize(std::span<const std::uint8_t> bytes, Index& out, std::string& err) {
  if (bytes.size() < kHeaderSize) {
    return fail(err, "truncated: " + std::to_string(bytes.size()) + " bytes, header needs " +
                         std::to_string(kHeaderSize));
  }
  if (std::memcmp(bytes.data(), kMagic, 8) != 0) return fail(err, "bad magic: not a rag index");

  Reader h(bytes.subspan(8, kHeaderSize - 8));
  std::uint32_t version = 0, dim = 0, count = 0, term_count = 0, crc = 0, reserved = 0;
  std::uint64_t payload_len = 0;
  h.get(version);
  h.get(dim);
  h.get(count);
  h.get(term_count);
  h.get(payload_len);
  h.get(crc);
  h.get(reserved);
  if (version != kIndexVersion) {
    return fail(err, "unsupported version " + std::to_string(version) + " (this engine reads " +
                         std::to_string(kIndexVersion) + ")");
  }
  if (reserved != 0) return fail(err, "corrupt header: reserved field is not zero");
  const auto payload = bytes.subspan(kHeaderSize);
  if (payload_len != payload.size()) {
    return fail(err, "truncated or padded: header says " + std::to_string(payload_len) +
                         " payload bytes, file has " + std::to_string(payload.size()));
  }
  if (crc32(payload) != crc) return fail(err, "checksum mismatch: file is corrupt");
  if (dim == 0) return fail(err, "corrupt header: dim is 0");

  Reader r(payload);
  // count and dim are u32, so count * dim fits in u64; compare by division so
  // the byte count can't overflow either.
  const std::uint64_t floats = std::uint64_t{count} * dim;
  if (floats > r.left() / sizeof(float)) return fail(err, "corrupt: vectors exceed the file");

  Index idx;
  idx.dim = dim;
  idx.vectors.resize(floats);
  for (auto& f : idx.vectors) {
    r.get(f);
    if (!std::isfinite(f)) return fail(err, "corrupt: a vector value is not finite");
  }
  if (count > r.left() / 8) return fail(err, "corrupt: chunk table exceeds the file");
  idx.access.resize(count);
  for (auto& a : idx.access) r.get(a);
  idx.bm25.doc_len.resize(count);
  for (auto& l : idx.bm25.doc_len) r.get(l);

  std::vector<std::uint64_t> tf_sum(count, 0);
  const std::string* prev = nullptr;
  for (std::uint32_t t = 0; t < term_count; ++t) {
    std::uint32_t len = 0, n = 0;
    std::span<const std::uint8_t> raw;
    if (!r.get(len) || len == 0 || !r.take(len, raw)) return fail(err, "corrupt: bad term");
    std::string term(raw.begin(), raw.end());
    if (prev != nullptr && !(*prev < term)) return fail(err, "corrupt: terms out of order");
    if (!r.get(n) || n == 0 || n > count || n > r.left() / 8) {
      return fail(err, "corrupt: bad postings count");
    }
    std::vector<Posting> list(n);
    for (std::uint32_t i = 0; i < n; ++i) {
      r.get(list[i].doc);
      r.get(list[i].tf);
      if (list[i].doc >= count || list[i].tf == 0 || (i > 0 && list[i].doc <= list[i - 1].doc)) {
        return fail(err, "corrupt: bad posting");
      }
      tf_sum[list[i].doc] += list[i].tf;
    }
    auto it = idx.bm25.postings.emplace_hint(idx.bm25.postings.end(), std::move(term),
                                             std::move(list));
    prev = &it->first;
  }
  if (r.left() != 0) return fail(err, "corrupt: trailing bytes after the last term");
  for (std::uint32_t d = 0; d < count; ++d) {
    if (tf_sum[d] != idx.bm25.doc_len[d]) {
      return fail(err, "corrupt: chunk " + std::to_string(d) +
                           " length doesn't match its postings");
    }
  }
  out = std::move(idx);
  return true;
}

}  // namespace rag
