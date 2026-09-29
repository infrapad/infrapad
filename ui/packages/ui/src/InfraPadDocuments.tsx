import { Route, Routes } from "react-router-dom";
import InfrapadDocsPage from "./InfrapadDocsPage.js";
import InfrapadDocDetailPage from "./InfrapadDocDetailPage.js";
import type { InfraPadFetch, InfraPadServices } from "./api.js";

export interface InfraPadDocumentsProps {
  services: InfraPadServices;
  /** Optional host fetch for all document, revision, and Prometheus requests. */
  fetch?: InfraPadFetch;
}

/** Routes below the host's wildcard mount; the host owns the router. */
export function InfraPadDocuments({ services, fetch: hostFetch }: InfraPadDocumentsProps) {
  return (
    <Routes>
      <Route index element={<InfrapadDocsPage services={services} fetch={hostFetch} />} />
      <Route path=":docId" element={<InfrapadDocDetailPage services={services} fetch={hostFetch} />} />
    </Routes>
  );
}
