// Copyright (C) 2020  Matthew Glazar
// See end of file for extended copyright information.

#include <iostream>
#include <memory>
#include <quick-lint-js/cli-location.h>
#include <quick-lint-js/error.h>
#include <quick-lint-js/location.h>
#include <quick-lint-js/optional.h>
#include <quick-lint-js/padded-string.h>
#include <quick-lint-js/token.h>
#include <quick-lint-js/unreachable.h>
#include <quick-lint-js/wasm-demo-error-reporter.h>
#include <string>

namespace quick_lint_js {
wasm_demo_error_reporter::wasm_demo_error_reporter(padded_string_view input)
    : locator_(input), input_(input.data()) {}

#define QLJS_ERROR_TYPE(name, code, struct_body, format_call) \
  void wasm_demo_error_reporter::report(name e) {             \
    format_error(e, this->format());                          \
  }
QLJS_X_ERROR_TYPES
#undef QLJS_ERROR_TYPE

const wasm_demo_error_reporter::error *
wasm_demo_error_reporter::get_errors() noexcept {
  // Null-terminate the returned errors.
  this->errors_.emplace_back();

  return this->errors_.data();
}

wasm_demo_error_formatter wasm_demo_error_reporter::format() {
  return wasm_demo_error_formatter(this);
}

char8 *wasm_demo_error_reporter::allocate_c_string(string8_view string) {
  char8 *result = this->string_allocator_.allocate_uninitialized_array<char8>(
      string.size() + 1);
  std::uninitialized_copy(string.begin(), string.end(), result);
  result[string.size()] = u8'\0';
  return result;
}

wasm_demo_error_formatter::wasm_demo_error_formatter(
    wasm_demo_error_reporter *reporter)
    : reporter_(reporter) {}

void wasm_demo_error_formatter::write_before_message(severity sev,
                                                     const source_code_span &) {
  if (sev == severity::note) {
    // Don't write notes. Only write the main message.
    return;
  }
  QLJS_ASSERT(this->current_message_.empty());
}

void wasm_demo_error_formatter::write_message_part(severity sev,
                                                   string8_view message) {
  if (sev == severity::note) {
    // Don't write notes. Only write the main message.
    return;
  }
  this->current_message_.append(message);
}

void wasm_demo_error_formatter::write_after_message(
    severity sev, const source_code_span &origin) {
  if (sev == severity::note) {
    // Don't write notes. Only write the main message.
    return;
  }
  wasm_demo_error_reporter::error &e = this->reporter_->errors_.emplace_back();
  wasm_demo_source_range r = this->reporter_->locator_.range(origin);
  e.begin_offset = r.begin;
  e.end_offset = r.end;
  e.message = this->reporter_->allocate_c_string(this->current_message_);
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
