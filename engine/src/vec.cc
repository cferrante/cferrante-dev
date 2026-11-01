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
  // Simplest form: one running sum. Each addition waits for the previous one,
  // and the compiler may not reorder float additions, so this does not
  // vectorise. Plan step 2.6 measures it before changing it.
  float sum = 0.0f;
  for (std::size_t i = 0; i < a.size(); ++i) sum += a[i] * b[i];
  return sum;
}

}  // namespace rag
