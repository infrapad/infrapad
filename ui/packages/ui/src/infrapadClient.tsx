import { createContext, type ReactNode, useContext } from "react";
import {
  getDocument,
  listBlockHistory,
  listDocuments,
  type InfraPadFetch,
  type InfraPadServices,
} from "./api.js";
import { queryRange } from "./prometheusApi.js";

// Internal, per-mount binding of the host's destinations and request function.
// Keep the plain API helpers responsible for URLs, parsing and errors.
export function createInfrapadClient(services: InfraPadServices, hostFetch?: InfraPadFetch) {
  const { infrapadApiBaseUrl, prometheusApiBaseUrl } = services;
  return {
    listDocuments: () => listDocuments(infrapadApiBaseUrl, hostFetch),
    getDocument: (docId: string) => getDocument(infrapadApiBaseUrl, docId, hostFetch),
    listBlockHistory: (docId: string, blockNumber: number) =>
      listBlockHistory(infrapadApiBaseUrl, docId, blockNumber, hostFetch),
    queryRange: (query: string, start: string | number, end: string | number, step: string) =>
      queryRange(prometheusApiBaseUrl, query, start, end, step, hostFetch),
  };
}

export type InfrapadClient = ReturnType<typeof createInfrapadClient>;

const InfrapadClientContext = createContext<InfrapadClient | null>(null);

export function InfrapadClientProvider({ client, children }: { client: InfrapadClient; children: ReactNode }) {
  return <InfrapadClientContext.Provider value={client}>{children}</InfrapadClientContext.Provider>;
}

export function useInfrapadClient(): InfrapadClient {
  const client = useContext(InfrapadClientContext);
  if (!client) throw new Error("InfraPad request client requires InfraPadDocuments");
  return client;
}
