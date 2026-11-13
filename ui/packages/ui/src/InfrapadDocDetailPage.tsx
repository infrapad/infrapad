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
import { useCallback, useEffect, useState } from "react";

import AlertsTimelineChart from "./AlertsTimelineChart.js";
import { blockContent } from "./api.js";
import type { InfrapadBlock, InfrapadDocument } from "./api.js";
import { useInfrapadClient } from "./infrapadClient.js";
import BlockRevisionsPanel, { formatDate } from "./BlockRevisionsPanel.js";
import { BlockPresentation } from "./Presentation.js";
import type { LabelMatcher, QueryResults } from "./alertsData.js";

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
    case "Core/Markdown":
      return "Markdown";
    case "Prometheus/AlertsMatcher":
      return "Alert Matcher";
    default:
      return type;
  }
}

// ---- Block renderers ----

/** Format a stored query bound with second precision. */
function formatQueryBound(iso: string): string {
  return new Date(iso).toLocaleString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

/** Render an alerts_matcher block as structured data + stored timeline chart. */
function AlertsMatcherBlockContent({ content }: { content: Record<string, unknown> }) {
  const matchers = content.labels_matchers as LabelMatcher[] | undefined;
  const queryResults = (content.query_results ?? undefined) as QueryResults | undefined;
  const since = content.since as string | undefined;
  const until = content.until as string | undefined;
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
        {queryResults && (
          <>
            <DescriptionListGroup>
              <DescriptionListTerm>Snapshot window</DescriptionListTerm>
              <DescriptionListDescription className="infrapad-alerts-query-window">
                <time dateTime={queryResults.start}>{formatQueryBound(queryResults.start)}</time>
                {" – "}
                <time dateTime={queryResults.end}>{formatQueryBound(queryResults.end)}</time>
              </DescriptionListDescription>
            </DescriptionListGroup>
            <DescriptionListGroup>
              <DescriptionListTerm>Step</DescriptionListTerm>
              <DescriptionListDescription className="infrapad-alerts-query-step">
                {queryResults.step_seconds}s
              </DescriptionListDescription>
            </DescriptionListGroup>
          </>
        )}
      </DescriptionList>

      {/* Stored alerts timeline; never queries Prometheus. */}
      <AlertsTimelineChart queryResults={queryResults} />
    </>
  );
}

/**
 * Render a single block's content: registered extension content goes
 * through the host presentation path (cards, else sanitized Markdown); the
 * stored AlertsMatcher timeline is the explicit exception.
 */
function renderBlockContent(block: InfrapadBlock) {
  if (block.type === "Prometheus/AlertsMatcher") {
    return <AlertsMatcherBlockContent content={blockContent(block)} />;
  }
  return <BlockPresentation block={block} />;
}

function BlockCard({ block, docId }: { block: InfrapadBlock; docId: string }) {
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
            {block.type === "Prometheus/AlertsMatcher"
              ? "automation: Incident Detector"
              : "AI agent: investigator"}
          </Label>
        </div>
      </CardHeader>
      <CardBody>{renderBlockContent(block)}</CardBody>
      {showRevisions && (
        <CardBody className="infrapad-revisions-panel">
          <Title headingLevel="h4" size="md" className="pf-v6-u-mb-sm">
            Revision History
          </Title>
          <BlockRevisionsPanel
            docId={docId}
            blockNumber={block.blockNumber}
          />
        </CardBody>
      )}
    </Card>
  );
}

// ---- Page ----

export default function InfrapadDocDetailPage() {
  const client = useInfrapadClient();
  const { docId } = useParams<{ docId: string }>();
  const [document, setDocument] = useState<InfrapadDocument | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const fetchDocument = useCallback(async () => {
    if (!docId) return;
    setLoading(true);
    setError(null);
    try {
      const resp = await client.getDocument(docId);
      if (!resp.document) throw new Error("Document not found");
      setDocument(resp.document);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to fetch document");
    } finally {
      setLoading(false);
    }
  }, [docId, client]);

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
            <BlockCard key={block.blockNumber} block={block} docId={docId!} />
          ))
        )}
      </div>
    </PageSection>
  );
}
