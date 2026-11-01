// The versioned index file format (design §5).
//
// Little-endian. A 40-byte header, then the payload:
//   offset  0  char[8] magic "RAGINDEX"
//           8  u32 version (1)
//          12  u32 dim
//          16  u32 count           chunks
//          20  u32 term_count
//          24  u64 payload_len     bytes after the header
//          32  u32 crc32           of the payload (IEEE, as zlib)
//          36  u32 reserved (0)
//   payload: f32 vectors[count * dim], u32 access[count], u32 doc_len[count],
//            then term_count terms in increasing byte order, each
//            u32 len, len bytes, u32 n, n x (u32 doc, u32 tf) in increasing doc order.
// Section sizes follow from the counts, so there are no offsets to trust.
#pragma once

#include <cstdint>
#include <span>
#include <string>
#include <string_view>
#include <vector>

#include "search.h"

namespace rag {

inline constexpr std::uint32_t kIndexVersion = 1;
inline constexpr std::size_t kHeaderSize = 40;

std::uint32_t crc32(std::span<const std::uint8_t> data) noexcept;

std::vector<std::uint8_t> serialize(const Index& index);

// Parses and fully validates bytes. On failure returns false with a message in
// err and leaves out unspecified. Never reads outside bytes.
bool deserialize(std::span<const std::uint8_t> bytes, Index& out, std::string& err);

}  // namespace rag
