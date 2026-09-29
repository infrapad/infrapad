import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";

import { InfraPadDocuments } from "../InfraPadDocuments.js";
import type { InfraPadFetch } from "../api.js";
import { useInfrapadClient, type InfrapadClient } from "../infrapadClient.js";

const { clients } = vi.hoisted(() => ({ clients: [] as InfrapadClient[] }));

// Exercise the real public mount and route, but collect its internal client without
// running page effects (the standalone browser journey covers the UI lifecycle).
vi.mock("../InfrapadDocsPage.js", async () => {
  const { useInfrapadClient } = await import("../infrapadClient.js");
  return { default: () => { clients.push(useInfrapadClient()); return null; } };
});
vi.mock("../InfrapadDocDetailPage.js", () => ({ default: () => null }));

afterEach(() => {
  vi.unstubAllGlobals();
  clients.length = 0;
});

it("binds all four operations to the supplied transport and isolates document mounts", async () => {
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
  const otherFetch = vi.fn<InfraPadFetch>(async () => Response.json({ documents: [] }));
  const mount = (api: string, prom: string, request: InfraPadFetch) =>
    renderToStaticMarkup(
      <MemoryRouter>
        <InfraPadDocuments services={{ infrapadApiBaseUrl: api, prometheusApiBaseUrl: prom }} fetch={request} />
      </MemoryRouter>,
    );

  mount("https://api.example/v1/", "https://prom.example/", hostFetch);
  mount("https://other.example/v1", "https://other-prom.example", otherFetch);
  const [client, otherClient] = clients;
  expect(client).not.toBe(otherClient);
  expect(await client.listDocuments()).toEqual({ documents: [] });
  expect((await client.getDocument("doc 1")).document.name).toBe("documents/doc 1");
  expect(await client.listBlockHistory("doc 1", 2)).toEqual({ blocks: [] });
  expect(await client.queryRange("ALERTS", 10, 20, "15s")).toEqual([]);
  expect(hostFetch.mock.calls.map(([input]) => String(input))).toEqual([
    "https://api.example/v1/documents",
    "https://api.example/v1/documents/doc%201",
    "https://api.example/v1/documents/doc%201/blocks/2/history",
    "https://prom.example/api/v1/query_range?query=ALERTS&start=10&end=20&step=15s",
  ]);
  expect(await otherClient.listDocuments()).toEqual({ documents: [] });
  expect(otherFetch).toHaveBeenCalledWith("https://other.example/v1/documents");
  expect(hostFetch).toHaveBeenCalledTimes(4);
  expect(globalThis.fetch).not.toHaveBeenCalled();

  // A token failure must propagate instead of falling back to anonymous fetch.
  otherFetch.mockRejectedValueOnce(new Error("missing token"));
  await expect(otherClient.queryRange("ALERTS", 10, 20, "15s")).rejects.toThrow("missing token");
  expect(globalThis.fetch).not.toHaveBeenCalled();
});

it("requires a configured document mount", () => {
  function UnprovidedConsumer() {
    useInfrapadClient();
    return null;
  }
  expect(() => renderToStaticMarkup(<UnprovidedConsumer />)).toThrow("requires InfraPadDocuments");
});
