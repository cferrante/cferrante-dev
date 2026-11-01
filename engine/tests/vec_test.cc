#include "vec.h"

#include <cmath>
#include <limits>
#include <vector>

#include "doctest/doctest.h"

using doctest::Approx;

TEST_CASE("normalize scales a 3-4-5 vector to unit length") {
  std::vector<float> v{3.0f, 4.0f};
  CHECK(rag::normalize(v));
  CHECK(v[0] == Approx(0.6f));
  CHECK(v[1] == Approx(0.8f));
}

TEST_CASE("normalize leaves an already-unit vector unchanged") {
  std::vector<float> v{0.0f, 1.0f, 0.0f};
  CHECK(rag::normalize(v));
  CHECK(v == std::vector<float>{0.0f, 1.0f, 0.0f});
}

TEST_CASE("normalize rejects a zero vector and leaves it unchanged") {
  std::vector<float> v{0.0f, 0.0f, 0.0f};
  CHECK_FALSE(rag::normalize(v));
  CHECK(v == std::vector<float>{0.0f, 0.0f, 0.0f});
}

TEST_CASE("normalize rejects non-finite values and leaves them unchanged") {
  const float inf = std::numeric_limits<float>::infinity();
  std::vector<float> v{1.0f, inf};
  CHECK_FALSE(rag::normalize(v));
  CHECK(v[0] == 1.0f);
  std::vector<float> n{1.0f, std::nanf("")};
  CHECK_FALSE(rag::normalize(n));
  CHECK(n[0] == 1.0f);
}

TEST_CASE("dot of known vectors") {
  const std::vector<float> a{1.0f, 2.0f, 3.0f};
  const std::vector<float> b{4.0f, -5.0f, 6.0f};
  CHECK(rag::dot(a, b) == Approx(12.0f));  // 4 - 10 + 18
}

TEST_CASE("dot with a zero vector is zero") {
  const std::vector<float> a{1.0f, 2.0f, 3.0f};
  const std::vector<float> z{0.0f, 0.0f, 0.0f};
  CHECK(rag::dot(a, z) == 0.0f);
}

TEST_CASE("dot of a unit vector with itself is one") {
  std::vector<float> v{1.0f, 2.0f, 2.0f};  // length 3
  REQUIRE(rag::normalize(v));
  CHECK(rag::dot(v, v) == Approx(1.0f));
}

TEST_CASE("dot handles lengths that are not a multiple of the unroll width") {
  // 19 elements: catches a vectorised loop that drops or double-counts the tail.
  std::vector<float> a(19), b(19);
  float expected = 0.0f;
  for (int i = 0; i < 19; ++i) {
    a[i] = static_cast<float>(i + 1);
    b[i] = 1.0f;
    expected += static_cast<float>(i + 1);
  }
  CHECK(rag::dot(a, b) == Approx(expected));  // 190
}
