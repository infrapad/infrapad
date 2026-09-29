// Prometheus HTTP API helpers for querying ALERTS metric data.

import type { InfraPadFetch } from "./api.js";

// ---- Types ----

/** A single label matcher from the infrapad alerts_matcher block content. */
export interface LabelMatcher {
  [labelName: string]: string[];
}

/** A single time-series result from Prometheus range query. */
export interface PromRangeSeries {
  metric: Record<string, string>;
  values: [number, string][]; // [unix_timestamp, value_string][]
}

/** Prometheus range query API response. */
interface PromRangeResponse {
  status: "success" | "error";
  data?: {
    resultType: "matrix";
    result: PromRangeSeries[];
  };
  error?: string;
  errorType?: string;
}

// ---- Query builder ----

/**
 * Build a PromQL selector from a single infrapad label matcher object.
 *
 * The infrapad `name` field maps to the Prometheus `alertname` label.
 * Single-value labels use `=`, multi-value labels use `=~"v1|v2"`.
 */
function matcherToSelector(matcher: LabelMatcher): string {
  const parts: string[] = [];
  for (const [key, values] of Object.entries(matcher)) {
    // Map infrapad "name" to Prometheus "alertname"
    const promLabel = key === "name" ? "alertname" : key;
    if (values.length === 1) {
      parts.push(`${promLabel}="${values[0]}"`);
    } else if (values.length > 1) {
      parts.push(`${promLabel}=~"${values.join("|")}"`);
    }
  }
  return parts.join(", ");
}

/**
 * Build a PromQL query string from infrapad alerts_matcher label matchers.
 *
 * Each matcher becomes `ALERTS{...}` and they are joined with ` or `.
 */
export function buildAlertsQuery(matchers: LabelMatcher[]): string {
  if (matchers.length === 0) return "ALERTS";
  const selectors = matchers.map(
    (m) => `ALERTS{${matcherToSelector(m)}}`,
  );
  return selectors.join(" or ");
}

// ---- API calls ----

/**
 * Execute a Prometheus range query.
 *
 * @param query  PromQL expression
 * @param start  Start time (ISO string or unix seconds)
 * @param end    End time (ISO string or unix seconds)
 * @param step   Query resolution step (e.g. "15s")
 */
export async function queryRange(
  baseUrl: string,
  query: string,
  start: string | number,
  end: string | number,
  step: string,
  hostFetch: InfraPadFetch = fetch,
): Promise<PromRangeSeries[]> {
  const params = new URLSearchParams({
    query,
    start: String(start),
    end: String(end),
    step,
  });

  const res = await hostFetch(`${baseUrl.replace(/\/$/, "")}/api/v1/query_range?${params}`);
  if (!res.ok) {
    throw new Error(`Prometheus API error (${res.status})`);
  }

  const body: PromRangeResponse = await res.json();
  if (body.status !== "success" || !body.data) {
    throw new Error(body.error ?? "Prometheus query failed");
  }

  return body.data.result;
}

// ---- Helpers ----

/**
 * Build a human-readable label string for a series, omitting common/noisy
 * labels like __name__, alertstate, job, instance.
 */
export function seriesLabel(metric: Record<string, string>): string {
  const skip = new Set([
    "__name__",
    "alertstate",
    "job",
    "instance",
    "severity",
  ]);
  const parts = Object.entries(metric)
    .filter(([k]) => !skip.has(k))
    .map(([k, v]) => `${k}="${v}"`)
    .join(", ");
  return parts || "ALERTS";
}

/**
 * Convert ISO date string to unix seconds.  Returns NaN for invalid input.
 */
export function isoToUnix(iso: string): number {
  return Math.floor(new Date(iso).getTime() / 1000);
}
