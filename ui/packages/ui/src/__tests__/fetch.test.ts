import { afterEach, expect, it, vi } from "vitest";

import { getDocument, listBlockHistory, listDocuments, type InfraPadFetch } from "../api.js";
import { queryRange } from "../prometheusApi.js";

afterEach(() => vi.unstubAllGlobals());

it("uses the host fetch for list, detail, revision history, and Prometheus queries", async () => {
  // None of these calls should silently bypass a host-supplied transport.
  vi.stubGlobal("fetch", vi.fn(() => { throw new Error("unexpected global fetch"); }));
  const hostFetch = vi.fn<InfraPadFetch>(async (input) => {
    const url = String(input);
    if (url.endsWith("/history")) return Response.json({ blocks: [] });
    if (url.includes("/api/v1/query_range?")) {
      return Response.json({ status: "success", data: { resultType: "matrix", result: [] } });
    }
    if (url.endsWith("/documents")) return Response.json({ documents: [] });
    return Response.json({ document: { name: "documents/doc 1", blocks: [] } });
  });

  expect(await listDocuments("https://api.example/v1/", hostFetch)).toEqual({ documents: [] });
  expect((await getDocument("https://api.example/v1", "doc 1", hostFetch)).document.name).toBe("documents/doc 1");
  expect(await listBlockHistory("https://api.example/v1", "doc 1", 2, hostFetch)).toEqual({ blocks: [] });
  expect(await queryRange("https://prom.example/", "ALERTS", 10, 20, "15s", hostFetch)).toEqual([]);

  expect(hostFetch).toHaveBeenCalledTimes(4);
  expect(hostFetch.mock.calls.map(([input]) => String(input))).toEqual([
    "https://api.example/v1/documents",
    "https://api.example/v1/documents/doc%201",
    "https://api.example/v1/documents/doc%201/blocks/2/history",
    "https://prom.example/api/v1/query_range?query=ALERTS&start=10&end=20&step=15s",
  ]);
});
