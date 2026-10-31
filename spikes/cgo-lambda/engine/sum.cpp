#include "sum.h"

#include <numeric>
#include <vector>

// Copies into a std::vector on purpose: the library then really depends on
// libstdc++ (allocation, exceptions), as the real engine will.
int64_t engine_sum(const int64_t *xs, size_t n) {
  // A C++ exception must never cross into Go; cgo can't unwind it.
  try {
    std::vector<int64_t> v(xs, xs + n);
    return std::accumulate(v.begin(), v.end(), int64_t{0});
  } catch (...) {
    return 0;
  }
}
