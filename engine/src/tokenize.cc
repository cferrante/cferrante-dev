#include "tokenize.h"

namespace rag {

namespace {

// Byte length of the separator that starts at text[i], or 0 if there is none:
// the dashes U+2010..U+2015 and U+2212, and the Unicode spaces U+00A0,
// U+2000..U+200A, U+202F, U+205F and U+3000. A sequence cut off by the end of
// text is not a separator.
std::size_t separator_at(std::string_view text, std::size_t i) {
  const auto at = [&](std::size_t j) { return static_cast<unsigned char>(text[j]); };
  const auto c = at(i);
  if (c == 0xC2) {
    return i + 1 < text.size() && at(i + 1) == 0xA0 ? 2 : 0;
  }
  if ((c != 0xE2 && c != 0xE3) || i + 2 >= text.size()) return 0;
  const auto b1 = at(i + 1);
  const auto b2 = at(i + 2);
  const bool sep =
      (c == 0xE2 && b1 == 0x80 && b2 >= 0x80 && b2 <= 0x8A) ||  // U+2000..U+200A spaces
      (c == 0xE2 && b1 == 0x80 && b2 >= 0x90 && b2 <= 0x95) ||  // U+2010..U+2015 dashes
      (c == 0xE2 && b1 == 0x80 && b2 == 0xAF) ||                // U+202F narrow no-break space
      (c == 0xE2 && b1 == 0x81 && b2 == 0x9F) ||                // U+205F medium math space
      (c == 0xE2 && b1 == 0x88 && b2 == 0x92) ||                // U+2212 minus sign
      (c == 0xE3 && b1 == 0x80 && b2 == 0x80);                  // U+3000 ideographic space
  return sep ? 3 : 0;
}

}  // namespace

std::vector<std::string> tokenize(std::string_view text) {
  std::vector<std::string> tokens;
  std::string cur;
  auto flush = [&] {
    if (!cur.empty()) tokens.push_back(std::move(cur));
    cur.clear();
  };
  for (std::size_t i = 0; i < text.size(); ++i) {
    const auto c = static_cast<unsigned char>(text[i]);
    if (c >= 'A' && c <= 'Z') {
      cur.push_back(static_cast<char>(c - 'A' + 'a'));
    } else if ((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
      cur.push_back(static_cast<char>(c));
    } else if (c < 0x80) {
      flush();  // ASCII punctuation, space or control
    } else if (const auto n = separator_at(text, i); n > 0) {
      flush();
      i += n - 1;  // skip the rest of the separator
    } else {
      cur.push_back(static_cast<char>(c));  // other non-ASCII byte: kept as is
    }
  }
  flush();
  return tokens;
}

}  // namespace rag
