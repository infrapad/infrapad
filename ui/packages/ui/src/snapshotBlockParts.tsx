// Parts shared by stored query snapshot blocks (Prometheus/TimeSeriesChart,
// Loki/Logs): the always-visible query expression and the optional Markdown
// description.

import { SafeMarkdown } from "./Presentation.js";

/** The query expression as selectable, whitespace-preserving code. */
export function QueryExpression({ expression }: { expression: string }) {
  return (
    <pre className="infrapad-snapshot-expression" aria-label="Query expression">
      <code>{expression}</code>
    </pre>
  );
}

/** The optional Markdown description (`content.text`); nothing when empty. */
export function SnapshotDescription({ text }: { text?: string }) {
  if (!text?.trim()) return null;
  return (
    <div className="infrapad-snapshot-description">
      <SafeMarkdown text={text} />
    </div>
  );
}
