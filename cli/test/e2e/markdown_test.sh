#!/usr/bin/env bash
#
# E2E test for the markdown pull/parse round-trip via the infrapad CLI.
#
# Prerequisites:
#   - infrapad server running (GRPC_ADDR defaults to localhost:50051)
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

PUSH_OUT=$($CLI md push --file "$MD_FILE" --block 2 2>&1)
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
# Summary
# -----------------------------------------------------------------------
print_summary
