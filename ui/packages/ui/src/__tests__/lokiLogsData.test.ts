import { describe, expect, it } from "vitest";

import type { LokiLogsResults } from "../lokiLogsData.js";
import { chronologicalRows, formatLogFields, formatNsExact, formatNsMillis } from "../lokiLogsData.js";

describe("chronologicalRows", () => {
  it("orders across streams by exact nanoseconds, keeping ties stable and duplicates", () => {
    const results: LokiLogsResults = {
      streams: [
        {
          labels: { pod: "a" },
          entries: [
            // Differ only beyond float64 precision.
            { timestamp: "1794567686759710825", line: "a-late" },
            { timestamp: "1794567686759710823", line: "a-tie" },
            { timestamp: "1794567686759710823", line: "a-tie" },
          ],
        },
        { labels: { pod: "b" }, entries: [{ timestamp: "1794567686759710823", line: "b-tie" }] },
        { labels: {}, entries: [] },
      ],
    };
    const before = JSON.stringify(results);
    const rows = chronologicalRows(results);
    expect(rows.map((r) => [r.line, r.labels.pod])).toEqual([
      ["a-tie", "a"],
      ["a-tie", "a"],
      ["b-tie", "b"],
      ["a-late", "a"],
    ]);
    expect(rows[3].timestamp).toBe("1794567686759710825");
    expect(JSON.stringify(results)).toBe(before);
    expect(chronologicalRows(null)).toEqual([]);
  });
});

describe("per-entry severity", () => {
  it("uses only the server-supplied field, leaving messages and source metadata alone", () => {
    const results: LokiLogsResults = { streams: [{
      labels: { level: "info" },
      entries: [
        { timestamp: "0", line: ' { "level": "debug" } ', severity: "warn", structured_metadata: { level: "error" } },
        { timestamp: "1", line: '{"level":"error"}' },
        { timestamp: "2", line: "plain", severity: "unknown" },
      ],
    }] };
    const before = JSON.stringify(results);
    const rows = chronologicalRows(results);
    expect(rows.map((r) => r.severity)).toEqual(["warn", undefined, "unknown"]);
    expect(rows[0].line).toBe(' { "level": "debug" } ');
    expect(JSON.stringify(results)).toBe(before);
  });
});

describe("structured messages", () => {
  it("displays the server message, falling back to the line, and formats fields as YAML", () => {
    const fields = { msg: "applied", nested: { big: "1794567686759710823", ok: true }, a: [1, "x"] };
    const rows = chronologicalRows({ streams: [{ labels: {}, entries: [
      { timestamp: "0", line: '{"msg":"applied"}', message: "applied", fields },
      { timestamp: "1", line: "plain <b>text</b>" },
    ] }] });
    expect(rows.map((r) => r.message)).toEqual(["applied", "plain <b>text</b>"]);
    expect(rows[0].line).toBe('{"msg":"applied"}');
    expect(rows[1].fields).toBeUndefined();
    expect(formatLogFields(fields)).toBe(
      ["a:", "  - 1", "  - x", "msg: applied", "nested:", "  big: '1794567686759710823'", "  ok: true"].join("\n"),
    );
  });
});

describe("timestamp formatting", () => {
  it("formats exact UTC nanoseconds and truncated milliseconds", () => {
    const ns = BigInt("1794567686759710823");
    expect(formatNsMillis(ns)).toBe("2026-11-13 11:01:26.759");
    expect(formatNsExact(ns)).toBe("2026-11-13 11:01:26.759710823 UTC");
  });

  it("handles pre-epoch timestamps", () => {
    // -1ns is the last nanosecond before the epoch.
    expect(formatNsExact(-1n)).toBe("1969-12-31 23:59:59.999999999 UTC");
    expect(formatNsMillis(-1n)).toBe("1969-12-31 23:59:59.999");
    expect(formatNsExact(-1_500_000n)).toBe("1969-12-31 23:59:59.998500000 UTC");
  });
});
