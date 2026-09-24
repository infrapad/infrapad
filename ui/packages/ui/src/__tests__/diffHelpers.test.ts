import { describe, expect, it } from "vitest";

import { computeLineDiff, formatUnifiedDiff } from "../diffHelpers.js";

/** Render a unified diff as a compact patch string for easy assertion. */
function patchView(original: string, modified: string, context = 3): string {
  const diff = computeLineDiff(original, modified);
  const lines = formatUnifiedDiff(diff, context);
  return lines
    .map((l) => {
      switch (l.type) {
        case "hunk-header": return l.text;
        case "removed":     return `-${l.text}`;
        case "added":       return `+${l.text}`;
        case "context":     return ` ${l.text}`;
      }
    })
    .join("\n");
}

describe("diffHelpers", () => {
  it("returns no changes for identical texts", () => {
    const diff = computeLineDiff("a\nb", "a\nb");
    expect(diff.changes).toEqual([]);
  });

  it("represents insertion and deletion without dropping neighboring context", () => {
    expect(patchView("first\nlast", "first\nnew\nlast", 1)).toContain("+new");
    expect(patchView("first\nold\nlast", "first\nlast", 1)).toContain("-old");
  });

  it("produces a correct unified patch with multiple hunks", () => {
    // 12 lines, change line 2 and line 11 — far enough apart for separate hunks
    const orig = Array.from({ length: 12 }, (_, i) => `L${i + 1}`);
    const mod = [...orig];
    mod[1] = "CHANGED2";      // line 2: replacement
    mod.splice(3, 1);         // line 4: deletion
    mod[9] = "CHANGED11";     // line 11 (now index 9): replacement + insertion
    mod.splice(10, 0, "NEW"); // insert after changed line

    expect(patchView(orig.join("\n"), mod.join("\n"), 1)).toBe(
      [
        "@@ -1,5 +1,4 @@",
        " L1",
        "-L2",
        "+CHANGED2",
        " L3",
        "-L4",
        " L5",
        "@@ -10,3 +9,4 @@",
        " L10",
        "-L11",
        "+CHANGED11",
        "+NEW",
        " L12",
      ].join("\n"),
    );
  });
});
