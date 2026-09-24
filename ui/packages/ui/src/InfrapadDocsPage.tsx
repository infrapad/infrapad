import { Link } from "react-router-dom";
import {
  Alert,
  Bullseye,
  Button,
  Content,
  EmptyState,
  EmptyStateBody,
  Label,
  PageSection,
  Spinner,
  Title,
  Tooltip,
} from "@patternfly/react-core";
import { SyncAltIcon } from "@patternfly/react-icons";
import {
  DataView,
  DataViewState,
} from "@patternfly/react-data-view/dist/dynamic/DataView";
import {
  DataViewTable,
  type DataViewTh,
  type DataViewTr,
} from "@patternfly/react-data-view/dist/dynamic/DataViewTable";
import { DataViewToolbar } from "@patternfly/react-data-view/dist/dynamic/DataViewToolbar";
import { useCallback, useEffect, useMemo, useState } from "react";

import { type InfraPadServices, type InfrapadDocument, listDocuments } from "./api.js";

const columns: DataViewTh[] = ["Title", "Status", "Namespace", "Created"];

function formatDate(iso?: string): string {
  if (!iso) return "—";
  return new Date(iso).toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  });
}

function statusColor(
  status: string,
): "blue" | "green" | "grey" | "orange" | "red" {
  switch (status) {
    case "active":
      return "green";
    case "archived":
      return "grey";
    default:
      return "blue";
  }
}

const InfrapadDocsPage = ({ services }: { services: InfraPadServices }) => {
  const [documents, setDocuments] = useState<InfrapadDocument[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const fetchDocuments = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await listDocuments(services.infrapadApiBaseUrl);
      setDocuments(resp.documents ?? []);
    } catch (err) {
      setError(
        err instanceof Error ? err.message : "Failed to fetch documents",
      );
    } finally {
      setLoading(false);
    }
  }, [services.infrapadApiBaseUrl]);

  useEffect(() => {
    fetchDocuments();
  }, [fetchDocuments]);

  const rows: DataViewTr[] = useMemo(
    () =>
      documents.map((document) => {
        // Extract document ID from resource name "documents/{id}"
        const docId = document.name.replace(/^documents\//, "");
        return [
          <Link key="title" to={encodeURIComponent(docId)}>
            <strong>{document.title}</strong>
          </Link>,
          <Label key="status" color={statusColor(document.status)} isCompact>
            {document.status}
          </Label>,
          document.namespace || "—",
          formatDate(document.createdAt),
        ];
      }),
    [documents],
  );

  if (loading) {
    return (
      <Bullseye>
        <Spinner />
      </Bullseye>
    );
  }

  return (
    <PageSection>
      <Title headingLevel="h1" className="pf-v6-u-mb-md">
        Infrapad
      </Title>
      <Content component="p" className="pf-v6-u-mb-lg">
        Notes about activities in the infrastructure managed by Infrapad.
      </Content>

      {error && (
        <Alert
          variant="warning"
          title={error}
          isInline
          className="pf-v6-u-mb-md"
          actionClose={
            <Button variant="plain" onClick={fetchDocuments}>
              Retry
            </Button>
          }
        />
      )}

      {!error && documents.length === 0 ? (
        <EmptyState titleText="No documents" headingLevel="h2">
          <EmptyStateBody>
            No documents found. Create one via the Infrapad CLI.
          </EmptyStateBody>
          <Button variant="primary" onClick={fetchDocuments}>
            Refresh
          </Button>
        </EmptyState>
      ) : (
        <DataView
          activeState={rows.length === 0 ? DataViewState.empty : undefined}
        >
          <DataViewToolbar
            actions={
              <Tooltip content="Refresh documents">
                <Button
                  variant="plain"
                  aria-label="Refresh documents"
                  onClick={fetchDocuments}
                >
                  <SyncAltIcon />
                </Button>
              </Tooltip>
            }
          />
          <DataViewTable
            aria-label="Infrapad documents"
            columns={columns}
            rows={rows}
          />
        </DataView>
      )}
    </PageSection>
  );
};

export default InfrapadDocsPage;
