#!/usr/bin/env bash
#
# E2E test for the incident investigation flow via the infrapad CLI.
# Mirrors server/test/e2e/incident_investigation_test.go
#
# Prerequisites:
#   - infrapad server running (GRPC_ADDR defaults to localhost:50051)
#   - infrapad CLI binary built (set INFRAPAD_CLI or it uses ../../../cli/infrapad)
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
CLI="${INFRAPAD_CLI:-${SCRIPT_DIR}/../../infrapad} --grpc-addr ${GRPC_ADDR:-localhost:50051}"

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

echo "=== Incident Investigation E2E Test ==="
echo ""

# -----------------------------------------------------------------------
# 1. Create a document
# -----------------------------------------------------------------------
echo "Step 1: Create document"
CREATE_OUT=$($CLI doc create --title "Payment service crash loop" --namespace payments)
echo "$CREATE_OUT"

DOC_NAME=$(echo "$CREATE_OUT" | grep '^name:' | awk '{print $2}')
assert_contains "doc name is non-empty" "$DOC_NAME" "docs/"
assert_contains "namespace is payments" "$CREATE_OUT" "namespace: payments"

echo ""

# -----------------------------------------------------------------------
# 2. Add alerts_matcher block
# -----------------------------------------------------------------------
echo "Step 2: Add alerts_matcher block"
ADD_ALERTS_OUT=$($CLI block add \
  --doc "$DOC_NAME" \
  --type alerts_matcher \
  --matchers '[{"name": ["CrashLoopBackOff"]}]')
echo "$ADD_ALERTS_OUT"

ALERTS_BLOCK_NUM=$(echo "$ADD_ALERTS_OUT" | grep '^block_number:' | awk '{print $2}')
assert_equals "alerts block_number is 1" "$ALERTS_BLOCK_NUM" "1"
assert_contains "alerts revision is 1" "$ADD_ALERTS_OUT" "revision_number: 1"
assert_contains "matchers_count is 1" "$ADD_ALERTS_OUT" "matchers_count: 1"

echo ""

# -----------------------------------------------------------------------
# 3. Add markdown block
# -----------------------------------------------------------------------
echo "Step 3: Add markdown block"
ADD_MD_OUT=$($CLI block add \
  --doc "$DOC_NAME" \
  --type markdown \
  --text "initial investigation writeup")
echo "$ADD_MD_OUT"

MD_BLOCK_NUM=$(echo "$ADD_MD_OUT" | grep '^block_number:' | awk '{print $2}')
assert_equals "markdown block_number is 2" "$MD_BLOCK_NUM" "2"
assert_contains "markdown revision is 1" "$ADD_MD_OUT" "revision_number: 1"
assert_contains "markdown text" "$ADD_MD_OUT" "text: initial investigation writeup"

echo ""

# -----------------------------------------------------------------------
# 4. Update alerts_matcher block: add KubeNodeNotReady
# -----------------------------------------------------------------------
echo "Step 4: Update alerts_matcher block"
UPDATE_ALERTS_OUT=$($CLI block update \
  --doc "$DOC_NAME" \
  --block-number 1 \
  --type alerts_matcher \
  --matchers '[{"name": ["CrashLoopBackOff"]}, {"name": ["KubeNodeNotReady"]}]')
echo "$UPDATE_ALERTS_OUT"

assert_contains "updated alerts revision is 2" "$UPDATE_ALERTS_OUT" "revision_number: 2"
assert_contains "matchers_count is 2" "$UPDATE_ALERTS_OUT" "matchers_count: 2"

echo ""

# -----------------------------------------------------------------------
# 5. Update markdown block
# -----------------------------------------------------------------------
echo "Step 5: Update markdown block"
UPDATE_MD_OUT=$($CLI block update \
  --doc "$DOC_NAME" \
  --block-number 2 \
  --type markdown \
  --text "updated investigation writeup")
echo "$UPDATE_MD_OUT"

assert_contains "updated markdown revision is 2" "$UPDATE_MD_OUT" "revision_number: 2"
assert_contains "updated markdown text" "$UPDATE_MD_OUT" "text: updated investigation writeup"

echo ""

# -----------------------------------------------------------------------
# 6. Verify final state: GetDoc with 2 blocks at revision 2
# -----------------------------------------------------------------------
echo "Step 6: Verify final document state"
GET_DOC_OUT=$($CLI doc get "$DOC_NAME")
echo "$GET_DOC_OUT"

assert_contains "doc has 2 blocks" "$GET_DOC_OUT" "blocks: 2"

# Count blocks at revision 2
REV2_COUNT=$(echo "$GET_DOC_OUT" | grep -c "rev=2" || true)
assert_equals "both blocks at revision 2" "$REV2_COUNT" "2"

echo ""

# -----------------------------------------------------------------------
# 7. Verify block history for alerts_matcher
# -----------------------------------------------------------------------
echo "Step 7: Verify alerts_matcher block history"
HISTORY_OUT=$($CLI block history \
  --doc "$DOC_NAME" \
  --block-number 1)
echo "$HISTORY_OUT"

# Should have 2 revisions
REVISION_COUNT=$(echo "$HISTORY_OUT" | grep -c '^revision_number:' || true)
assert_equals "history has 2 revisions" "$REVISION_COUNT" "2"

# First revision: 1 matcher; second: 2 matchers
MATCHERS_COUNTS=$(echo "$HISTORY_OUT" | grep '^matchers_count:' | awk '{print $2}' | tr '\n' ',')
assert_equals "matchers counts are 1,2" "$MATCHERS_COUNTS" "1,2,"

echo ""

# -----------------------------------------------------------------------
# Summary
# -----------------------------------------------------------------------
echo "=== Results: $pass passed, $fail failed ==="
if [[ $fail -gt 0 ]]; then
  exit 1
fi
echo "All tests passed!"
