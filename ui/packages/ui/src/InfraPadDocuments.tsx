import {
  Alert,
  Bullseye,
  Button,
  Card,
  CardBody,
  CardHeader,
  Content,
  EmptyState,
  EmptyStateBody,
  Label,
  PageSection,
  Spinner,
  Title,
} from "@patternfly/react-core";
import { useCallback, useEffect, useState, type ReactNode } from "react";
import { Link, Route, Routes, useParams } from "react-router-dom";

export interface InfraPadServices {
  infrapadApiBaseUrl: string;
  prometheusApiBaseUrl: string;
}

export interface InfrapadBlock {
  name: string;
  blockNumber: number;
  revisionNumber: number;
  authorId?: string;
  type: string;
  status?: string;
  createdAt?: string;
  content: Record<string, unknown>;
}

export interface InfrapadDocument {
  name: string;
  status: string;
  title: string;
  namespace?: string;
  createdAt?: string;
  blocks?: InfrapadBlock[];
}

export interface InfraPadDocumentsProps {
  services: InfraPadServices;
}

async function request<T>(baseUrl: string, path: string): Promise<T> {
  const response = await fetch(`${baseUrl}${path}`);
  if (!response.ok) {
    let message = `InfraPad API error (${response.status})`;
    try {
      const body = (await response.json()) as Record<string, unknown>;
      if (typeof body.message === "string") message = body.message;
      else if (typeof body.error === "string") message = body.error;
    } catch {
      // Keep the status-based message for non-JSON failures.
    }
    throw new Error(message);
  }
  return (await response.json()) as T;
}

function formatDate(value?: string): string {
  if (!value) return "—";
  return new Date(value).toLocaleString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function statusColor(
  status: string,
): "blue" | "green" | "grey" | "orange" | "red" {
  if (status === "active" || status === "published") return "green";
  if (status === "archived") return "grey";
  if (status === "deleted") return "red";
  if (status === "progressing") return "orange";
  return "blue";
}

function Loading() {
  return (
    <Bullseye className="infrapad-ui-loading">
      <Spinner aria-label="Loading documents" />
    </Bullseye>
  );
}

function DocumentsIndex({ services }: InfraPadDocumentsProps) {
  const [documents, setDocuments] = useState<InfrapadDocument[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const result = await request<{ documents?: InfrapadDocument[] }>(
        services.infrapadApiBaseUrl,
        "/documents",
      );
      setDocuments(result.documents ?? []);
    } catch (cause) {
      setError(
        cause instanceof Error ? cause.message : "Failed to fetch documents",
      );
    } finally {
      setLoading(false);
    }
  }, [services.infrapadApiBaseUrl]);

  useEffect(() => {
    void load();
  }, [load]);

  if (loading) return <Loading />;

  return (
    <PageSection>
      <Title headingLevel="h1" className="pf-v6-u-mb-md">
        InfraPad documents
      </Title>
      <Content component="p" className="pf-v6-u-mb-lg">
        Notes about activities in the infrastructure managed by InfraPad.
      </Content>
      {error ? (
        <Alert
          variant="warning"
          title={error}
          isInline
          actionLinks={
            <Button variant="link" onClick={() => void load()}>
              Retry
            </Button>
          }
        />
      ) : documents.length === 0 ? (
        <EmptyState titleText="No documents" headingLevel="h2">
          <EmptyStateBody>
            No documents found. Create one with the InfraPad CLI.
          </EmptyStateBody>
          <Button variant="primary" onClick={() => void load()}>
            Refresh
          </Button>
        </EmptyState>
      ) : (
        <div
          className="infrapad-ui-document-list"
          aria-label="InfraPad documents"
        >
          {documents.map((document) => {
            const documentId = document.name.replace(/^documents\//, "");
            return (
              <Card key={document.name} isCompact>
                <CardHeader>
                  <Title headingLevel="h2" size="lg">
                    <Link to={encodeURIComponent(documentId)}>
                      {document.title}
                    </Link>
                  </Title>
                </CardHeader>
                <CardBody className="infrapad-ui-document-meta">
                  <Label color={statusColor(document.status)} isCompact>
                    {document.status}
                  </Label>
                  <span>{document.namespace || "No namespace"}</span>
                  <span>Created {formatDate(document.createdAt)}</span>
                </CardBody>
              </Card>
            );
          })}
        </div>
      )}
    </PageSection>
  );
}

function renderBlock(block: InfrapadBlock): ReactNode {
  if (block.type === "markdown" && typeof block.content.text === "string") {
    return <div className="infrapad-ui-markdown">{block.content.text}</div>;
  }
  return (
    <pre className="infrapad-ui-json">
      {JSON.stringify(block.content, null, 2)}
    </pre>
  );
}

function DocumentDetail({ services }: InfraPadDocumentsProps) {
  const { docId } = useParams<{ docId: string }>();
  const [document, setDocument] = useState<InfrapadDocument | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (!docId) return;
    setLoading(true);
    setError(null);
    try {
      const result = await request<{ document?: InfrapadDocument }>(
        services.infrapadApiBaseUrl,
        `/documents/${encodeURIComponent(docId)}`,
      );
      if (!result.document) throw new Error("Document not found");
      setDocument(result.document);
    } catch (cause) {
      setError(
        cause instanceof Error ? cause.message : "Failed to fetch document",
      );
    } finally {
      setLoading(false);
    }
  }, [docId, services.infrapadApiBaseUrl]);

  useEffect(() => {
    void load();
  }, [load]);

  if (loading) return <Loading />;

  if (error || !document) {
    return (
      <PageSection>
        <EmptyState titleText={error ?? "Document not found"} headingLevel="h1">
          <EmptyStateBody>
            The requested document could not be loaded.
          </EmptyStateBody>
          <Link to=".." relative="path">
            Back to documents
          </Link>
        </EmptyState>
      </PageSection>
    );
  }

  return (
    <PageSection>
      <nav aria-label="Breadcrumb" className="infrapad-ui-breadcrumb">
        <Link to=".." relative="path">
          Documents
        </Link>
        <span aria-hidden="true"> / </span>
        <span>{document.title}</span>
      </nav>
      <Title headingLevel="h1">{document.title}</Title>
      <div className="infrapad-ui-document-meta pf-v6-u-mt-sm pf-v6-u-mb-lg">
        <Label color={statusColor(document.status)} isCompact>
          {document.status}
        </Label>
        {document.namespace && (
          <Label color="blue" isCompact>
            {document.namespace}
          </Label>
        )}
        <span>Created {formatDate(document.createdAt)}</span>
      </div>
      <div className="infrapad-ui-block-list">
        {(document.blocks ?? []).length === 0 ? (
          <EmptyState titleText="No blocks" headingLevel="h2">
            <EmptyStateBody>
              This document has no content blocks yet.
            </EmptyStateBody>
          </EmptyState>
        ) : (
          document.blocks?.map((block) => (
            <Card key={block.blockNumber} isCompact>
              <CardHeader>
                <Label color="purple" isCompact>
                  {block.type}
                </Label>
                <span className="infrapad-ui-block-meta">
                  revision {block.revisionNumber}
                </span>
              </CardHeader>
              <CardBody>{renderBlock(block)}</CardBody>
            </Card>
          ))
        )}
      </div>
    </PageSection>
  );
}

export function InfraPadDocuments({ services }: InfraPadDocumentsProps) {
  return (
    <Routes>
      <Route index element={<DocumentsIndex services={services} />} />
      <Route path=":docId" element={<DocumentDetail services={services} />} />
    </Routes>
  );
}
