/**
 * Pure diff-computation helpers for revision history.
 *
 * Uses Monaco's DefaultLinesDiffComputer for line-level diffs.
 * Monaco is a direct dependency of this package. Its diff module can be
 * imported directly without pulling in the full
 * editor / DOM infrastructure.
 *
 * The module re-exports a thin API that hides Monaco internals.  Consumers
 * receive simple, serialisable diff descriptors — no Monaco types leak out.
 */

import { linesDiffComputers } from "monaco-editor/editor/common/diff/linesDiffComputers.js";

// ---- Public types ----

/** A contiguous range of changed lines between two texts. */
export interface LineDiffChange {
  /** 1-based start line in the original text. */
  originalStart: number;
  /** Number of lines removed from the original (0 = pure insertion). */
  originalLength: number;
  /** 1-based start line in the modified text. */
  modifiedStart: number;
  /** Number of lines added in the modified text (0 = pure deletion). */
  modifiedLength: number;
  /** Lines removed from the original (convenience — already in `originalLines`). */
  removedLines: string[];
  /** Lines added in the modified (convenience — already in `modifiedLines`). */
  addedLines: string[];
}

/** Full result of a line diff between two texts. */
export interface LineDiffResult {
  /** All changed regions, sorted by position in the original text. */
  changes: LineDiffChange[];
  /** The original text split into lines. */
  originalLines: string[];
  /** The modified text split into lines. */
  modifiedLines: string[];
  /** True when the computation timed out (diff may be approximate). */
  hitTimeout: boolean;
}

// ---- Implementation ----

const MAX_COMPUTATION_MS = 1000;

/**
 * Compute a line-level diff between two multi-line strings.
 *
 * Uses Monaco's advanced diff algorithm (Myers + heuristic optimisations)
 * which produces high-quality diffs with move detection, well suited for
 * unified / side-by-side rendering later.
 */
export function computeLineDiff(
  original: string,
  modified: string,
): LineDiffResult {
  const originalLines = original.split("\n");
  const modifiedLines = modified.split("\n");

  const computer = linesDiffComputers.getDefault();
  const result = computer.computeDiff(originalLines, modifiedLines, {
    maxComputationTimeMs: MAX_COMPUTATION_MS,
    ignoreTrimWhitespace: true,
  });

  const changes: LineDiffChange[] = result.changes.map((c) => {
    const origStart = c.original.startLineNumber;
    const origEnd = c.original.endLineNumberExclusive;
    const modStart = c.modified.startLineNumber;
    const modEnd = c.modified.endLineNumberExclusive;

    return {
      originalStart: origStart,
      originalLength: origEnd - origStart,
      modifiedStart: modStart,
      modifiedLength: modEnd - modStart,
      removedLines: originalLines.slice(origStart - 1, origEnd - 1),
      addedLines: modifiedLines.slice(modStart - 1, modEnd - 1),
    };
  });

  return {
    changes,
    originalLines,
    modifiedLines,
    hitTimeout: result.hitTimeout,
  };
}

// ---- Unified diff formatting ----

/**
 * Format a LineDiffResult as a unified-diff-style array of annotated lines.
 * Each entry carries a type tag so the UI can colour it appropriately.
 *
 * Context lines (unchanged) surrounding each hunk are included for
 * readability (default 3, like `diff -u`).
 */
export interface UnifiedDiffLine {
  type: "context" | "added" | "removed" | "hunk-header";
  text: string;
  /** 1-based line number in the original (context/removed) or modified (added). */
  lineNumber?: number;
}

export function formatUnifiedDiff(
  diffResult: LineDiffResult,
  contextLines = 3,
): UnifiedDiffLine[] {
  const { changes, originalLines, modifiedLines } = diffResult;

  if (changes.length === 0) {
    // No changes — return all lines as context.
    return originalLines.map((text, i) => ({
      type: "context" as const,
      text,
      lineNumber: i + 1,
    }));
  }

  // Build hunks: group changes that are close together (within 2*contextLines).
  const hunks = groupChangesIntoHunks(
    changes,
    contextLines,
  );
  const lines: UnifiedDiffLine[] = [];

  for (const hunk of hunks) {
    // Hunk header (like @@ -a,b +c,d @@)
    const origRange = hunkOriginalRange(
      hunk,
      contextLines,
      originalLines.length,
    );
    const modRange = hunkModifiedRange(
      hunk,
      contextLines,
      modifiedLines.length,
    );
    lines.push({
      type: "hunk-header",
      text: `@@ -${origRange.start},${origRange.length} +${modRange.start},${modRange.length} @@`,
    });

    let origPos = origRange.start; // 1-based current position in original
    for (const change of hunk) {
      // Context lines before this change
      while (origPos < change.originalStart) {
        lines.push({
          type: "context",
          text: originalLines[origPos - 1],
          lineNumber: origPos,
        });
        origPos++;
      }
      // Removed lines
      for (const removed of change.removedLines) {
        lines.push({ type: "removed", text: removed, lineNumber: origPos });
        origPos++;
      }
      // Added lines
      let modPos = change.modifiedStart;
      for (const added of change.addedLines) {
        lines.push({ type: "added", text: added, lineNumber: modPos });
        modPos++;
      }
    }
    // Context lines after last change in hunk
    const endOrig = origRange.start + origRange.length;
    while (origPos < endOrig) {
      lines.push({
        type: "context",
        text: originalLines[origPos - 1],
        lineNumber: origPos,
      });
      origPos++;
    }
  }

  return lines;
}

// ---- Internal helpers ----

function groupChangesIntoHunks(
  changes: LineDiffChange[],
  contextLines: number,
): LineDiffChange[][] {
  if (changes.length === 0) return [];
  const gap = contextLines * 2;
  const hunks: LineDiffChange[][] = [[changes[0]]];

  for (let i = 1; i < changes.length; i++) {
    const prev = changes[i - 1];
    const curr = changes[i];
    const prevEnd = prev.originalStart + prev.originalLength;
    // If the gap between end of previous change and start of current change
    // is small enough, merge into same hunk.
    if (curr.originalStart - prevEnd <= gap) {
      hunks[hunks.length - 1].push(curr);
    } else {
      hunks.push([curr]);
    }
  }
  return hunks;
}

function hunkOriginalRange(
  hunk: LineDiffChange[],
  contextLines: number,
  totalLines: number,
): { start: number; length: number } {
  const first = hunk[0];
  const last = hunk[hunk.length - 1];
  const start = Math.max(1, first.originalStart - contextLines);
  const end = Math.min(
    totalLines + 1,
    last.originalStart + last.originalLength + contextLines,
  );
  return { start, length: end - start };
}

function hunkModifiedRange(
  hunk: LineDiffChange[],
  contextLines: number,
  totalLines: number,
): { start: number; length: number } {
  const first = hunk[0];
  const last = hunk[hunk.length - 1];
  const start = Math.max(1, first.modifiedStart - contextLines);
  const end = Math.min(
    totalLines + 1,
    last.modifiedStart + last.modifiedLength + contextLines,
  );
  return { start, length: end - start };
}
