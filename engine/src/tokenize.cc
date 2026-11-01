#include "tokenize.h"

namespace rag {

namespace {

// True when text[i..i+2] is one of the dashes that separate tokens:
// U+2010..U+2015 (E2 80 90..95) or U+2212 minus (E2 88 92).
bool dash_at(std::string_view text, std::size_t i) {
  if (i + 2 >= text.size() || static_cast<unsigned char>(text[i]) != 0xE2) return false;
  const auto b1 = static_cast<unsigned char>(text[i + 1]);
  const auto b2 = static_cast<unsigned char>(text[i + 2]);
  return (b1 == 0x80 && b2 >= 0x90 && b2 <= 0x95) || (b1 == 0x88 && b2 == 0x92);
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
    } else if (dash_at(text, i)) {
      flush();
      i += 2;  // skip the rest of the dash
    } else {
      cur.push_back(static_cast<char>(c));  // other non-ASCII byte: kept as is
    }
  }
  flush();
  return tokens;
}

}  // namespace rag
