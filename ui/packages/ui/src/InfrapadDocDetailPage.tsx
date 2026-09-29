import { Link, useParams } from "react-router-dom";
import {
  Alert,
  Breadcrumb,
  BreadcrumbItem,
  Bullseye,
  Button,
  Card,
  CardBody,
  CardHeader,
  Content,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  EmptyState,
  EmptyStateBody,
  EmptyStateFooter,
  Label,
  PageSection,
  Spinner,
  Title,
} from "@patternfly/react-core";
import DOMPurify from "dompurify";
import { marked } from "marked";
import { useCallback, useEffect, useState } from "react";

import AlertsTimelineChart from "./AlertsTimelineChart.js";
import type { InfraPadFetch, InfraPadServices, InfrapadBlock, InfrapadDocument } from "./api.js";
import { getDocument } from "./api.js";
import BlockRevisionsPanel, { formatDate } from "./BlockRevisionsPanel.js";
import type { LabelMatcher } from "./prometheusApi.js";

// ---- Helpers ----

function statusColor(
  status: string,
): "blue" | "green" | "grey" | "orange" | "red" {
  switch (status) {
    case "active":
      return "green";
    case "archived":
      return "grey";
    case "published":
      return "green";
    case "progressing":
      return "blue";
    case "deleted":
      return "red";
    default:
      return "blue";
  }
}

function blockTypeLabel(type: string): string {
  switch (type) {
    case "markdown":
      return "Markdown";
    case "alerts_matcher":
      return "Alert Matcher";
    default:
      return type;
  }
}

// ---- Block renderers ----

/** Render markdown content as sanitized HTML. */
function MarkdownBlockContent({ text }: { text: string }) {
  const html = DOMPurify.sanitize(marked.parse(text) as string);
  return (
    <div
      className="infrapad-markdown-content"
      dangerouslySetInnerHTML={{ __html: html }}
    />
  );
}

/** Render an alerts_matcher block as structured data + live timeline chart. */
function AlertsMatcherBlockContent({
  content,
  prometheusApiBaseUrl,
  fetch: hostFetch,
}: {
  content: Record<string, unknown>;
  prometheusApiBaseUrl: string;
  fetch?: InfraPadFetch;
}) {
  const matchers = content.LabelsMatchers as
    Array<Record<string, string[]>> | undefined;
  const since = content.Since as string | undefined;
  const until = content.Until as string | undefined;
  // "0001-01-01T00:00:00Z" is the Go zero time — treat as "ongoing"
  const isOngoing = !until || until.startsWith("0001-01-01");

  return (
    <>
      <DescriptionList isHorizontal isCompact>
        {matchers && matchers.length > 0 && (
          <DescriptionListGroup>
            <DescriptionListTerm>Matchers</DescriptionListTerm>
            <DescriptionListDescription>
              <div className="infrapad-alert-matchers">
                {matchers.map((m, i) => {
                  const parts = Object.entries(m)
                    .map(
                      ([k, v]) =>
                        `${k}=${Array.isArray(v) ? v.join(",") : v}`,
                    )
                    .join(", ");
                  return (
                    <Label
                      key={i}
                      color="blue"
                      isCompact
                      className="pf-v6-u-mr-xs pf-v6-u-mb-xs"
                    >
                      {parts}
                    </Label>
                  );
                })}
              </div>
            </DescriptionListDescription>
          </DescriptionListGroup>
        )}
        <DescriptionListGroup>
          <DescriptionListTerm>Since</DescriptionListTerm>
          <DescriptionListDescription>
            {since ? formatDate(since) : "—"}
          </DescriptionListDescription>
        </DescriptionListGroup>
        <DescriptionListGroup>
          <DescriptionListTerm>Until</DescriptionListTerm>
          <DescriptionListDescription>
            {isOngoing ? (
              <Label color="orange" isCompact>
                Ongoing
              </Label>
            ) : (
              formatDate(until)
            )}
          </DescriptionListDescription>
        </DescriptionListGroup>
      </DescriptionList>

      {/* Live alerts timeline chart from Prometheus */}
      {matchers && matchers.length > 0 && since && (
        <AlertsTimelineChart
          baseUrl={prometheusApiBaseUrl}
          fetch={hostFetch}
          matchers={matchers as LabelMatcher[]}
          since={since}
          until={isOngoing ? undefined : until}
        />
      )}
    </>
  );
}

/** Fallback renderer for unknown block types — show content as formatted JSON. */
function GenericBlockContent({
  content,
}: {
  content: Record<string, unknown>;
}) {
  return (
    <pre className="infrapad-json-content">
      {JSON.stringify(content, null, 2)}
    </pre>
  );
}

/** Render a single block's content based on its type. */
function renderBlockContent(block: InfrapadBlock, prometheusApiBaseUrl: string, hostFetch?: InfraPadFetch) {
  switch (block.type) {
    case "markdown":
      return (
        <MarkdownBlockContent text={(block.content.text as string) ?? ""} />
      );
    case "alerts_matcher":
      return <AlertsMatcherBlockContent content={block.content} prometheusApiBaseUrl={prometheusApiBaseUrl} fetch={hostFetch} />;
    default:
      return <GenericBlockContent content={block.content} />;
  }
}

function BlockCard({ block, docId, services, fetch: hostFetch }: { block: InfrapadBlock; docId: string; services: InfraPadServices; fetch?: InfraPadFetch }) {
  const [showRevisions, setShowRevisions] = useState(false);

  return (
    <Card isCompact className="infrapad-block-card">
      <CardHeader
        className="infrapad-block-header"
        actions={{
          actions: (
            <span className="infrapad-block-meta">
              <Button
                variant="link"
                isInline
                size="sm"
                className="infrapad-rev-toggle"
                onClick={() => setShowRevisions((prev) => !prev)}
              >
                rev {block.revisionNumber}
                {showRevisions ? " ▾" : " ▸"}
              </Button>
              {block.createdAt && <> · {formatDate(block.createdAt)}</>}
              {block.authorId && <> · {block.authorId}</>}
            </span>
          ),
          hasNoOffset: true,
        }}
      >
        <div className="infrapad-block-header-left">
          <Label color="purple" isCompact>
            {blockTypeLabel(block.type)}
          </Label>
          <Label color="orange" isCompact>
            {block.type === "alerts_matcher"
              ? "automation: Incident Detector"
              : "AI agent: investigator"}
          </Label>
        </div>
      </CardHeader>
      <CardBody>{renderBlockContent(block, services.prometheusApiBaseUrl, hostFetch)}</CardBody>
      {showRevisions && (
        <CardBody className="infrapad-revisions-panel">
          <Title headingLevel="h4" size="md" className="pf-v6-u-mb-sm">
            Revision History
          </Title>
          <BlockRevisionsPanel
            docId={docId}
            baseUrl={services.infrapadApiBaseUrl}
            blockNumber={block.blockNumber}
            fetch={hostFetch}
          />
        </CardBody>
      )}
    </Card>
  );
}

// ---- Page ----

export default function InfrapadDocDetailPage({ services, fetch: hostFetch }: { services: InfraPadServices; fetch?: InfraPadFetch }) {
  const { docId } = useParams<{ docId: string }>();
  const [document, setDocument] = useState<InfrapadDocument | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const fetchDocument = useCallback(async () => {
    if (!docId) return;
    setLoading(true);
    setError(null);
    try {
      const resp = await getDocument(services.infrapadApiBaseUrl, docId, hostFetch);
      if (!resp.document) throw new Error("Document not found");
      setDocument(resp.document);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to fetch document");
    } finally {
      setLoading(false);
    }
  }, [docId, services.infrapadApiBaseUrl, hostFetch]);

  useEffect(() => {
    fetchDocument();
  }, [fetchDocument]);

  if (loading) {
    return (
      <Bullseye>
        <Spinner />
      </Bullseye>
    );
  }

  if (error || !document) {
    return (
      <PageSection>
        <EmptyState titleText={error ?? "Document not found"} headingLevel="h1">
          <EmptyStateBody>
            The requested document could not be loaded.
          </EmptyStateBody>
          <EmptyStateFooter>
            <Link to=".." relative="path">Back to Documents</Link>
            <Button variant="link" onClick={fetchDocument}>Retry</Button>
          </EmptyStateFooter>
        </EmptyState>
      </PageSection>
    );
  }

  const blocks = document.blocks ?? [];

  return (
    <PageSection>
      <Breadcrumb className="pf-v6-u-mb-md">
        <BreadcrumbItem
          render={({ className, ariaCurrent }) => (
            <Link to=".." relative="path" className={className} aria-current={ariaCurrent}>
              Documents
            </Link>
          )}
        />
        <BreadcrumbItem isActive>{document.title}</BreadcrumbItem>
      </Breadcrumb>

      <div className="infrapad-doc-header">
        <Title headingLevel="h1">{document.title}</Title>
        <div className="infrapad-doc-meta pf-v6-u-mt-sm">
          <Label color={statusColor(document.status)} isCompact>
            {document.status}
          </Label>
          {document.namespace && (
            <Label color="blue" isCompact>
              {document.namespace}
            </Label>
          )}
          <Content component="small">
            Created {formatDate(document.createdAt)}
          </Content>
        </div>
      </div>

      {error && (
        <Alert
          variant="warning"
          title={error}
          isInline
          className="pf-v6-u-mb-md"
          actionClose={
            <Button variant="plain" onClick={fetchDocument}>
              Retry
            </Button>
          }
        />
      )}

      <div className="infrapad-blocks-list">
        {blocks.length === 0 ? (
          <EmptyState titleText="No blocks" headingLevel="h3">
            <EmptyStateBody>
              This document has no content blocks yet.
            </EmptyStateBody>
          </EmptyState>
        ) : (
          blocks.map((block) => (
            <BlockCard key={block.blockNumber} block={block} docId={docId!} services={services} fetch={hostFetch} />
          ))
        )}
      </div>
    </PageSection>
  );
}
