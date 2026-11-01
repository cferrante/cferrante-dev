#include "tokenize.h"

#include <string>
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
