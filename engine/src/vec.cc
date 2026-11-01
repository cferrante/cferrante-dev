#include "vec.h"

#include <cmath>

namespace rag {

bool normalize(std::span<float> v) noexcept {
  // Accumulate in double: squaring large floats can overflow float, and it
  // keeps the length accurate for 512+ dimensions. Runs at index build, not
  // per query, so the cost doesn't matter.
  double sum = 0.0;
  for (float x : v) {
    if (!std::isfinite(x)) return false;
    sum += static_cast<double>(x) * x;
  }
  if (sum == 0.0) return false;
  const double inv = 1.0 / std::sqrt(sum);
  for (float& x : v) x = static_cast<float>(x * inv);
  return true;
}

float dot(std::span<const float> a, std::span<const float> b) noexcept {
  // Eight independent running sums. A single sum is a chain of additions the
  // compiler may not reorder (float addition isn't associative), so it can't
  // vectorise without -ffast-math. Lane-wise sums need no reordering, so GCC
  // vectorises this at -O3: SSE2 on x86_64, NEON on arm64, no intrinsics.
  // Measured in plan step 2.6: 3x faster than one sum; 16 lanes was slower.
  constexpr std::size_t kLanes = 8;
  float acc[kLanes] = {};
  const std::size_t n = a.size();
  std::size_t i = 0;
  for (; i + kLanes <= n; i += kLanes) {
    for (std::size_t j = 0; j < kLanes; ++j) acc[j] += a[i + j] * b[i + j];
  }
  float sum = 0.0f;
  for (float x : acc) sum += x;
  for (; i < n; ++i) sum += a[i] * b[i];  // tail when n isn't a multiple of kLanes
  return sum;
}

}  // namespace rag
