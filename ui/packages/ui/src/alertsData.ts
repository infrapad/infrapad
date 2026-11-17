// Network-independent types and helpers for rendering stored
// Prometheus/AlertsMatcher query results.

// ---- Types ----

/** A single label matcher from the AlertsMatcher block content. */
export interface LabelMatcher {
  [labelName: string]: string[];
}

/**
 * A `[unix_seconds, value]` sample pair as stored by the server. A `null`
 * value marks a timestamped sample without a numeric value (e.g. a
 * non-finite Prometheus result); it is distinct from zero.
 */
export type QuerySample = [number, number | null];

/** One stored time series. */
export interface QueryResultsSeries {
  labels: Record<string, string>;
  samples: QuerySample[];
}

/** Stored `query_results` of a Prometheus/AlertsMatcher block. */
export interface QueryResults {
  /** RFC3339 query start. */
  start: string;
  /** RFC3339 query end. */
  end: string;
  step_seconds: number;
  series: QueryResultsSeries[];
}

/** A lane state transition: from `t` (unix seconds) the lane is `active` or not. */
export interface LaneTransition {
  t: number;
  active: boolean;
}

// ---- Helpers ----

/**
 * Build a human-readable label string for a series, omitting common/noisy
 * labels like __name__, alertstate, job, instance.
 */
export function seriesLabel(labels: Record<string, string>): string {
  const skip = new Set([
    "__name__",
    "alertstate",
    "job",
    "instance",
    "severity",
  ]);
  const parts = Object.entries(labels)
    .filter(([k]) => !skip.has(k))
    .map(([k, v]) => `${k}="${v}"`)
    .join(", ");
  return parts || "ALERTS";
}

/** Convert an RFC3339 string to (possibly fractional) unix seconds. */
export function isoToUnix(iso: string): number {
  return new Date(iso).getTime() / 1000;
}

/**
 * Convert stored samples into step transitions over `[startS, endS]`.
 *
 * A sample with a positive value activates the lane for
 * `[ts, min(nextTs, ts + stepS, endS))`; zero/negative/null values (a null
 * sample still ends the preceding coverage), gaps beyond
 * one step, and time outside sample coverage stay inactive. Transitions are
 * derived from sample boundaries only (no iteration over the query grid).
 * The result always starts at `startS` and ends at `endS`, so it can be
 * rendered directly with step-after interpolation.
 */
export function samplesToTransitions(
  samples: QuerySample[],
  startS: number,
  endS: number,
  stepS: number,
): LaneTransition[] {
  const out: LaneTransition[] = [{ t: startS, active: false }];
  const push = (t: number, active: boolean) => {
    const last = out[out.length - 1];
    if (last.active === active) return;
    if (last.t === t) {
      // Zero-length segment: replace it, then drop a redundant repeat.
      out.pop();
      const prev = out[out.length - 1];
      if (prev && prev.active === active) return;
    }
    out.push({ t, active });
  };

  for (let i = 0; i < samples.length; i++) {
    const [ts, value] = samples[i];
    if (value === null || !(value > 0)) continue;
    let segEnd = Math.min(ts + stepS, endS);
    if (i + 1 < samples.length) segEnd = Math.min(segEnd, samples[i + 1][0]);
    const segStart = Math.max(ts, startS);
    if (segEnd <= segStart) continue;
    push(segStart, true);
    push(segEnd, false);
  }

  const last = out[out.length - 1];
  if (last.t < endS) out.push({ t: endS, active: last.active });
  return out;
}
