// Copyright (C) 2020  Matthew Glazar
// See end of file for extended copyright information.

#include <cstddef>
#include <cstdint>
#include <gmock/gmock.h>
#include <gtest/gtest.h>
#include <quick-lint-js/have.h>
#include <quick-lint-js/monotonic-allocator.h>

// @@@ delete QLJS_HAVE_ALIGNED_NEW if needed

#if 0 // @@@
#define QLJS_HAVE_OVERALIGNED 1
#else
#define QLJS_HAVE_OVERALIGNED 0
#endif

namespace quick_lint_js {
namespace {
TEST(test_monotonic_allocator,
     separate_char_allocations_are_contiguous_without_padding) {
  monotonic_allocator alloc;
  char* a = alloc.new_object<char>();
  char* b = alloc.new_object<char>();
  char* c = alloc.new_object<char>();
  char* d = alloc.new_object<char>();
  EXPECT_EQ(reinterpret_cast<std::uintptr_t>(a) + 1,
            reinterpret_cast<std::uintptr_t>(b));
  EXPECT_EQ(reinterpret_cast<std::uintptr_t>(b) + 1,
            reinterpret_cast<std::uintptr_t>(c));
  EXPECT_EQ(reinterpret_cast<std::uintptr_t>(c) + 1,
            reinterpret_cast<std::uintptr_t>(d));
}

#if QLJS_HAVE_OVERALIGNED
struct alignas(std::max_align_t) overaligned_64 {};
#endif

using test_monotonic_allocator_typed_types = ::testing::Types<
#if QLJS_HAVE_OVERALIGNED
    overaligned_64,
#endif
    char, short, int, long, long long,  //
    float, double, long double,
    std::max_align_t>;
template <class T>
class test_monotonic_allocator_typed : public ::testing::Test {};
TYPED_TEST_SUITE(test_monotonic_allocator_typed,
                 test_monotonic_allocator_typed_types,
                 ::testing::internal::DefaultNameGenerator);
TYPED_TEST(test_monotonic_allocator_typed, type_alignment_is_respected) {
  monotonic_allocator alloc;
  [[maybe_unused]] char* misalign = alloc.new_object<char>();
  TypeParam* p = alloc.new_object<TypeParam>();
  EXPECT_EQ(reinterpret_cast<std::uintptr_t>(p) & (alignof(TypeParam) - 1), 0)
      << p;
}
}
}

// quick-lint-js finds bugs in JavaScript programs.
// Copyright (C) 2020  Matthew Glazar
//
// This file is part of quick-lint-js.
//
// quick-lint-js is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// quick-lint-js is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with quick-lint-js.  If not, see <https://www.gnu.org/licenses/>.
