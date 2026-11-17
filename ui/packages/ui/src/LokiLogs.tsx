import {
  Alert,
  Button,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  ExpandableSection,
  Title,
} from "@patternfly/react-core";
import { AngleDownIcon, AngleRightIcon } from "@patternfly/react-icons";
import { useLayoutEffect, useMemo, useRef, useState } from "react";

import type { LogRow, LokiLogsContent, LokiLogsQuery } from "./lokiLogsData.js";
import { chronologicalRows, formatLogFields, formatNsMillis, sortedPairs } from "./lokiLogsData.js";
import { formatUtcTimestamp } from "./timeSeriesChartData.js";

/**
 * Render a stored Loki/Logs snapshot as a compact oldest-first list. Never
 * queries Loki: the query and entries come from the stored content.
 */
export default function LokiLogs({ content }: { content: LokiLogsContent }) {
  const { query, title } = content;
  const results = content.query_results ?? undefined;
  const rows = useMemo(() => chronologicalRows(results), [results]);

  let body;
  if (!results) {
    body = <Message title="No snapshot submitted." />;
  } else if (rows.length === 0) {
    body = <Message title="No log entries found for this time range." />;
  } else {
    body = (
      <ol className="infrapad-logs-list" aria-label="Log entries" tabIndex={0}>
        {rows.map((row) => (
          <LogRowItem key={row.key} row={row} />
        ))}
      </ol>
    );
  }

  return (
    <div className="infrapad-logs">
      {title && (
        <Title headingLevel="h3" size="md" className="infrapad-logs-title">
          {title}
        </Title>
      )}
      {body}
      <QueryDetails query={query} />
    </div>
  );
}

function LogRowItem({ row }: { row: LogRow }) {
  const [detailsOpen, setDetailsOpen] = useState(false);
  const [messageOpen, setMessageOpen] = useState(false);
  const [overflows, setOverflows] = useState(false);
  const [originalOpen, setOriginalOpen] = useState(false);
  const messageRef = useRef<HTMLDivElement>(null);

  // Offer Show more only when the clamped preview actually hides content.
  useLayoutEffect(() => {
    const el = messageRef.current;
    if (!el || messageOpen) return;
    const check = () => setOverflows(el.scrollHeight > el.clientHeight + 1);
    check();
    const ro = new ResizeObserver(check);
    ro.observe(el);
    return () => ro.disconnect();
  }, [messageOpen, row.message]);

  const shortTime = formatNsMillis(row.ns);
  const labels = sortedPairs(row.labels);
  const metadata = sortedPairs(row.structuredMetadata);
  const fieldsYaml = useMemo(() => (row.fields ? formatLogFields(row.fields) : undefined), [row.fields]);

  return (
    <li
      className="infrapad-logs-row"
      data-severity={row.severity}
      onClick={(event) => {
        // Leave native text selection/copy and nested controls untouched.
        // The arrow remains the keyboard-accessible toggle for the row.
        if (event.defaultPrevented || event.button !== 0 || event.detail > 1 ||
            event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return;
        if (event.target instanceof Element &&
            event.target.closest("button, a, input, textarea, select, [role='button'], .infrapad-logs-details")) return;
        if (window.getSelection()?.isCollapsed === false) return;
        setDetailsOpen((v) => !v);
      }}
    >
      {row.severity && (
        <span
          className="infrapad-logs-level-bar"
          role="img"
          aria-label={`Level: ${row.severity}`}
          title={`Level: ${row.severity}`}
        />
      )}
      <div className="infrapad-logs-row-main">
        <button
          type="button"
          className="infrapad-logs-row-toggle"
          aria-expanded={detailsOpen}
          aria-label={detailsOpen ? "Hide entry details" : "Show entry details"}
          onClick={() => setDetailsOpen((v) => !v)}
        >
          {detailsOpen ? <AngleDownIcon /> : <AngleRightIcon />}
        </button>
        <time className="infrapad-logs-time" dateTime={`${shortTime.replace(" ", "T")}Z`} title="UTC">
          {shortTime}
        </time>
        <div className="infrapad-logs-message-wrap">
          <div
            ref={messageRef}
            className={`infrapad-logs-message${messageOpen ? "" : " infrapad-logs-message-clamped"}`}
          >
            {row.message}
          </div>
          {(overflows || messageOpen) && (
            <Button
              variant="link"
              isInline
              size="sm"
              className="infrapad-logs-more"
              aria-expanded={messageOpen}
              onClick={() => setMessageOpen((v) => !v)}
            >
              {messageOpen ? "Show less" : "Show more"}
            </Button>
          )}
        </div>
      </div>
      {detailsOpen && (
        <div className="infrapad-logs-details">
          <DescriptionList isHorizontal isCompact>
            {row.severity && (
              <DescriptionListGroup>
                <DescriptionListTerm>Level</DescriptionListTerm>
                <DescriptionListDescription>{row.severity}</DescriptionListDescription>
              </DescriptionListGroup>
            )}
            {fieldsYaml !== undefined && (
              <DescriptionListGroup>
                <DescriptionListTerm>Log fields</DescriptionListTerm>
                <DescriptionListDescription>
                  <pre className="infrapad-logs-yaml">{fieldsYaml}</pre>
                </DescriptionListDescription>
              </DescriptionListGroup>
            )}
            <DescriptionListGroup>
              <DescriptionListTerm>Stream labels</DescriptionListTerm>
              <DescriptionListDescription>
                <KeyValues pairs={labels} empty="No labels" />
              </DescriptionListDescription>
            </DescriptionListGroup>
            {metadata.length > 0 && (
              <DescriptionListGroup>
                <DescriptionListTerm>Structured metadata</DescriptionListTerm>
                <DescriptionListDescription>
                  <KeyValues pairs={metadata} empty="None" />
                </DescriptionListDescription>
              </DescriptionListGroup>
            )}
          </DescriptionList>
          {row.fields && (
            <ExpandableSection
              toggleText="Original line"
              isExpanded={originalOpen}
              onToggle={(_e, v) => setOriginalOpen(v)}
              className="infrapad-logs-original"
            >
              <pre className="infrapad-logs-yaml">{row.line}</pre>
            </ExpandableSection>
          )}
        </div>
      )}
    </li>
  );
}

function KeyValues({ pairs, empty }: { pairs: [string, string][]; empty: string }) {
  if (pairs.length === 0) return <span className="infrapad-logs-empty-kv">{empty}</span>;
  return (
    <dl className="infrapad-logs-kv">
      {pairs.map(([k, v]) => (
        <div key={k} className="infrapad-logs-kv-pair">
          <dt>{k}</dt>
          <dd>{v}</dd>
        </div>
      ))}
    </dl>
  );
}

/** Read-only stored query definition; never contacts the source. */
function QueryDetails({ query }: { query: LokiLogsQuery }) {
  const [expanded, setExpanded] = useState(false);
  return (
    <ExpandableSection
      toggleText="Query details"
      isExpanded={expanded}
      onToggle={(_e, v) => setExpanded(v)}
      className="infrapad-logs-query"
    >
      <DescriptionList isHorizontal isCompact>
        <DescriptionListGroup>
          <DescriptionListTerm>Expression</DescriptionListTerm>
          <DescriptionListDescription>
            <code className="infrapad-timeseries-expression">{query.expression}</code>
          </DescriptionListDescription>
        </DescriptionListGroup>
        <DescriptionListGroup>
          <DescriptionListTerm>Window</DescriptionListTerm>
          <DescriptionListDescription>
            <time dateTime={query.start}>{formatUtcTimestamp(new Date(query.start))}</time>
            {" – "}
            <time dateTime={query.end}>{formatUtcTimestamp(new Date(query.end))}</time>
          </DescriptionListDescription>
        </DescriptionListGroup>
        <DescriptionListGroup>
          <DescriptionListTerm>Requested limit</DescriptionListTerm>
          <DescriptionListDescription>{query.limit}</DescriptionListDescription>
        </DescriptionListGroup>
        {query.source_url && (
          <DescriptionListGroup>
            <DescriptionListTerm>Source</DescriptionListTerm>
            <DescriptionListDescription>{query.source_url}</DescriptionListDescription>
          </DescriptionListGroup>
        )}
        {query.datasource_name && (
          <DescriptionListGroup>
            <DescriptionListTerm>Grafana datasource</DescriptionListTerm>
            <DescriptionListDescription>{query.datasource_name}</DescriptionListDescription>
          </DescriptionListGroup>
        )}
        {query.datasource_uid && (
          <DescriptionListGroup>
            <DescriptionListTerm>Datasource UID</DescriptionListTerm>
            <DescriptionListDescription>
              <code>{query.datasource_uid}</code>
            </DescriptionListDescription>
          </DescriptionListGroup>
        )}
      </DescriptionList>
    </ExpandableSection>
  );
}

function Message({ title }: { title: string }) {
  return <Alert variant="info" isInline isPlain title={title} className="infrapad-timeseries-message" />;
}
