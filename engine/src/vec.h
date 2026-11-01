// Vector maths for the retrieval engine.
#pragma once

#include <span>

namespace rag {

// Scales v to unit length in place. Returns false, leaving v unchanged, when v
// has no direction (all zeros) or contains a non-finite value.
bool normalize(std::span<float> v) noexcept;

// Dot product of two equal-length vectors. For unit vectors this is the cosine
// similarity. Precondition: a.size() == b.size().
float dot(std::span<const float> a, std::span<const float> b) noexcept;

}  // namespace rag
