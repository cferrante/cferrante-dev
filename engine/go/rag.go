// Package rag is the Go binding for the C++ retrieval engine (design §5).
// The directory is engine/go; "go" is a keyword, so the package is rag.
//
// cgo compiles the engine from source (engine.cc), so `go build` and
// `go test` need no separate CMake step.
package rag

/*
#cgo CFLAGS: -I${SRCDIR}/../include
#cgo CXXFLAGS: -std=c++20 -O3 -I${SRCDIR}/../include -I${SRCDIR}/../src
#include <stdlib.h>
#include "rag.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"unsafe"
)

// ErrClosed is returned when an Index is used after Close.
var ErrClosed = errors.New("rag: index is closed")

const errLen = 256

// errBuf is a caller-allocated buffer for the engine's error messages.
type errBuf [errLen]C.char

func (e *errBuf) ptr() *C.char { return &e[0] }
func (e *errBuf) err(op string) error {
	return fmt.Errorf("rag: %s: %s", op, C.GoString(&e[0]))
}

// cPath converts a file path for C. C strings end at the first NUL, so a path
// containing one would silently name a different file; reject it instead.
func cPath(op, path string) (*C.char, error) {
	if strings.IndexByte(path, 0) >= 0 {
		return nil, fmt.Errorf("rag: %s: path contains a NUL byte", op)
	}
	return C.CString(path), nil
}

// Hit is one search result.
type Hit struct {
	ChunkID uint32
	Score   float32
}

// Index is a loaded, read-only index. Search is safe for concurrent use.
// Close takes the write lock, so it waits for searches in flight and later
// searches get ErrClosed instead of touching freed memory.
type Index struct {
	mu    sync.RWMutex
	p     *C.rag_index
	dim   int
	count int // chunks: no search returns more hits
}

// Load reads and validates an index file. Call Close when done.
func Load(path string) (*Index, error) {
	cpath, err := cPath("load", path)
	if err != nil {
		return nil, err
	}
	defer C.free(unsafe.Pointer(cpath))
	var e errBuf
	p := C.rag_index_load(cpath, e.ptr(), errLen)
	if p == nil {
		return nil, e.err("load")
	}
	return &Index{p: p, dim: int(C.rag_index_dim(p)), count: int(C.rag_index_count(p))}, nil
}

// Dim is the vector length the index expects.
func (ix *Index) Dim() int { return ix.dim }

// Search returns up to k hits for chunks whose access bits intersect mask,
// best first. text may be empty (vector search only); an all-zero vec means
// keyword search only. vec must have Dim() finite values. A NUL in text is
// treated as a space, which is how the tokenizer would split it anyway.
func (ix *Index) Search(vec []float32, text string, mask uint32, k int) ([]Hit, error) {
	if len(vec) != ix.dim {
		return nil, fmt.Errorf("rag: search: vector has %d values, index wants %d", len(vec), ix.dim)
	}
	// Size the output by what the index can return, not by the caller's k.
	k = min(k, ix.count)
	if k <= 0 {
		return nil, nil
	}
	var ctext *C.char
	if text != "" {
		ctext = C.CString(strings.ReplaceAll(text, "\x00", " "))
		defer C.free(unsafe.Pointer(ctext))
	}
	out := make([]C.rag_hit, k)

	ix.mu.RLock()
	defer ix.mu.RUnlock()
	if ix.p == nil {
		return nil, ErrClosed
	}
	// Go memory passed for the duration of the call only, and it holds no Go
	// pointers, so this follows the cgo pointer rules.
	n := C.rag_search(ix.p, (*C.float)(unsafe.Pointer(&vec[0])), C.uint32_t(len(vec)), ctext,
		C.uint32_t(mask), C.uint32_t(k), &out[0])
	if n < 0 {
		// The length is checked above, so this is a NaN or infinite value in vec
		// (design §5) or, rarely, an internal error the engine caught.
		return nil, errors.New("rag: search: engine rejected the query: non-finite vector value or internal error")
	}
	hits := make([]Hit, n)
	for i := range hits {
		hits[i] = Hit{ChunkID: uint32(out[i].chunk_id), Score: float32(out[i].score)}
	}
	return hits, nil
}

// Close frees the index. It is safe to call more than once.
func (ix *Index) Close() error {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if ix.p != nil {
		C.rag_index_free(ix.p)
		ix.p = nil
	}
	return nil
}

// Builder accumulates chunks and writes an index file. Chunk ids are assigned
// in Add order from 0. Not safe for concurrent use.
type Builder struct {
	p   *C.rag_builder
	dim int
}

// NewBuilder starts an index of dim-length vectors. Call Close when done.
func NewBuilder(dim int) (*Builder, error) {
	if dim <= 0 || dim > math.MaxUint32 {
		return nil, fmt.Errorf("rag: new builder: dim %d out of range", dim)
	}
	var e errBuf
	p := C.rag_builder_new(C.uint32_t(dim), e.ptr(), errLen)
	if p == nil {
		return nil, e.err("new builder")
	}
	return &Builder{p: p, dim: dim}, nil
}

// Add appends a chunk. vec is copied and normalized by the engine; it must have
// dim finite values, not all zero. access must have at least one bit set.
func (b *Builder) Add(text string, vec []float32, access uint32) error {
	if len(vec) != b.dim {
		return fmt.Errorf("rag: add: vector has %d values, builder wants %d", len(vec), b.dim)
	}
	if strings.IndexByte(text, 0) >= 0 {
		// C.CString would silently cut the text at the NUL.
		return errors.New("rag: add: text contains a NUL byte")
	}
	ctext := C.CString(text)
	defer C.free(unsafe.Pointer(ctext))
	var e errBuf
	if C.rag_builder_add(b.p, ctext, (*C.float)(unsafe.Pointer(&vec[0])), C.uint32_t(access),
		e.ptr(), errLen) != 0 {
		return e.err("add")
	}
	return nil
}

// Write writes the index file to path.
func (b *Builder) Write(path string) error {
	cpath, err := cPath("write", path)
	if err != nil {
		return err
	}
	defer C.free(unsafe.Pointer(cpath))
	var e errBuf
	if C.rag_builder_write(b.p, cpath, e.ptr(), errLen) != 0 {
		return e.err("write")
	}
	return nil
}

// Close frees the builder. It is safe to call more than once.
func (b *Builder) Close() {
	if b.p != nil {
		C.rag_builder_free(b.p)
		b.p = nil
	}
}
