// Network-independent types and helpers for rendering stored
// Prometheus/TimeSeriesChart snapshots.

import type { QueryResultsSeries, QuerySample } from "./alertsData.js";

// ---- Types ----

/** Required query definition of a Prometheus/TimeSeriesChart block. */
export interface TimeSeriesQuery {
  expression: string;
  /** Optional Prometheus base URL, stored for external tooling only. */
  source_url?: string;
  /** Optional opaque producer-owned datasource identifier. */
  datasource?: string;
  /** RFC3339 query start. */
  start: string;
  /** RFC3339 query end. */
  end: string;
  step_seconds: number;
}

/** Stored snapshot; `series: []` is a successful query with zero series. */
export interface TimeSeriesResults {
  series: QueryResultsSeries[];
}

/** Content of a Prometheus/TimeSeriesChart block. */
export interface TimeSeriesChartContent {
  query: TimeSeriesQuery;
  title?: string;
  /** Optional Markdown description. */
  text?: string;
  unit?: string;
  /** Absent/null when no snapshot was submitted (or it was cleared). */
  query_results?: TimeSeriesResults | null;
}

/** A numeric sample: `t` in unix seconds, `v` the raw value. */
export interface NumericPoint {
  t: number;
  v: number;
}

// ---- Helpers ----

/**
 * Split samples into runs of points that are connected by straight lines.
 * A run breaks at a null sample and whenever adjacent timestamps are more
 * than `stepS` apart. A single-point run is an isolated sample.
 */
export function sampleRuns(samples: QuerySample[], stepS: number): NumericPoint[][] {
  const runs: NumericPoint[][] = [];
  let current: NumericPoint[] | null = null;
  // Tolerate floating-point noise in fractional timestamps/steps.
  const maxGap = stepS * (1 + 1e-9);
  for (const [t, v] of samples) {
    if (v === null) {
      current = null;
      continue;
    }
    if (current && t - current[current.length - 1].t > maxGap) current = null;
    if (!current) {
      current = [];
      runs.push(current);
    }
    current.push({ t, v });
  }
  return runs;
}

/** Full Prometheus-style label set, e.g. `up{instance="a", job="b"}`. */
export function fullLabelSet(labels: Record<string, string>): string {
  const { __name__: name, ...rest } = labels;
  const parts = Object.keys(rest)
    .sort()
    .map((k) => `${k}="${rest[k]}"`);
  return `${name ?? ""}{${parts.join(", ")}}`;
}

/**
 * Compact, distinguishing legend names: only labels whose values differ
 * across series, in sorted key order (`instance=…, pod=…`). For a single
 * series or identical label sets, fall back to the full label set. Names
 * are made unique with a numeric suffix so every source series stays
 * identifiable.
 */
export function seriesDisplayNames(labelSets: Record<string, string>[]): string[] {
  const keys = new Set<string>();
  for (const labels of labelSets) for (const k of Object.keys(labels)) keys.add(k);
  const varying = [...keys].sort().filter((k) => {
    const first = labelSets[0]?.[k];
    return labelSets.some((labels) => labels[k] !== first);
  });

  const names = labelSets.map((labels) => {
    const compact = varying
      .filter((k) => k in labels)
      .map((k) => `${k}=${labels[k]}`)
      .join(", ");
    if (compact) return compact;
    const full = fullLabelSet(labels);
    return full === "{}" ? "series" : full;
  });

  const total = new Map<string, number>();
  for (const n of names) total.set(n, (total.get(n) ?? 0) + 1);
  const seen = new Map<string, number>();
  return names.map((n) => {
    if (total.get(n)! === 1) return n;
    const i = (seen.get(n) ?? 0) + 1;
    seen.set(n, i);
    return `${n} (${i})`;
  });
}

/**
 * Automatically fitted linear Y domain over numeric values, with a small
 * margin. Returns undefined when there are no values.
 */
export function fitYDomain(values: number[]): [number, number] | undefined {
  if (values.length === 0) return undefined;
  let min = Infinity;
  let max = -Infinity;
  for (const v of values) {
    if (v < min) min = v;
    if (v > max) max = v;
  }
  if (min === max) {
    const pad = Math.abs(min) * 0.1 || 1;
    return [min - pad, max + pad];
  }
  const pad = (max - min) * 0.05;
  return [min - pad, max + pad];
}

/** Full UTC timestamp, e.g. `2026-11-13 10:00:00 UTC` (ms only when nonzero). */
export function formatUtcTimestamp(date: Date): string {
  const iso = date.toISOString();
  const ms = iso.slice(19, 23);
  return `${iso.slice(0, 10)} ${iso.slice(11, 19)}${ms === ".000" ? "" : ms} UTC`;
}

/** UTC axis tick: `HH:MM`, prefixed with `MM-DD` for long windows. */
export function formatUtcTick(date: Date, includeDate: boolean): string {
  const iso = date.toISOString();
  return includeDate ? `${iso.slice(5, 10)} ${iso.slice(11, 16)}` : iso.slice(11, 16);
}
