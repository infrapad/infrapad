#!/usr/bin/env bash
#
# E2E test for the Markdown pull/parse/edit/push round-trip via the infrapad CLI.
# Pull and refreshed push output must match the server export byte-for-byte.
#
# Prerequisites:
#   - InfraPad and the dummy auth proxy running (default http://localhost:8089)
#   - infrapad CLI binary built (set INFRAPAD_CLI or it uses ../../../cli/infrapad)
#   - curl, jq, cmp
#
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "${SCRIPT_DIR}/_lib.sh"

TMPDIR_E2E=$(mktemp -d)
trap 'rm -rf "$TMPDIR_E2E"' EXIT

# Compare files including trailing newlines (unlike command substitution).
assert_same_file() {
  local label="$1" actual="$2" expected="$3"
  if cmp -s "$actual" "$expected"; then
    echo "  ✓ $label"
    pass=$((pass + 1))
  else
    echo "  ✗ $label: files differ"
    diff "$expected" "$actual" | head -20 || true
    fail=$((fail + 1))
  fi
}

# Write the decoded server export without adding or removing newlines.
export_markdown() {
  curl --fail --silent --show-error \
    -H "Authorization: Bearer ${INFRAPAD_TOKEN}" \
    "${API_URL%/}/v1/documents/$1/markdown" | jq -j '.markdown' > "$2"
}

echo "=== Markdown Round-trip E2E Test ==="
echo ""

# -----------------------------------------------------------------------
# 1. Create a document and pull to a nested file and stdout
# -----------------------------------------------------------------------
echo "Step 1: Create a document and pull to a nested file and stdout"
create_test_doc
echo "  doc: $DOC_ID"
MD_FILE="${TMPDIR_E2E}/nested/dir/${DOC_ID}.md"
EXPECTED="${TMPDIR_E2E}/expected.md"
export_markdown "$DOC_ID" "$EXPECTED"
assert_equals "export ends with a newline" "$(tail -c1 "$EXPECTED" | od -An -c | tr -d ' ')" '\n'

PULL_OUT=$($CLI md pull --document "$DOC_ID" --file "$MD_FILE" 2>&1)
assert_contains "pull reports output file" "$PULL_OUT" "Written to $MD_FILE"
assert_same_file "pulled file matches export" "$MD_FILE" "$EXPECTED"
$CLI md pull --document "documents/$DOC_ID" > "${TMPDIR_E2E}/stdout.md"
assert_same_file "stdout matches export" "${TMPDIR_E2E}/stdout.md" "$EXPECTED"

MD_CONTENT=$(cat "$MD_FILE")
assert_contains "frontmatter has doc id" "$MD_CONTENT" "document: $DOC_ID"
assert_contains "frontmatter has title" "$MD_CONTENT" "title: Payment service crash loop"
assert_contains "frontmatter has namespace" "$MD_CONTENT" "namespace: payments"
assert_contains "frontmatter has status" "$MD_CONTENT" "status: active"
assert_contains "has alerts directive" "$MD_CONTENT" "::infrapad_block{block=1 rev=2 type=Prometheus/AlertsMatcher"
assert_contains "has markdown directive" "$MD_CONTENT" "::infrapad_block{block=2 rev=2 type=Core/Markdown"
assert_contains "alerts have structured header" "$MD_CONTENT" '```yaml infrapad'
assert_contains "alerts content has CrashLoopBackOff" "$MD_CONTENT" "CrashLoopBackOff"
assert_contains "alerts content has KubeNodeNotReady" "$MD_CONTENT" "KubeNodeNotReady"
assert_contains "markdown text present" "$MD_CONTENT" "updated investigation writeup"
assert_contains "markdown multiline present" "$MD_CONTENT" "root cause identified"
echo ""

# -----------------------------------------------------------------------
# 2. Parse the pulled Markdown offline
# -----------------------------------------------------------------------
echo "Step 2: Parse the pulled Markdown offline"
# An invalid API URL proves that parse neither validates nor contacts the API.
PARSE_OUT=$(${INFRAPAD_CLI:-${SCRIPT_DIR}/../../infrapad} --api-url 'not a URL' md parse --file "$MD_FILE")
assert_contains "parsed doc id" "$PARSE_OUT" "document:  $DOC_ID"
assert_contains "parsed title" "$PARSE_OUT" "title:     Payment service crash loop"
assert_contains "parsed namespace" "$PARSE_OUT" "namespace: payments"
assert_contains "parsed status" "$PARSE_OUT" "status:    active"
assert_contains "parsed block count" "$PARSE_OUT" "Blocks (2):"
assert_contains "parsed alerts type" "$PARSE_OUT" "type:   Prometheus/AlertsMatcher"
assert_contains "parsed markdown type" "$PARSE_OUT" "type:   Core/Markdown"
assert_contains "parsed alerts block number" "$PARSE_OUT" "block:  1"
assert_contains "parsed markdown block number" "$PARSE_OUT" "block:  2"
echo ""

# -----------------------------------------------------------------------
# 3. Refresh using frontmatter only, and override with --document
# -----------------------------------------------------------------------
echo "Step 3: Refresh using frontmatter only, and override with --document"
# Keep valid frontmatter; break everything after it. Pull must not parse blocks.
awk 'n < 2 { print; if ($0 == "---") n++; next } { print "::infrapad_block{block=oops type=Unknown/Type"; exit }' \
  "$EXPECTED" > "$MD_FILE"
PULL_OUT=$($CLI md pull --file "$MD_FILE" 2>&1)
assert_contains "file-only pull reports output file" "$PULL_OUT" "Written to"
assert_same_file "file-only pull repairs damaged body" "$MD_FILE" "$EXPECTED"

# A second (empty) document suffices to check explicit document precedence.
OTHER_DOC=$($CLI document create --title "Other document" --namespace payments -o json | jq -r '.name')
export_markdown "${OTHER_DOC#documents/}" "${TMPDIR_E2E}/other.md"
$CLI md pull --document "$OTHER_DOC" --file "$MD_FILE" 2> /dev/null
assert_same_file "explicit document overrides destination metadata" "$MD_FILE" "${TMPDIR_E2E}/other.md"
$CLI md pull --document "$DOC_ID" --file "$MD_FILE" 2> /dev/null
echo ""

# -----------------------------------------------------------------------
# 4. Edit body and header fields, add a block, and push
# -----------------------------------------------------------------------
echo "Step 4: Edit body and header fields, add a block, and push"
sed -i \
  -e 's/root cause identified/root cause identified\n\nAdditional notes from local edit./' \
  -e 's/KubeNodeNotReady/KubePodNotReady/' \
  -e 's/since: "2026-08-14T09:00:00Z"/since: "2026-08-14T08:00:00Z"/' \
  "$MD_FILE"
cat >> "$MD_FILE" <<'EOF'

::infrapad_block{block=new type=Core/Markdown}
# Additional notes from the investigation

This was a red-herring.
EOF
PUSH_OUT=$($CLI md push --file "$MD_FILE" 2>&1)
assert_contains "header block updated" "$PUSH_OUT" "Block 1 updated"
assert_contains "body block updated" "$PUSH_OUT" "Block 2 updated"
assert_contains "new block added" "$PUSH_OUT" "Block added"
assert_contains "file written" "$PUSH_OUT" "Written to $MD_FILE"
export_markdown "$DOC_ID" "$EXPECTED"
assert_same_file "refreshed file matches export" "$MD_FILE" "$EXPECTED"
REFRESHED=$(cat "$MD_FILE")
assert_contains "header edit persisted" "$REFRESHED" "KubePodNotReady"
assert_contains "header since persisted" "$REFRESHED" '2026-08-14T08:00:00Z'
assert_contains "header revision incremented" "$REFRESHED" "block=1 rev=3 type=Prometheus/AlertsMatcher"
assert_contains "body revision incremented" "$REFRESHED" "block=2 rev=3 type=Core/Markdown"
assert_contains "new block numbered" "$REFRESHED" "block=3 rev=1 type=Core/Markdown"
assert_not_contains "no block=new left" "$REFRESHED" "block=new"

PARSE_OUT=$($CLI md parse --file "$MD_FILE")
assert_contains "parsed pushed block count" "$PARSE_OUT" "Blocks (3):"
assert_contains "parsed pushed alerts type" "$PARSE_OUT" "type:   Prometheus/AlertsMatcher"
assert_contains "parsed pushed original content" "$PARSE_OUT" "root cause identified"
assert_contains "parsed pushed edit" "$PARSE_OUT" "Additional notes from local edit."
assert_contains "parsed new block number" "$PARSE_OUT" "block:  3"
assert_contains "parsed new block heading" "$PARSE_OUT" "# Additional notes from the investigation"
assert_contains "parsed new block content" "$PARSE_OUT" "This was a red-herring."
echo ""

# -----------------------------------------------------------------------
# 5. Add multiple new blocks in one push
# -----------------------------------------------------------------------
echo "Step 5: Add multiple new blocks in one push"
cat >> "$MD_FILE" <<'EOF'

::infrapad_block{type=Core/Markdown block=new}
# Investigation Summary

The payment service pod is in CrashLoopBackOff due to an OOM kill.

::infrapad_block{type=Core/Markdown block=new}
# Recommended Actions

1. Increase memory limit from 256Mi to 512Mi.
2. Add horizontal pod autoscaler.
EOF
PUSH_OUT=$($CLI md push --file "$MD_FILE" 2>&1)
assert_equals "push reports two blocks added" "$(echo "$PUSH_OUT" | grep -c 'Block added')" "2"
assert_not_contains "existing blocks not updated" "$PUSH_OUT" "updated"
assert_contains "file written" "$PUSH_OUT" "Written to $MD_FILE"
export_markdown "$DOC_ID" "$EXPECTED"
assert_same_file "multiple-block push matches export" "$MD_FILE" "$EXPECTED"
REFRESHED=$(cat "$MD_FILE")
assert_contains "block 4 numbered" "$REFRESHED" "block=4 rev=1 type=Core/Markdown"
assert_contains "block 5 numbered" "$REFRESHED" "block=5 rev=1 type=Core/Markdown"
assert_contains "header block revision unchanged" "$REFRESHED" "block=1 rev=3 type=Prometheus/AlertsMatcher"
assert_contains "body block revision unchanged" "$REFRESHED" "block=2 rev=3 type=Core/Markdown"
assert_contains "first added block revision unchanged" "$REFRESHED" "block=3 rev=1 type=Core/Markdown"
assert_not_contains "no block=new left" "$REFRESHED" "block=new"

PARSE_OUT=$($CLI md parse --file "$MD_FILE")
assert_contains "parsed five blocks" "$PARSE_OUT" "Blocks (5):"
assert_contains "parsed block 4 number" "$PARSE_OUT" "block:  4"
assert_contains "parsed block 5 number" "$PARSE_OUT" "block:  5"
assert_contains "parsed investigation heading" "$PARSE_OUT" "Investigation Summary"
assert_contains "parsed investigation content" "$PARSE_OUT" "OOM kill"
assert_contains "parsed actions heading" "$PARSE_OUT" "Recommended Actions"
assert_contains "parsed actions content" "$PARSE_OUT" "Increase memory limit"
echo ""

# -----------------------------------------------------------------------
# 6. Push without changes still refreshes the file
# -----------------------------------------------------------------------
echo "Step 6: Push without changes still refreshes the file"
NOOP_OUT=$($CLI md push --file "$MD_FILE" 2>&1)
assert_contains "no changes reported" "$NOOP_OUT" "No changes detected"
assert_contains "file written" "$NOOP_OUT" "Written to $MD_FILE"
assert_same_file "no-op push matches export" "$MD_FILE" "$EXPECTED"
echo ""

# -----------------------------------------------------------------------
# 7. Stale but untouched content refreshes to newer server state
# -----------------------------------------------------------------------
echo "Step 7: Stale but untouched content refreshes to newer server state"
$CLI block update --document "$DOC_ID" --block-number 2 --type Core/Markdown \
  --content '{"text": "concurrent server-side edit"}' > /dev/null
STALE_FILE="${TMPDIR_E2E}/stale.md"
cp "$MD_FILE" "$STALE_FILE"
NOOP_OUT=$($CLI md push --file "$MD_FILE" 2>&1)
assert_contains "stale no-op reported" "$NOOP_OUT" "No changes detected"
assert_contains "stale no-op written" "$NOOP_OUT" "Written to $MD_FILE"
export_markdown "$DOC_ID" "$EXPECTED"
assert_same_file "stale file refreshed to export" "$MD_FILE" "$EXPECTED"
assert_contains "refreshed with concurrent edit" "$(cat "$MD_FILE")" "concurrent server-side edit"
echo ""

# -----------------------------------------------------------------------
# 8. Editing a stale revision is rejected without changing either side
# -----------------------------------------------------------------------
echo "Step 8: Editing a stale revision is rejected without changing either side"
cp "$STALE_FILE" "$MD_FILE"
sed -i 's/Additional notes from local edit./Conflicting local edit./' "$MD_FILE"
cp "$MD_FILE" "${TMPDIR_E2E}/edited.md"
if ERR_OUT=$($CLI md push --file "$MD_FILE" 2>&1); then
  echo "  ✗ stale edit unexpectedly succeeded"
  fail=$((fail + 1))
else
  assert_contains "stale edit reports conflict" "$ERR_OUT" "HTTP 409"
fi
assert_same_file "local edits preserved" "$MD_FILE" "${TMPDIR_E2E}/edited.md"
export_markdown "$DOC_ID" "${TMPDIR_E2E}/server.md"
assert_same_file "failed push leaves server unchanged" "${TMPDIR_E2E}/server.md" "$EXPECTED"
echo ""

# -----------------------------------------------------------------------
# 9. Pull failures preserve the destination
# -----------------------------------------------------------------------
echo "Step 9: Pull failures preserve the destination"
if ERR_OUT=$($CLI md pull --document "does-not-exist-$$" --file "$MD_FILE" 2>&1); then
  echo "  ✗ pull of a missing document unexpectedly succeeded"
  fail=$((fail + 1))
else
  assert_contains "error reports export failure" "$ERR_OUT" "export document markdown"
fi
assert_same_file "failed pull preserves destination" "$MD_FILE" "${TMPDIR_E2E}/edited.md"
if ERR_OUT=$($CLI md pull --file "${TMPDIR_E2E}/does-not-exist.md" 2>&1); then
  echo "  ✗ file-only pull of a missing file unexpectedly succeeded"
  fail=$((fail + 1))
else
  assert_contains "missing file reports read error" "$ERR_OUT" "read file to extract document ID"
fi
echo ""

# -----------------------------------------------------------------------
# 10. Missing document IDs are reported before creating a client
# -----------------------------------------------------------------------
echo "Step 10: Missing document IDs are reported before creating a client"
BAD_FILE="${TMPDIR_E2E}/bad.md"
printf -- '---\ntitle: no id\n---\nbody\n' > "$BAD_FILE"
for command in pull push; do
  # An invalid API URL proves the frontmatter is checked first.
  if ERR_OUT=$(${INFRAPAD_CLI:-${SCRIPT_DIR}/../../infrapad} --api-url 'not a URL' md "$command" --file "$BAD_FILE" 2>&1); then
    echo "  ✗ $command without a document ID unexpectedly succeeded"
    fail=$((fail + 1))
  else
    assert_contains "$command reports missing document ID" "$ERR_OUT" "has no document ID"
  fi
done

# -----------------------------------------------------------------------
# Summary
# -----------------------------------------------------------------------
print_summary
