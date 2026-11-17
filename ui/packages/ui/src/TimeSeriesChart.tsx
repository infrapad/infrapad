import {
  Chart,
  ChartAxis,
  ChartLine,
  ChartScatter,
  ChartThemeColor,
  ChartVoronoiContainer,
} from "@patternfly/react-charts/victory";
import {
  Alert,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  ExpandableSection,
  Title,
  Tooltip,
} from "@patternfly/react-core";
import { useEffect, useMemo, useRef, useState } from "react";

import { isoToUnix } from "./alertsData.js";
import { AXIS_STYLE, GRID_STYLE, SERIES_COLORS, TEXT_STYLE } from "./chartTheme.js";
import type { TimeSeriesChartContent, TimeSeriesQuery } from "./timeSeriesChartData.js";
import {
  fitYDomain,
  formatUtcTick,
  formatUtcTimestamp,
  fullLabelSet,
  sampleRuns,
  seriesDisplayNames,
} from "./timeSeriesChartData.js";

const CHART_HEIGHT = 280;

interface LinePoint {
  x: Date;
  y: number | null;
}

interface ScatterPoint {
  x: Date;
  y: number;
  name: string;
  isolated: boolean;
}

interface PreparedSeries {
  name: string;
  fullLabels: string;
  color: string;
  line: LinePoint[];
  points: ScatterPoint[];
}

/**
 * Render a stored Prometheus/TimeSeriesChart snapshot. Never queries
 * Prometheus: the window, step, and samples come from the stored content.
 */
export default function TimeSeriesChart({ content }: { content: TimeSeriesChartContent }) {
  const { query, title, unit } = content;
  const results = content.query_results ?? undefined;

  const series = useMemo(() => prepareSeries(content), [content]);
  const values = useMemo(() => series.flatMap((s) => s.points.map((p) => p.y)), [series]);

  let body;
  if (!results) {
    body = <Message title="No snapshot submitted." />;
  } else if (series.length === 0) {
    body = <Message title="No series found for this time range." />;
  } else if (values.length === 0) {
    body = (
      <>
        <Message title="No numeric samples in this snapshot." />
        <Legend series={series} />
      </>
    );
  } else {
    body = (
      <>
        <LineChart query={query} unit={unit} series={series} values={values} />
        <Legend series={series} />
      </>
    );
  }

  return (
    <div className="infrapad-timeseries">
      {title && (
        <Title headingLevel="h3" size="md" className="infrapad-timeseries-title">
          {title}
        </Title>
      )}
      {body}
      <QueryDetails query={query} />
    </div>
  );
}

/** Convert stored series into line data (null breaks) and hoverable points. */
function prepareSeries(content: TimeSeriesChartContent): PreparedSeries[] {
  const stored = content.query_results?.series ?? [];
  const names = seriesDisplayNames(stored.map((s) => s.labels));
  return stored.map((s, i) => {
    const runs = sampleRuns(s.samples, content.query.step_seconds);
    const line: LinePoint[] = [];
    const points: ScatterPoint[] = [];
    runs.forEach((run, r) => {
      // A null-y point ends the previous run so the line is not connected.
      if (r > 0) line.push({ x: new Date(run[0].t * 1000), y: null });
      for (const p of run) {
        const x = new Date(p.t * 1000);
        line.push({ x, y: p.v });
        points.push({ x, y: p.v, name: names[i], isolated: run.length === 1 });
      }
    });
    return {
      name: names[i],
      fullLabels: fullLabelSet(s.labels),
      color: SERIES_COLORS[i % SERIES_COLORS.length],
      line,
      points,
    };
  });
}

function LineChart({
  query,
  unit,
  series,
  values,
}: {
  query: TimeSeriesQuery;
  unit?: string;
  series: PreparedSeries[];
  values: number[];
}) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(600);
  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    const ro = new ResizeObserver((entries) => {
      for (const entry of entries) setWidth(Math.max(entry.contentRect.width, 300));
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const startS = isoToUnix(query.start);
  const endS = isoToUnix(query.end);
  const includeDate = endS - startS > 24 * 3600;
  const tickValues = useMemo(() => {
    const ticks: Date[] = [];
    for (let i = 0; i <= 6; i++) ticks.push(new Date((startS + ((endS - startS) * i) / 6) * 1000));
    return ticks;
  }, [startS, endS]);
  const yDomain = useMemo(() => fitYDomain(values), [values]);
  const unitSuffix = unit ? ` ${unit}` : "";

  return (
    <div className="infrapad-timeseries-chart" ref={containerRef}>
      <Chart
        height={CHART_HEIGHT}
        width={width}
        padding={{ top: 10, bottom: 50, left: 80, right: 30 }}
        domain={{ x: [new Date(startS * 1000), new Date(endS * 1000)], y: yDomain }}
        themeColor={ChartThemeColor.multiOrdered}
        containerComponent={
          <ChartVoronoiContainer
            // Lines contain null break markers; tooltips come from the points.
            voronoiBlacklist={series.map((_, i) => `line-${i}`)}
            labels={({ datum }: { datum: ScatterPoint }) =>
              `${datum.name}\n${formatUtcTimestamp(datum.x)}\n${datum.y}${unitSuffix}`}
            constrainToVisibleArea
          />
        }
      >
        <ChartAxis
          scale="time"
          tickValues={tickValues}
          tickFormat={(t: Date) => formatUtcTick(t, includeDate)}
          label="Time (UTC)"
          style={{ tickLabels: TEXT_STYLE, axisLabel: { ...TEXT_STYLE, padding: 30 }, axis: AXIS_STYLE, grid: GRID_STYLE }}
        />
        <ChartAxis
          dependentAxis
          label={unit}
          style={{ tickLabels: TEXT_STYLE, axisLabel: { ...TEXT_STYLE, padding: 60 }, axis: AXIS_STYLE, grid: GRID_STYLE }}
        />
        {series.map((s, i) => (
          <ChartLine
            key={`line-${i}`}
            name={`line-${i}`}
            data={s.line}
            style={{ data: { stroke: s.color, strokeWidth: 1.5 } }}
          />
        ))}
        {series.map((s, i) => (
          <ChartScatter
            key={`points-${i}`}
            name={`points-${i}`}
            data={s.points}
            // Connected samples are drawn by the line; isolated ones need a dot.
            size={({ datum }: { datum: ScatterPoint }) => (datum.isolated ? 3 : 0)}
            style={{ data: { fill: s.color } }}
          />
        ))}
      </Chart>
    </div>
  );
}

/** Legend with compact names; the full label set is shown on hover/focus. */
function Legend({ series }: { series: PreparedSeries[] }) {
  return (
    <ul className="infrapad-timeseries-legend" aria-label="Series">
      {series.map((s, i) => (
        <li key={i}>
          <Tooltip content={s.fullLabels}>
            <span className="infrapad-timeseries-legend-item" tabIndex={0}>
              <span className="infrapad-timeseries-legend-swatch" style={{ background: s.color }} />
              {s.name}
            </span>
          </Tooltip>
        </li>
      ))}
    </ul>
  );
}

/** Read-only stored query definition; never contacts the source. */
function QueryDetails({ query }: { query: TimeSeriesQuery }) {
  const [expanded, setExpanded] = useState(false);
  return (
    <ExpandableSection
      toggleText="Query details"
      isExpanded={expanded}
      onToggle={(_e, v) => setExpanded(v)}
      className="infrapad-timeseries-query"
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
          <DescriptionListTerm>Step</DescriptionListTerm>
          <DescriptionListDescription>{query.step_seconds}s</DescriptionListDescription>
        </DescriptionListGroup>
        {query.source_url && (
          <DescriptionListGroup>
            <DescriptionListTerm>Source</DescriptionListTerm>
            <DescriptionListDescription>{query.source_url}</DescriptionListDescription>
          </DescriptionListGroup>
        )}
      </DescriptionList>
    </ExpandableSection>
  );
}

function Message({ title }: { title: string }) {
  return <Alert variant="info" isInline isPlain title={title} className="infrapad-timeseries-message" />;
}
