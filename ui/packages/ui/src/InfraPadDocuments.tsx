import { useMemo } from "react";
import { Route, Routes } from "react-router-dom";
import InfrapadDocsPage from "./InfrapadDocsPage.js";
import InfrapadDocDetailPage from "./InfrapadDocDetailPage.js";
import type { InfraPadFetch, InfraPadServices } from "./api.js";
import { createInfrapadClient, InfrapadClientProvider } from "./infrapadClient.js";

export interface InfraPadDocumentsProps {
  services: InfraPadServices;
  /** Optional host fetch for all document, revision, and Prometheus requests. */
  fetch?: InfraPadFetch;
}

/** Routes below the host's wildcard mount; the host owns the router. */
export function InfraPadDocuments({ services, fetch: hostFetch }: InfraPadDocumentsProps) {
  const client = useMemo(
    () => createInfrapadClient(services, hostFetch),
    [services.infrapadApiBaseUrl, services.prometheusApiBaseUrl, hostFetch],
  );
  return (
    <InfrapadClientProvider client={client}>
      <Routes>
        <Route index element={<InfrapadDocsPage />} />
        <Route path=":docId" element={<InfrapadDocDetailPage />} />
      </Routes>
    </InfrapadClientProvider>
  );
}
