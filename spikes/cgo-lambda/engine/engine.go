// Package engine calls a small C++ library through cgo. It stands in for the
// retrieval engine so spike 1.2 can test building cgo binaries for Lambda.
package engine

/*
#cgo CXXFLAGS: -std=c++17 -O2
#include "sum.h"
*/
import "C"

import "unsafe"

// Sum adds xs in C++.
func Sum(xs []int64) int64 {
	if len(xs) == 0 {
		return 0
	}
	// Safe to pass: the slice holds no Go pointers, and C doesn't keep it.
	return int64(C.engine_sum((*C.int64_t)(unsafe.Pointer(&xs[0])), C.size_t(len(xs))))
}
