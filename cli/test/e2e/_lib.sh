#!/usr/bin/env bash
#
# Shared helpers for infrapad CLI e2e tests.
#
# Source this file at the top of each test script:
#   SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
#   source "${SCRIPT_DIR}/_lib.sh"
#

set -euo pipefail

SCRIPT_DIR="${SCRIPT_DIR:?SCRIPT_DIR must be set before sourcing _lib.sh}"
CLI="${INFRAPAD_CLI:-${SCRIPT_DIR}/../../infrapad} --grpc-addr ${GRPC_ADDR:-localhost:50061}"

pass=0
fail=0

assert_contains() {
  local label="$1" haystack="$2" needle="$3"
  if echo "$haystack" | grep -qF "$needle"; then
    echo "  ✓ $label"
    pass=$((pass + 1))
  else
    echo "  ✗ $label: expected to contain '$needle'"
    echo "    got: $haystack"
    fail=$((fail + 1))
  fi
}

assert_equals() {
  local label="$1" actual="$2" expected="$3"
  if [[ "$actual" == "$expected" ]]; then
    echo "  ✓ $label"
    pass=$((pass + 1))
  else
    echo "  ✗ $label: expected '$expected', got '$actual'"
    fail=$((fail + 1))
  fi
}

assert_not_contains() {
  local label="$1" haystack="$2" needle="$3"
  if ! echo "$haystack" | grep -qF "$needle"; then
    echo "  ✓ $label"
    pass=$((pass + 1))
  else
    echo "  ✗ $label: expected NOT to contain '$needle'"
    echo "    got: $haystack"
    fail=$((fail + 1))
  fi
}

# table_data_rows filters table output to only data rows, stripping the header
# line and any full-row fenced blocks (```...```).
table_data_rows() {
  local output="$1"
  echo "$output" | awk '
    NR == 1 { next }
    /^```/  { fence = !fence; next }
    !fence  { print }
  '
}

# table_cell extracts a cell value from table output by column header name and
# row number (1-based, excluding the header). Works with the 3-space-separated
# column format produced by the printer.  Full-row fenced blocks are skipped.
#
# Usage: table_cell "$output" "Header" [row]
#   row defaults to 1
table_cell() {
  local output="$1" header="$2" row="${3:-1}"
  local header_line data_line col_start col_end

  header_line=$(echo "$output" | head -1)

  # Find the column start position (byte offset) from the header line.
  col_start=$(echo "$header_line" | grep -b -o "$header" | head -1 | cut -d: -f1)

  # Find end: start of next column header or end of line.
  # Next column starts after 3+ spaces following current header text.
  local after=$((col_start + ${#header}))
  local rest="${header_line:$after}"
  if [[ "$rest" =~ ^([[:space:]]+)[^[:space:]] ]]; then
    col_end=$((after + ${#BASH_REMATCH[1]}))
  else
    col_end=10000  # last column, take everything
  fi

  data_line=$(table_data_rows "$output" | sed -n "${row}p")
  # Extract the substring, trim whitespace.
  echo "${data_line:$col_start:$((col_end - col_start))}" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//'
}

# create_test_doc creates a document with blocks for testing.
# Sets DOC_ID variable in the caller's scope.
#
# Usage: create_test_doc
create_test_doc() {
  CREATE_OUT=$($CLI document create --title "Payment service crash loop" --namespace payments)
  DOC_ID=$(table_cell "$CREATE_OUT" "ID")

  # Add alerts_matcher block (block 1).
  $CLI block add \
    --document "$DOC_ID" \
    --type alerts_matcher \
    --content '{"LabelsMatchers": [{"name": ["CrashLoopBackOff"]}]}' > /dev/null

  # Update alerts_matcher to add KubeNodeNotReady (rev 2).
  $CLI block update \
    --document "$DOC_ID" \
    --block-number 1 \
    --type alerts_matcher \
    --content '{"LabelsMatchers": [{"name": ["CrashLoopBackOff"]}, {"name": ["KubeNodeNotReady"]}]}' > /dev/null

  # Add markdown block (block 2).
  $CLI block add \
    --document "$DOC_ID" \
    --type markdown \
    --content '{"text": "initial investigation writeup\nneeds further analysis"}' > /dev/null

  # Update markdown block (rev 2).
  $CLI block update \
    --document "$DOC_ID" \
    --block-number 2 \
    --type markdown \
    --content '{"text": "updated investigation writeup\nroot cause identified"}' > /dev/null
}

# print_summary prints the test results and exits with code 1 if any failed.
print_summary() {
  echo ""
  echo "=== Results: $pass passed, $fail failed ==="
  if [[ $fail -gt 0 ]]; then
    exit 1
  fi
  echo "All tests passed!"
}
