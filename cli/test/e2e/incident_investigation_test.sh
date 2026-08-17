#!/usr/bin/env bash
#
# E2E test for the incident investigation flow via the infrapad CLI.
# Mirrors server/test/e2e/incident_investigation_test.go
#
# Prerequisites:
#   - infrapad server running (GRPC_ADDR defaults to localhost:50061)
#   - infrapad CLI binary built (set INFRAPAD_CLI or it uses ../../../cli/infrapad)
#
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "${SCRIPT_DIR}/_lib.sh"

echo "=== Incident Investigation E2E Test ==="
echo ""

# -----------------------------------------------------------------------
# 1. Create a document
# -----------------------------------------------------------------------
echo "Step 1: Create document"
CREATE_OUT=$($CLI doc create --title "Payment service crash loop" --namespace payments)
echo "$CREATE_OUT"

DOC_ID=$(table_cell "$CREATE_OUT" "ID")
assert_contains "doc id is non-empty" "$DOC_ID" "-"
DOC_NAME="docs/${DOC_ID}"
assert_equals "namespace is payments" "$(table_cell "$CREATE_OUT" "NAMESPACE")" "payments"

echo ""

# -----------------------------------------------------------------------
# 2. Add alerts_matcher block
# -----------------------------------------------------------------------
echo "Step 2: Add alerts_matcher block"
ADD_ALERTS_OUT=$($CLI block add \
  --doc "$DOC_NAME" \
  --type alerts_matcher \
  --content '{"LabelsMatchers": [{"name": ["CrashLoopBackOff"]}]}')
echo "$ADD_ALERTS_OUT"

assert_equals "alerts block_number is 1" "$(table_cell "$ADD_ALERTS_OUT" "BLOCK")" "1"
assert_equals "alerts revision is 1" "$(table_cell "$ADD_ALERTS_OUT" "REV")" "1"

# Verify matcher content via JSON output
ADD_ALERTS_JSON=$($CLI block get -o json --doc "$DOC_NAME" --block-number 1)
assert_contains "matchers present" "$ADD_ALERTS_JSON" "CrashLoopBackOff"

echo ""

# -----------------------------------------------------------------------
# 3. Add markdown block
# -----------------------------------------------------------------------
echo "Step 3: Add markdown block"
ADD_MD_OUT=$($CLI block add \
  --doc "$DOC_NAME" \
  --type markdown \
  --content '{"text": "initial investigation writeup\nneeds further analysis"}')
echo "$ADD_MD_OUT"

MD_BLOCK_NUM=$(table_cell "$ADD_MD_OUT" "BLOCK")
assert_equals "markdown block_number is 2" "$MD_BLOCK_NUM" "2"
assert_equals "markdown revision is 1" "$(table_cell "$ADD_MD_OUT" "REV")" "1"

# Verify content via JSON
ADD_MD_JSON=$($CLI block get -o json --doc "$DOC_NAME" --block-number "$MD_BLOCK_NUM")
assert_contains "markdown text" "$ADD_MD_JSON" "initial investigation writeup"
assert_contains "markdown multiline content uses | style" "$ADD_MD_OUT" '|'

echo ""

# -----------------------------------------------------------------------
# 4. Update alerts_matcher block: add KubeNodeNotReady
# -----------------------------------------------------------------------
echo "Step 4: Update alerts_matcher block"
UPDATE_ALERTS_OUT=$($CLI block update \
  --doc "$DOC_NAME" \
  --block-number 1 \
  --type alerts_matcher \
  --content '{"LabelsMatchers": [{"name": ["CrashLoopBackOff"]}, {"name": ["KubeNodeNotReady"]}]}')
echo "$UPDATE_ALERTS_OUT"

assert_equals "updated alerts revision is 2" "$(table_cell "$UPDATE_ALERTS_OUT" "REV")" "2"

# Verify matchers via JSON
UPDATE_ALERTS_JSON=$($CLI block get -o json --doc "$DOC_NAME" --block-number 1)
MATCHERS_COUNT=$(echo "$UPDATE_ALERTS_JSON" | python3 -c "
import sys, json
data = json.load(sys.stdin)
print(len(data.get('content', {}).get('LabelsMatchers', [])))
" 2>/dev/null || echo "?")
assert_equals "matchers_count is 2" "$MATCHERS_COUNT" "2"

echo ""

# -----------------------------------------------------------------------
# 5. Update markdown block
# -----------------------------------------------------------------------
echo "Step 5: Update markdown block"
UPDATE_MD_OUT=$($CLI block update \
  --doc "$DOC_NAME" \
  --block-number "$MD_BLOCK_NUM" \
  --type markdown \
  --content '{"text": "updated investigation writeup\nroot cause identified"}')
echo "$UPDATE_MD_OUT"

assert_equals "updated markdown revision is 2" "$(table_cell "$UPDATE_MD_OUT" "REV")" "2"

UPDATE_MD_JSON=$($CLI block get -o json --doc "$DOC_NAME" --block-number "$MD_BLOCK_NUM")
assert_contains "updated markdown text" "$UPDATE_MD_JSON" "updated investigation writeup"
assert_contains "updated markdown multiline" "$UPDATE_MD_JSON" "root cause identified"

echo ""

# -----------------------------------------------------------------------
# 6. Verify final state: GetDoc with blocks at latest revisions
# -----------------------------------------------------------------------
echo "Step 6: Verify final document state"
LIST_BLOCKS_OUT=$($CLI block list --doc "$DOC_NAME")
echo "$LIST_BLOCKS_OUT"

BLOCK_COUNT=$(table_data_rows "$LIST_BLOCKS_OUT" | wc -l | tr -d ' ')
assert_equals "doc has expected block count" "$BLOCK_COUNT" "2"

echo ""

# -----------------------------------------------------------------------
# 7. Verify block history for alerts_matcher
# -----------------------------------------------------------------------
echo "Step 7: Verify alerts_matcher block history"
HISTORY_OUT=$($CLI block history \
  --doc "$DOC_NAME" \
  --block-number 1)
echo "$HISTORY_OUT"

# Count only data rows (skip header and fenced full-row blocks)
HISTORY_ROW_COUNT=$(table_data_rows "$HISTORY_OUT" | wc -l | tr -d ' ')
assert_equals "history has 2 revisions" "$HISTORY_ROW_COUNT" "2"

# Check revisions via JSON
HISTORY_JSON=$($CLI block history -o json --doc "$DOC_NAME" --block-number 1)
REV1_MATCHERS=$(echo "$HISTORY_JSON" | python3 -c "
import sys, json
data = json.load(sys.stdin)
print(len(data[0].get('content', {}).get('LabelsMatchers', [])))
" 2>/dev/null || echo "?")
REV2_MATCHERS=$(echo "$HISTORY_JSON" | python3 -c "
import sys, json
data = json.load(sys.stdin)
print(len(data[1].get('content', {}).get('LabelsMatchers', [])))
" 2>/dev/null || echo "?")
assert_equals "first revision has 1 matcher" "$REV1_MATCHERS" "1"
assert_equals "second revision has 2 matchers" "$REV2_MATCHERS" "2"

echo ""

# -----------------------------------------------------------------------
# Summary
# -----------------------------------------------------------------------
print_summary
