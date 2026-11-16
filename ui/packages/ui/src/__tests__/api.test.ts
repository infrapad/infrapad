import { describe, expect, it } from "vitest";

import { blockTimestamp, type InfrapadBlock } from "../api.js";

function block(record: InfrapadBlock["record"]): InfrapadBlock {
  return {
    name: "documents/doc-1/blocks/1",
    blockNumber: 1,
    revisionNumber: 1,
    type: "Core/Markdown",
    createdAt: "2025-02-01T12:00:00Z",
    record,
  };
}

describe("blockTimestamp", () => {
  it("uses the standalone event time instead of the database creation time", () => {
    expect(blockTimestamp(block({ timestamp: "2025-01-02T03:04:05Z", content: {} })))
      .toBe("2025-01-02T03:04:05Z");
  });

  it("uses the first grouped member, matching the server's ordering anchor", () => {
    expect(blockTimestamp(block({ items: [
      { timestamp: "2025-01-02T03:05:00Z", key: "first", content: {} },
      { timestamp: "2025-01-02T03:06:00Z", key: "second", content: {} },
    ] }))).toBe("2025-01-02T03:05:00Z");
  });

  it("does not substitute database time when the event time is absent", () => {
    expect(blockTimestamp(block({ content: {} }))).toBeUndefined();
    expect(blockTimestamp(block({ items: [] }))).toBeUndefined();
  });
});
