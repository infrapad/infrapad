// Hosts provide both service locations; this package has no runtime globals.
export interface InfraPadServices {
  infrapadApiBaseUrl: string;
  prometheusApiBaseUrl: string;
}

/** Host-supplied fetch for document and Prometheus calls; ordinary fetch is the default. */
export type InfraPadFetch = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;

async function infrapadFetch<T>(
  baseUrl: string,
  path: string,
  hostFetch: InfraPadFetch,
): Promise<T> {
  const res = await hostFetch(`${baseUrl.replace(/\/$/, "")}${path}`);
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    const msg =
      (body as Record<string, string>).message ||
      (body as Record<string, string>).error ||
      `Infrapad API error (${res.status})`;
    throw new Error(msg);
  }
  return res.json();
}

// ---- Domain types (matching the swagger / proto definitions) ----

export interface InfrapadBlock {
  name: string;
  blockNumber: number;
  revisionNumber: number;
  authorId?: string;
  type: string;
  status?: string; // "progressing" | "published" | "deleted"
  createdAt?: string;
  content: Record<string, unknown>;
}

export interface InfrapadDocument {
  name: string; // "documents/{id}"
  status: string; // "active" | "archived"
  title: string;
  namespace?: string;
  createdAt?: string;
  blocks: InfrapadBlock[];
}

// ---- Get document ----

export interface GetDocumentResponse {
  document: InfrapadDocument;
}

export function getDocument(baseUrl: string, docId: string, hostFetch: InfraPadFetch = fetch): Promise<GetDocumentResponse> {
  return infrapadFetch(baseUrl, `/documents/${encodeURIComponent(docId)}`, hostFetch);
}

// ---- List documents ----

export interface ListDocumentsResponse {
  documents: InfrapadDocument[];
}

export function listDocuments(baseUrl: string, hostFetch: InfraPadFetch = fetch): Promise<ListDocumentsResponse> {
  return infrapadFetch(baseUrl, "/documents", hostFetch);
}

// ---- Block history ----

export interface ListBlockHistoryResponse {
  blocks: InfrapadBlock[];
}

export function listBlockHistory(
  baseUrl: string,
  docId: string,
  blockNumber: number,
  hostFetch: InfraPadFetch = fetch,
): Promise<ListBlockHistoryResponse> {
  return infrapadFetch(
    baseUrl,
    `/documents/${encodeURIComponent(docId)}/blocks/${blockNumber}/history`,
    hostFetch,
  );
}
