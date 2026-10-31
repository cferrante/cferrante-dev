// C interface to the spike's C++ code. cgo can only call C, so C++ is
// exposed through extern "C" functions with plain C types.
#ifndef ENGINE_SUM_H
#define ENGINE_SUM_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

// Returns the sum of xs[0..n), or 0 if n is 0.
int64_t engine_sum(const int64_t *xs, size_t n);

#ifdef __cplusplus
}
#endif

#endif
