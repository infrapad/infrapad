import jsYaml from "js-yaml";

// Network-independent types and helpers for rendering stored Loki/Logs
// snapshots. Timestamps are decimal Unix-nanosecond strings and are handled
// with BigInt so no precision is lost.

// ---- Types ----

/** Required query definition of a Loki/Logs block (provenance only). */
export interface LokiLogsQuery {
  expression: string;
  /** RFC3339 query start. */
  start: string;
  /** RFC3339 query end. */
  end: string;
  /** Requested entry limit; not a completeness guarantee. */
  limit: number;
  source_url?: string;
  datasource_uid?: string;
  datasource_name?: string;
}

/** Canonical server-normalized values; keep in sync with loki.Severity. */
export type LogSeverity = "trace" | "debug" | "info" | "warn" | "error" | "critical" | "fatal" | "unknown";

export interface LokiLogsEntry {
  /** Decimal Unix-nanosecond timestamp string. */
  timestamp: string;
  line: string;
  severity?: LogSeverity;
  structured_metadata?: Record<string, string>;
  /** Server-derived conventional message (`msg`, then `message`). */
  message?: string;
  /**
   * Server-derived complete JSON object of `line`; numbers JavaScript cannot
   * represent exactly arrive as decimal strings.
   */
  fields?: Record<string, unknown>;
}

export interface LokiLogsStream {
  labels: Record<string, string>;
  entries: LokiLogsEntry[];
}

/** Stored snapshot; `streams: []` is a successful empty result. */
export interface LokiLogsResults {
  streams: LokiLogsStream[];
}

/** Content of a Loki/Logs block. */
export interface LokiLogsContent {
  title?: string;
  query: LokiLogsQuery;
  /** Absent/null when no snapshot was submitted (or it was cleared). */
  query_results?: LokiLogsResults | null;
}

/** An entry flattened across streams, ready for display. */
export interface LogRow {
  /** Stable key: `<stream index>-<entry index>`. */
  key: string;
  ns: bigint;
  timestamp: string;
  line: string;
  /** Display text: the server-derived message, otherwise `line`. */
  message: string;
  /** Server-derived structured fields of `line`, when it is a JSON object. */
  fields?: Record<string, unknown>;
  /** Canonical severity supplied by the server. */
  severity?: LogSeverity;
  labels: Record<string, string>;
  structuredMetadata?: Record<string, string>;
}

// ---- Helpers ----

const NS_PER_MS = 1_000_000n;

/**
 * Flatten all stream entries into one oldest-first list. Ordering uses
 * exact BigInt timestamps; equal timestamps keep submitted stream/entry
 * order. Duplicates are retained and the stored content is not mutated.
 */
export function chronologicalRows(results: LokiLogsResults | null | undefined): LogRow[] {
  const rows: LogRow[] = [];
  (results?.streams ?? []).forEach((stream, s) => {
    (stream.entries ?? []).forEach((entry, e) => {
      rows.push({
        key: `${s}-${e}`,
        ns: BigInt(entry.timestamp),
        timestamp: entry.timestamp,
        line: entry.line,
        message: entry.message ?? entry.line,
        fields: entry.fields,
        severity: entry.severity,
        labels: stream.labels ?? {},
        structuredMetadata: entry.structured_metadata,
      });
    });
  });
  // Array.prototype.sort is stable, preserving submitted order for ties.
  return rows.sort((a, b) => (a.ns < b.ns ? -1 : a.ns > b.ns ? 1 : 0));
}

/** Split nanoseconds into floored milliseconds and a 0..999999 remainder. */
function splitNs(ns: bigint): { ms: bigint; subMs: bigint } {
  let ms = ns / NS_PER_MS;
  let subMs = ns % NS_PER_MS;
  if (subMs < 0n) {
    ms -= 1n;
    subMs += NS_PER_MS;
  }
  return { ms, subMs };
}

/** `YYYY-MM-DD HH:MM:SS.mmm` in UTC (milliseconds truncated, not rounded). */
export function formatNsMillis(ns: bigint): string {
  const iso = new Date(Number(splitNs(ns).ms)).toISOString();
  return `${iso.slice(0, 10)} ${iso.slice(11, 23)}`;
}

/** Exact `YYYY-MM-DD HH:MM:SS.nnnnnnnnn UTC`. */
export function formatNsExact(ns: bigint): string {
  const { ms, subMs } = splitNs(ns);
  const iso = new Date(Number(ms)).toISOString();
  return `${iso.slice(0, 10)} ${iso.slice(11, 23)}${subMs.toString().padStart(6, "0")} UTC`;
}

/** Map entries sorted by key, for stable key/value display. */
export function sortedPairs(map: Record<string, string> | undefined): [string, string][] {
  return Object.keys(map ?? {})
    .sort()
    .map((k) => [k, map![k]]);
}

/** Readable YAML of server-derived log fields (strings stay strings). */
export function formatLogFields(fields: Record<string, unknown>): string {
  return jsYaml.dump(fields, { lineWidth: -1, sortKeys: true }).trimEnd();
}
