// The one tokenizer: used on chunk text at index build and on the query at
// search time, so the two can never disagree (design §5).
#pragma once

#include <string>
#include <string_view>
#include <vector>

namespace rag {

// Splits text into tokens. ASCII letters are lowercased; any other ASCII byte
// that isn't a letter or digit separates tokens, and so do the Unicode dashes
// U+2010..U+2015 and U+2212. Every other non-ASCII byte stays inside its token
// unchanged (no Unicode case folding). Never reads past the end of text, even
// when it ends partway through a UTF-8 sequence.
std::vector<std::string> tokenize(std::string_view text);

}  // namespace rag
