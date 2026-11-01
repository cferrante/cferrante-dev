# Retrieval engine (design §5). Builds the engine library and its doctest
# suite; the Go binding (engine/go) compiles the same sources through cgo.
cmake_minimum_required(VERSION 3.25)
project(rag LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

option(RAG_SANITIZE "Build with AddressSanitizer and UBSan" OFF)

add_compile_options(-Wall -Wextra -Wpedantic -Werror)
if(RAG_SANITIZE)
  # -fno-sanitize-recover makes any UB report fail the test run.
  add_compile_options(-fsanitize=address,undefined -fno-sanitize-recover=all -fno-omit-frame-pointer)
  add_link_options(-fsanitize=address,undefined)
endif()

add_library(rag STATIC
  src/vec.cc
  src/topk.cc
)
target_include_directories(rag PUBLIC include src)

add_executable(rag_tests
  tests/main.cc
  tests/vec_test.cc
  tests/topk_test.cc
)
target_link_libraries(rag_tests PRIVATE rag)
target_include_directories(rag_tests SYSTEM PRIVATE third_party)

enable_testing()
add_test(NAME rag_tests COMMAND rag_tests)
