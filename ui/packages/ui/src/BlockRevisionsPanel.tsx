import {
  Alert,
  Content,
  Label,
  Spinner,
} from "@patternfly/react-core";
import jsYaml from "js-yaml";
import { useEffect, useMemo, useState } from "react";

import type { InfrapadBlock, ListBlockHistoryResponse } from "./api.js";
import { listBlockHistory } from "./api.js";
import { computeLineDiff, formatUnifiedDiff } from "./diffHelpers.js";
import type { UnifiedDiffLine } from "./diffHelpers.js";

// ---- Pure helpers (used by this panel and re-exported for other pages) ----

export function formatDate(iso?: string): string {
  if (!iso) return "\u2014";
  return new Date(iso).toLocaleString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

/**
 * Serialize a block's content to the textual format used in the infrapad
 * markdown representation.  This is used in the revision history view as
 * preparation for diff rendering.
 */
export function blockContentToText(block: InfrapadBlock): string {
  if (block.type === "markdown") {
    return (block.content.text as string) ?? "";
  }
  // Structured types: serialize content to YAML.
  // flowLevel: 3 keeps leaf arrays (e.g. label values) inline [val]
  // while expanding top-level structure for readability.
  return jsYaml.dump(block.content, { flowLevel: 3, lineWidth: -1 }).trimEnd();
}

/**
 * Sort block history responses into chronological order and filter out
 * single-revision blocks (no history to display).
 */
export function prepareRevisions(resp: ListBlockHistoryResponse): InfrapadBlock[] {
  // If only one revision exists (the current one), treat as no history.
  // Otherwise show all revisions (including current) in chronological order
  // to prepare for diff support between consecutive revisions.
  const all = resp.blocks
    .slice()
    .sort((a, b) => a.revisionNumber - b.revisionNumber);
  return all.length <= 1 ? [] : all;
}

// ---- Components ----

/** Render block content in its textual/YAML form (for revision history). */
function RevisionTextContent({ block }: { block: InfrapadBlock }) {
  const text = blockContentToText(block);
  return <pre className="infrapad-revision-text-content">{text}</pre>;
}

/** Render a unified diff between two revisions with color-coded lines. */
function RevisionDiffContent({
  previous,
  current,
}: {
  previous: InfrapadBlock;
  current: InfrapadBlock;
}) {
  const diffLines = useMemo(() => {
    const oldText = blockContentToText(previous);
    const newText = blockContentToText(current);
    const diff = computeLineDiff(oldText, newText);
    return formatUnifiedDiff(diff);
  }, [previous, current]);

  return (
    <pre className="infrapad-revision-diff-content">
      {diffLines.map((line, i) => (
        <DiffLine key={i} line={line} />
      ))}
    </pre>
  );
}

/** Single line within a unified diff view. */
function DiffLine({ line }: { line: UnifiedDiffLine }) {
  const prefix =
    line.type === "added"
      ? "+"
      : line.type === "removed"
        ? "-"
        : line.type === "hunk-header"
          ? ""
          : " ";
  return (
    <span className={`infrapad-diff-line infrapad-diff-line--${line.type}`}>
      {prefix}{line.text}
    </span>
  );
}

/** Expandable revisions panel shown inside a block card. */
export default function BlockRevisionsPanel({
  docId,
  blockNumber,
  baseUrl,
}: {
  docId: string;
  blockNumber: number;
  baseUrl: string;
}) {
  const [revisions, setRevisions] = useState<InfrapadBlock[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);
    listBlockHistory(baseUrl, docId, blockNumber)
      .then((resp) => {
        if (!cancelled) {
          setRevisions(prepareRevisions(resp));
        }
      })
      .catch((err) => {
        if (!cancelled)
          setError(
            err instanceof Error ? err.message : "Failed to load history",
          );
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [baseUrl, docId, blockNumber]);

  if (loading) {
    return (
      <div className="infrapad-revisions-loading">
        <Spinner size="md" /> Loading revisions…
      </div>
    );
  }

  if (error) {
    return (
      <Alert variant="danger" title={error} isInline isPlain />
    );
  }

  if (!revisions || revisions.length === 0) {
    return (
      <Content component="small" className="pf-v6-u-color-200">
        No previous revisions.
      </Content>
    );
  }

  return (
    <div className="infrapad-revisions-list">
      {revisions.map((rev, i) => (
        <div key={rev.revisionNumber} className="infrapad-revision-entry">
          <div className="infrapad-revision-header">
            <Label color="grey" isCompact>
              rev {rev.revisionNumber}
            </Label>
            <span className="infrapad-block-meta">
              {rev.createdAt && formatDate(rev.createdAt)}
              {rev.authorId && <> · {rev.authorId}</>}
            </span>
          </div>
          <div className="infrapad-revision-content">
            {i === 0 ? (
              <RevisionTextContent block={rev} />
            ) : (
              <RevisionDiffContent
                previous={revisions[i - 1]}
                current={rev}
              />
            )}
          </div>
        </div>
      ))}
    </div>
  );
}
