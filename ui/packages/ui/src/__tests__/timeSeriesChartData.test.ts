import { describe, expect, it } from "vitest";

import { fitYDomain, formatUtcTimestamp, sampleRuns, seriesDisplayNames } from "../timeSeriesChartData.js";

describe("sampleRuns", () => {
  it("breaks at nulls and gaps beyond one step, keeping isolated points", () => {
    const runs = sampleRuns(
      [[0, 1], [60, 2], [120, null], [180, 3], [240, 4], [420, 5], [600, null]],
      60,
    );
    expect(runs).toEqual([
      [{ t: 0, v: 1 }, { t: 60, v: 2 }],
      [{ t: 180, v: 3 }, { t: 240, v: 4 }],
      [{ t: 420, v: 5 }],
    ]);
  });

  it("returns no runs for all-null samples and keeps zero values", () => {
    expect(sampleRuns([[0, null], [60, null]], 60)).toEqual([]);
    expect(sampleRuns([[0, 0]], 60)).toEqual([[{ t: 0, v: 0 }]]);
  });

  it("tolerates fractional off-grid timestamps within one step", () => {
    expect(sampleRuns([[0.1, 1], [2.6, 1], [5.2, 1]], 2.5)).toHaveLength(2);
  });
});

describe("seriesDisplayNames", () => {
  it("uses only varying labels and keeps series sharing a pod distinct", () => {
    const base = { __name__: "x", job: "etcd", namespace: "ns" };
    expect(seriesDisplayNames([
      { ...base, pod: "etcd-1", instance: "10.0.0.1:2381" },
      { ...base, pod: "etcd-1", instance: "10.0.0.2:2381" },
      { ...base, pod: "etcd-0", instance: "10.0.0.3:2381" },
    ])).toEqual([
      "instance=10.0.0.1:2381, pod=etcd-1",
      "instance=10.0.0.2:2381, pod=etcd-1",
      "instance=10.0.0.3:2381, pod=etcd-0",
    ]);
  });

  it("falls back to full labels for single or identical series", () => {
    expect(seriesDisplayNames([{ job: "a", __name__: "up" }])).toEqual(['up{job="a"}']);
    expect(seriesDisplayNames([{}, {}])).toEqual(["series (1)", "series (2)"]);
  });
});

describe("helpers", () => {
  it("fits the Y domain and formats UTC timestamps", () => {
    expect(fitYDomain([])).toBeUndefined();
    expect(fitYDomain([2, 2])).toEqual([1.8, 2.2]);
    expect(fitYDomain([0, 10])).toEqual([-0.5, 10.5]);
    expect(formatUtcTimestamp(new Date("2026-11-13T10:00:00Z"))).toBe("2026-11-13 10:00:00 UTC");
  });
});
