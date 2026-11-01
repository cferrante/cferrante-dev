#include "tokenize.h"

#include <string>
#include <memory>
#include <vector>

#include "doctest/doctest.h"

using Tokens = std::vector<std::string>;

TEST_CASE("lowercases ASCII and splits on punctuation and spaces") {
  CHECK(rag::tokenize("Hello, World!") == Tokens{"hello", "world"});
  CHECK(rag::tokenize("  Go\tengine\n") == Tokens{"go", "engine"});
}

TEST_CASE("digits stay in tokens; other ASCII symbols split") {
  CHECK(rag::tokenize("C++ and Go1.27") == Tokens{"c", "and", "go1", "27"});
  CHECK(rag::tokenize("multi-agent_loop") == Tokens{"multi", "agent", "loop"});
}

TEST_CASE("Unicode dashes split tokens like an ASCII hyphen") {
  CHECK(rag::tokenize("multi‑agent") == Tokens{"multi", "agent"});  // non-breaking hyphen
  CHECK(rag::tokenize("a‐b‒c–d—e―f") ==
        Tokens{"a", "b", "c", "d", "e", "f"});  // hyphen, figure, en, em, bar
  CHECK(rag::tokenize("x−y") == Tokens{"x", "y"});  // minus sign
}

TEST_CASE("neighbours of the dash range are not separators") {
  CHECK(rag::tokenize("a‖b") == Tokens{"a‖b"});  // double vertical line
  CHECK(rag::tokenize("a\xE2\x80\x8F" "b") == Tokens{"a\xE2\x80\x8F" "b"});  // U+200F RLM, as bytes
}

TEST_CASE("other non-ASCII bytes are kept unchanged, with no Unicode case folding") {
  CHECK(rag::tokenize("Café") == Tokens{"café"});
  CHECK(rag::tokenize("CAFÉ") == Tokens{"cafÉ"});
}

TEST_CASE("text with no letters or digits gives no tokens") {
  CHECK(rag::tokenize("").empty());
  CHECK(rag::tokenize(" ,.!? — ").empty());
}

TEST_CASE("a UTF-8 sequence cut off at the end is kept, not over-read") {
  // A lone E2 80 could be the start of a dash; there's no third byte to check.
  CHECK(rag::tokenize(std::string_view("ab\xE2\x80", 4)) == Tokens{"ab\xE2\x80"});
  CHECK(rag::tokenize(std::string_view("\xE2", 1)) == Tokens{"\xE2"});
}

TEST_CASE("Unicode spaces split tokens like an ASCII space") {
  // Written as bytes: U+00A0 no-break, U+2002 en, U+2009 thin, U+200A hair,
  // U+202F narrow no-break, U+205F math, U+3000 ideographic space.
  CHECK(rag::tokenize("Go\xC2\xA0" "engine") == Tokens{"go", "engine"});
  CHECK(rag::tokenize("a\xE2\x80\x82" "b\xE2\x80\x89" "c\xE2\x80\x8A" "d") ==
        Tokens{"a", "b", "c", "d"});
  CHECK(rag::tokenize("x\xE2\x80\xAF" "y\xE2\x81\x9F" "z\xE3\x80\x80" "w") ==
        Tokens{"x", "y", "z", "w"});
}

TEST_CASE("characters next to the Unicode spaces are not separators") {
  CHECK(rag::tokenize("a\xC2\xA1" "b") == Tokens{"a\xC2\xA1" "b"});          // U+00A1 inverted !
  CHECK(rag::tokenize("a\xE2\x80\x8B" "b") == Tokens{"a\xE2\x80\x8B" "b"});  // U+200B zero-width space
  CHECK(rag::tokenize("a\xE3\x80\x81" "b") == Tokens{"a\xE3\x80\x81" "b"});  // U+3001 ideographic comma
}

TEST_CASE("a lone first byte of a 2-byte space at the very end is not over-read") {
  // Exactly-sized heap buffer: reading past it is an ASan error, unlike a
  // string literal, whose terminating NUL would hide the over-read.
  auto buf = std::make_unique<char[]>(3);
  buf[0] = 'a';
  buf[1] = 'b';
  buf[2] = '\xC2';
  CHECK(rag::tokenize(std::string_view(buf.get(), 3)) == Tokens{"ab\xC2"});
}
