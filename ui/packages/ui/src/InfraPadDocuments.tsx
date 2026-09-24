import { Route, Routes } from "react-router-dom";
import InfrapadDocsPage from "./InfrapadDocsPage.js";
import InfrapadDocDetailPage from "./InfrapadDocDetailPage.js";
import type { InfraPadServices } from "./api.js";

export interface InfraPadDocumentsProps {
  services: InfraPadServices;
}

/** Routes below the host's wildcard mount; the host owns the router. */
export function InfraPadDocuments({ services }: InfraPadDocumentsProps) {
  return (
    <Routes>
      <Route index element={<InfrapadDocsPage services={services} />} />
      <Route path=":docId" element={<InfrapadDocDetailPage services={services} />} />
    </Routes>
  );
}
