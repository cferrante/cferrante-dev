// Unity build of the engine for cgo. cgo compiles only the C/C++ files in this
// package's directory, so this one file includes the engine sources; quoted
// includes inside them resolve relative to engine/src. CMake builds the same
// sources for the C++ tests and benchmarks. Keep this list in step with
// engine/CMakeLists.txt; a missing file fails the link with an undefined symbol.
#include "../src/bm25.cc"
#include "../src/index_file.cc"
#include "../src/rag.cc"
#include "../src/rrf.cc"
#include "../src/search.cc"
#include "../src/tokenize.cc"
#include "../src/topk.cc"
#include "../src/vec.cc"
