#!/usr/bin/env bash
#
# E2E test for the markdown pull/parse round-trip via the infrapad CLI.
#
# Prerequisites:
#   - infrapad server running (GRPC_ADDR defaults to localhost:50061)
#   - infrapad CLI binary built (set INFRAPAD_CLI or it uses ../../../cli/infrapad)
#
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "${SCRIPT_DIR}/_lib.sh"

TMPDIR_E2E=$(mktemp -d)
trap 'rm -rf "$TMPDIR_E2E"' EXIT

echo "=== Markdown Pull/Parse E2E Test ==="
echo ""

# -----------------------------------------------------------------------
# 1. Set up test document with blocks
# -----------------------------------------------------------------------
echo "Step 1: Create test document with blocks"
create_test_doc
echo "  doc: $DOC_ID"
echo ""

# -----------------------------------------------------------------------
# 2. Pull the document as markdown
# -----------------------------------------------------------------------
echo "Step 2: Pull document as markdown"
MD_FILE="${TMPDIR_E2E}/${DOC_ID}.md"
PULL_OUT=$($CLI md pull --doc "$DOC_ID" --file "$MD_FILE" 2>&1)
assert_contains "pull reports output file" "$PULL_OUT" "Written to"

MD_CONTENT=$(cat "$MD_FILE")
echo "$MD_CONTENT"
echo ""

# -----------------------------------------------------------------------
# 3. Verify frontmatter
# -----------------------------------------------------------------------
echo "Step 3: Verify frontmatter"
assert_contains "frontmatter has doc id" "$MD_CONTENT" "doc: $DOC_ID"
assert_contains "frontmatter has title" "$MD_CONTENT" "title: Payment service crash loop"
assert_contains "frontmatter has namespace" "$MD_CONTENT" "namespace: payments"
assert_contains "frontmatter has status" "$MD_CONTENT" "status: active"
echo ""

# -----------------------------------------------------------------------
# 4. Verify block directives and content
# -----------------------------------------------------------------------
echo "Step 4: Verify block directives and content"
assert_contains "has alerts_matcher directive" "$MD_CONTENT" "::infrapad_block{block=1 rev=2 type=alerts_matcher"
assert_contains "has markdown directive" "$MD_CONTENT" "::infrapad_block{block=2 rev=2 type=markdown"
assert_contains "alerts content has CrashLoopBackOff" "$MD_CONTENT" "CrashLoopBackOff"
assert_contains "alerts content has KubeNodeNotReady" "$MD_CONTENT" "KubeNodeNotReady"
assert_contains "alerts wrapped in yaml fence" "$MD_CONTENT" '```yaml'
assert_contains "markdown text present" "$MD_CONTENT" "updated investigation writeup"
assert_contains "markdown multiline present" "$MD_CONTENT" "root cause identified"
echo ""

# -----------------------------------------------------------------------
# 5. Parse the pulled markdown and verify round-trip
# -----------------------------------------------------------------------
echo "Step 5: Parse the pulled markdown (round-trip)"
PARSE_OUT=$($CLI md parse --file "$MD_FILE")
echo "$PARSE_OUT"

assert_contains "parsed doc id" "$PARSE_OUT" "doc:       $DOC_ID"
assert_contains "parsed title" "$PARSE_OUT" "title:     Payment service crash loop"
assert_contains "parsed namespace" "$PARSE_OUT" "namespace: payments"
assert_contains "parsed status" "$PARSE_OUT" "status:    active"
assert_contains "parsed block count" "$PARSE_OUT" "Blocks (2):"
assert_contains "parsed alerts_matcher type" "$PARSE_OUT" "type:   alerts_matcher"
assert_contains "parsed markdown type" "$PARSE_OUT" "type:   markdown"
assert_contains "parsed alerts block number" "$PARSE_OUT" "block:  1"
assert_contains "parsed markdown block number" "$PARSE_OUT" "block:  2"
echo ""

# -----------------------------------------------------------------------
# 6. Pull to stdout (no --file flag)
# -----------------------------------------------------------------------
echo "Step 6: Pull to stdout"
STDOUT_OUT=$($CLI md pull --doc "$DOC_ID")
assert_equals "stdout matches file content" "$STDOUT_OUT" "$MD_CONTENT"
echo ""

# -----------------------------------------------------------------------
# 7. Edit a markdown block locally and push it
# -----------------------------------------------------------------------
echo "Step 7: Edit markdown block locally and push"
# Append new content to block 2 (markdown block).
sed -i 's/root cause identified/root cause identified\n\nAdditional notes from local edit./' "$MD_FILE"

EDITED_CONTENT=$(cat "$MD_FILE")
assert_contains "local edit present before push" "$EDITED_CONTENT" "Additional notes from local edit."

PUSH_OUT=$($CLI md push --file "$MD_FILE" 2>&1)
assert_contains "push reports block updated" "$PUSH_OUT" "Block 2 updated"
assert_contains "push reports file written" "$PUSH_OUT" "Written to"
echo ""

# -----------------------------------------------------------------------
# 8. Verify the file was refreshed from the server after push
# -----------------------------------------------------------------------
echo "Step 8: Verify file refreshed after push"
PUSHED_CONTENT=$(cat "$MD_FILE")
assert_contains "refreshed file has doc id" "$PUSHED_CONTENT" "doc: $DOC_ID"
assert_contains "refreshed file has block 2 directive" "$PUSHED_CONTENT" "::infrapad_block{block=2"
assert_contains "refreshed file has pushed content" "$PUSHED_CONTENT" "Additional notes from local edit."
assert_contains "refreshed file has original content" "$PUSHED_CONTENT" "root cause identified"
# The rev should have incremented from 2 to 3 after the push.
assert_contains "refreshed block 2 has new rev" "$PUSHED_CONTENT" "::infrapad_block{block=2 rev=3 type=markdown"
echo ""

# -----------------------------------------------------------------------
# 9. Independently pull and verify the server has the pushed content
# -----------------------------------------------------------------------
echo "Step 9: Independent pull to verify server state"
VERIFY_FILE="${TMPDIR_E2E}/${DOC_ID}_verify.md"
$CLI md pull --doc "$DOC_ID" --file "$VERIFY_FILE" 2>/dev/null
VERIFY_CONTENT=$(cat "$VERIFY_FILE")
assert_contains "independent pull has pushed content" "$VERIFY_CONTENT" "Additional notes from local edit."
assert_contains "independent pull has original content" "$VERIFY_CONTENT" "root cause identified"
assert_contains "independent pull has updated rev" "$VERIFY_CONTENT" "::infrapad_block{block=2 rev=3 type=markdown"
# Block 1 should be unchanged.
assert_contains "block 1 unchanged after push" "$VERIFY_CONTENT" "::infrapad_block{block=1 rev=2 type=alerts_matcher"
assert_contains "block 1 content intact" "$VERIFY_CONTENT" "CrashLoopBackOff"
echo ""

# -----------------------------------------------------------------------
# 10. Parse the pushed file to verify round-trip integrity
# -----------------------------------------------------------------------
echo "Step 10: Parse pushed file for round-trip integrity"
PARSE_PUSHED=$($CLI md parse --file "$MD_FILE")
assert_contains "parsed pushed block count" "$PARSE_PUSHED" "Blocks (2):"
assert_contains "parsed pushed markdown content" "$PARSE_PUSHED" "Additional notes from local edit."
assert_contains "parsed pushed original content" "$PARSE_PUSHED" "root cause identified"
assert_contains "parsed pushed alerts_matcher intact" "$PARSE_PUSHED" "type:   alerts_matcher"
echo ""

# -----------------------------------------------------------------------
# 11. Add a new block via md push
# -----------------------------------------------------------------------
echo "Step 11: Add a new block locally and push"
# Append a new block directive and content to the pulled file.
cat >> "$MD_FILE" <<'EOF'

::infrapad_block{block=new type=markdown}

# Additional notes from the investigation

This was a red-herring.
EOF

NEW_BLOCK_CONTENT=$(cat "$MD_FILE")
assert_contains "local file has new block directive" "$NEW_BLOCK_CONTENT" "block=new type=markdown"
assert_contains "local file has new block content" "$NEW_BLOCK_CONTENT" "This was a red-herring."

PUSH_NEW_OUT=$($CLI md push --file "$MD_FILE" 2>&1)
assert_contains "push new reports block added" "$PUSH_NEW_OUT" "Block added"
assert_contains "push new reports file written" "$PUSH_NEW_OUT" "Written to"
echo ""

# -----------------------------------------------------------------------
# 12. Verify the file was refreshed and the new block got a real number
# -----------------------------------------------------------------------
echo "Step 12: Verify new block assigned a real block number after push"
PUSHED_NEW_CONTENT=$(cat "$MD_FILE")
assert_contains "refreshed file has doc id" "$PUSHED_NEW_CONTENT" "doc: $DOC_ID"
assert_not_contains "refreshed file has no block=new directive" "$PUSHED_NEW_CONTENT" "block=new"
assert_contains "refreshed file has new block content" "$PUSHED_NEW_CONTENT" "This was a red-herring."
assert_contains "refreshed file has new block heading" "$PUSHED_NEW_CONTENT" "# Additional notes from the investigation"
# The new block should be block 3.
assert_contains "refreshed file has block 3 directive" "$PUSHED_NEW_CONTENT" "::infrapad_block{block=3"
assert_contains "refreshed file has block 3 as markdown" "$PUSHED_NEW_CONTENT" "block=3 rev=1 type=markdown"
# Earlier blocks should be unchanged.
assert_contains "block 1 still present" "$PUSHED_NEW_CONTENT" "::infrapad_block{block=1 rev=2 type=alerts_matcher"
assert_contains "block 2 still present" "$PUSHED_NEW_CONTENT" "::infrapad_block{block=2 rev=3 type=markdown"
echo ""

# -----------------------------------------------------------------------
# 13. Independent pull to verify server received the new block
# -----------------------------------------------------------------------
echo "Step 13: Independent pull to verify new block on server"
VERIFY_NEW_FILE="${TMPDIR_E2E}/${DOC_ID}_verify_new.md"
$CLI md pull --doc "$DOC_ID" --file "$VERIFY_NEW_FILE" 2>/dev/null
VERIFY_NEW_CONTENT=$(cat "$VERIFY_NEW_FILE")
assert_contains "independent pull has 3 blocks" "$VERIFY_NEW_CONTENT" "::infrapad_block{block=3"
assert_contains "independent pull has new block content" "$VERIFY_NEW_CONTENT" "This was a red-herring."
assert_contains "independent pull has new block heading" "$VERIFY_NEW_CONTENT" "# Additional notes from the investigation"
assert_contains "independent pull block 1 intact" "$VERIFY_NEW_CONTENT" "CrashLoopBackOff"
assert_contains "independent pull block 2 intact" "$VERIFY_NEW_CONTENT" "root cause identified"
echo ""

# -----------------------------------------------------------------------
# 14. Parse the file with the new block to verify round-trip integrity
# -----------------------------------------------------------------------
echo "Step 14: Parse file with new block for round-trip integrity"
PARSE_NEW=$($CLI md parse --file "$MD_FILE")
assert_contains "parsed new block count" "$PARSE_NEW" "Blocks (3):"
assert_contains "parsed new block content" "$PARSE_NEW" "This was a red-herring."
assert_contains "parsed block 3 number" "$PARSE_NEW" "block:  3"
assert_contains "parsed alerts_matcher still intact" "$PARSE_NEW" "type:   alerts_matcher"
echo ""

# -----------------------------------------------------------------------
# 15. Add two new blocks at once and push
# -----------------------------------------------------------------------
echo "Step 15: Add two new blocks at once and push"
# Simulate the incident-investigate workflow: investigation summary + recommended actions.
cat >> "$MD_FILE" <<'EOF'

::infrapad_block{type=markdown block=new}
# Investigation Summary

**Investigated at**: 2026-08-14T12:00:00Z

## Findings

The payment service pod is in CrashLoopBackOff due to an OOM kill.

## Diagnosis

Memory limit is set too low for the current traffic pattern.

::infrapad_block{type=markdown block=new}
# Recommended Actions

1. Increase memory limit from 256Mi to 512Mi.
2. Add horizontal pod autoscaler.
3. Review recent traffic spike with the platform team.
EOF

TWO_BLOCK_CONTENT=$(cat "$MD_FILE")
# Verify both new block directives are present locally.
TWO_NEW_COUNT=$(grep -c 'block=new' "$MD_FILE")
assert_equals "local file has two block=new directives" "$TWO_NEW_COUNT" "2"
assert_contains "local file has investigation summary" "$TWO_BLOCK_CONTENT" "# Investigation Summary"
assert_contains "local file has recommended actions" "$TWO_BLOCK_CONTENT" "# Recommended Actions"

PUSH_TWO_OUT=$($CLI md push --file "$MD_FILE" 2>&1)
echo "$PUSH_TWO_OUT"
# Both blocks should be reported as added.
TWO_ADDED_COUNT=$(echo "$PUSH_TWO_OUT" | grep -c "Block added")
assert_equals "push reports two blocks added" "$TWO_ADDED_COUNT" "2"
assert_contains "push two reports file written" "$PUSH_TWO_OUT" "Written to"
echo ""

# -----------------------------------------------------------------------
# 16. Verify both new blocks got real block numbers after push
# -----------------------------------------------------------------------
echo "Step 16: Verify both new blocks assigned real block numbers"
PUSHED_TWO_CONTENT=$(cat "$MD_FILE")
assert_not_contains "refreshed file has no block=new" "$PUSHED_TWO_CONTENT" "block=new"
assert_contains "refreshed file has investigation summary" "$PUSHED_TWO_CONTENT" "# Investigation Summary"
assert_contains "refreshed file has recommended actions" "$PUSHED_TWO_CONTENT" "# Recommended Actions"
assert_contains "refreshed file has OOM diagnosis" "$PUSHED_TWO_CONTENT" "OOM kill"
assert_contains "refreshed file has memory limit action" "$PUSHED_TWO_CONTENT" "Increase memory limit"
# The two new blocks should be block 4 and block 5.
assert_contains "refreshed file has block 4" "$PUSHED_TWO_CONTENT" "::infrapad_block{block=4 rev=1 type=markdown"
assert_contains "refreshed file has block 5" "$PUSHED_TWO_CONTENT" "::infrapad_block{block=5 rev=1 type=markdown"
# Earlier blocks should be unchanged.
assert_contains "block 1 still present" "$PUSHED_TWO_CONTENT" "::infrapad_block{block=1 rev=2 type=alerts_matcher"
assert_contains "block 2 still present" "$PUSHED_TWO_CONTENT" "::infrapad_block{block=2 rev=3 type=markdown"
assert_contains "block 3 still present" "$PUSHED_TWO_CONTENT" "::infrapad_block{block=3"
echo ""

# -----------------------------------------------------------------------
# 17. Independent pull to verify server received both new blocks
# -----------------------------------------------------------------------
echo "Step 17: Independent pull to verify both new blocks on server"
VERIFY_TWO_FILE="${TMPDIR_E2E}/${DOC_ID}_verify_two.md"
$CLI md pull --doc "$DOC_ID" --file "$VERIFY_TWO_FILE" 2>/dev/null
VERIFY_TWO_CONTENT=$(cat "$VERIFY_TWO_FILE")
assert_contains "independent pull has block 4" "$VERIFY_TWO_CONTENT" "::infrapad_block{block=4"
assert_contains "independent pull has block 5" "$VERIFY_TWO_CONTENT" "::infrapad_block{block=5"
assert_contains "independent pull has investigation summary" "$VERIFY_TWO_CONTENT" "# Investigation Summary"
assert_contains "independent pull has recommended actions" "$VERIFY_TWO_CONTENT" "# Recommended Actions"
assert_contains "independent pull has OOM diagnosis" "$VERIFY_TWO_CONTENT" "OOM kill"
assert_contains "independent pull has memory limit action" "$VERIFY_TWO_CONTENT" "Increase memory limit"
assert_contains "independent pull block 1 intact" "$VERIFY_TWO_CONTENT" "CrashLoopBackOff"
assert_contains "independent pull block 2 intact" "$VERIFY_TWO_CONTENT" "root cause identified"
assert_contains "independent pull block 3 intact" "$VERIFY_TWO_CONTENT" "This was a red-herring."
echo ""

# -----------------------------------------------------------------------
# 18. Parse the file with five blocks to verify round-trip integrity
# -----------------------------------------------------------------------
echo "Step 18: Parse file with five blocks for round-trip integrity"
PARSE_TWO=$($CLI md parse --file "$MD_FILE")
assert_contains "parsed five block count" "$PARSE_TWO" "Blocks (5):"
assert_contains "parsed block 4 number" "$PARSE_TWO" "block:  4"
assert_contains "parsed block 5 number" "$PARSE_TWO" "block:  5"
assert_contains "parsed investigation content" "$PARSE_TWO" "Investigation Summary"
assert_contains "parsed recommended actions content" "$PARSE_TWO" "Recommended Actions"
echo ""

# -----------------------------------------------------------------------
# Summary
# -----------------------------------------------------------------------
print_summary
