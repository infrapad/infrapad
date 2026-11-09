#!/usr/bin/env bash
#
# E2E test for `infrapad md pull` delegating rendering to the server export.
#
# Pull output must be byte-for-byte identical to the server's
# GET /v1/documents/{id}/markdown `markdown` string.
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

# assert_same_file compares two files byte-for-byte (including trailing newlines).
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

# export_markdown writes the decoded export endpoint markdown for $1 into $2.
export_markdown() {
  curl --fail --silent --show-error \
    -H "Authorization: Bearer ${INFRAPAD_TOKEN}" \
    "${API_URL%/}/v1/documents/$1/markdown" | jq -j '.markdown' > "$2"
}

echo "=== Markdown Pull E2E Test ==="
echo ""

echo "Step 1: Create test documents"
create_test_doc
OTHER_DOC_ID=$DOC_ID
create_test_doc
echo "  doc: $DOC_ID (other: $OTHER_DOC_ID)"
EXPECTED="${TMPDIR_E2E}/expected.md"
export_markdown "$DOC_ID" "$EXPECTED"
assert_equals "export ends with a newline" "$(tail -c1 "$EXPECTED" | od -An -c | tr -d ' ')" '\n'
echo ""

echo "Step 2: Pull to a file and to stdout"
MD_FILE="${TMPDIR_E2E}/nested/dir/${DOC_ID}.md"
PULL_OUT=$($CLI md pull --document "$DOC_ID" --file "$MD_FILE" 2>&1)
assert_contains "pull reports output file" "$PULL_OUT" "Written to $MD_FILE"
assert_same_file "file matches export" "$MD_FILE" "$EXPECTED"

STDOUT_FILE="${TMPDIR_E2E}/stdout.md"
$CLI md pull --document "documents/$DOC_ID" > "$STDOUT_FILE"
assert_same_file "stdout matches export" "$STDOUT_FILE" "$EXPECTED"
echo ""

echo "Step 3: Refresh with --file only after damaging block content"
# Keep valid frontmatter; break everything after it.
awk 'n < 2 { print; if ($0 == "---") n++; next } { print "::infrapad_block{block=oops type=Unknown/Type"; exit }' \
  "$EXPECTED" > "$MD_FILE"
assert_contains "file damaged" "$(cat "$MD_FILE")" "Unknown/Type"
$CLI md pull --file "$MD_FILE" 2> /dev/null
assert_same_file "refreshed file matches export" "$MD_FILE" "$EXPECTED"
echo ""

echo "Step 4: Explicit --document overrides destination metadata"
OTHER_EXPECTED="${TMPDIR_E2E}/other-expected.md"
export_markdown "$OTHER_DOC_ID" "$OTHER_EXPECTED"
$CLI md pull --document "$OTHER_DOC_ID" --file "$MD_FILE" 2> /dev/null
assert_same_file "file holds the requested document" "$MD_FILE" "$OTHER_EXPECTED"
echo ""

echo "Step 5: Failed export preserves the destination"
cp "$MD_FILE" "${TMPDIR_E2E}/before.md"
if ERR_OUT=$($CLI md pull --document "does-not-exist-$$" --file "$MD_FILE" 2>&1); then
  echo "  ✗ pull of a missing document unexpectedly succeeded"
  fail=$((fail + 1))
else
  assert_contains "error reports export failure" "$ERR_OUT" "export document markdown"
fi
assert_same_file "destination unchanged" "$MD_FILE" "${TMPDIR_E2E}/before.md"
echo ""

echo "Step 6: Invalid frontmatter is reported before contacting the server"
BAD_FILE="${TMPDIR_E2E}/bad.md"
printf -- '---\ntitle: no id\n---\nbody\n' > "$BAD_FILE"
# An invalid API URL proves the server is never contacted.
if ERR_OUT=$(${INFRAPAD_CLI:-${SCRIPT_DIR}/../../infrapad} --api-url 'not a URL' md pull --file "$BAD_FILE" 2>&1); then
  echo "  ✗ pull without a document ID unexpectedly succeeded"
  fail=$((fail + 1))
else
  assert_contains "missing document ID reported" "$ERR_OUT" "has no document ID"
fi

print_summary
