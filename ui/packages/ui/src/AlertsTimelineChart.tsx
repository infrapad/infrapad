import {
  Chart,
  ChartArea,
  ChartAxis,
  ChartThemeColor,
  ChartVoronoiContainer,
} from "@patternfly/react-charts/victory";
import { Alert, Bullseye, Spinner } from "@patternfly/react-core";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import type { LabelMatcher, PromRangeSeries } from "./prometheusApi.js";
import {
  buildAlertsQuery,
  isoToUnix,
  seriesLabel,
} from "./prometheusApi.js";
import { useInfrapadClient } from "./infrapadClient.js";

// ---- Constants ----

/** Padding (seconds) to add before/after the incident window. */
const TIME_PAD_S = 120;

/** Prometheus rejects queries exceeding 11,000 points per series. */
const MAX_POINTS = 10_000;

/** Nice step candidates (seconds) used for longer incident windows. */
const STEP_CANDIDATES_S = [15, 30, 60, 120, 300, 600, 1800, 3600, 6 * 3600, 24 * 3600];

/**
 * Pick the smallest nice step that keeps the window within the point
 * budget, so long-running incidents do not exceed Prometheus's maximum
 * query resolution.
 */
function computeStepS(startUnix: number, endUnix: number): number {
  const rangeS = Math.max(endUnix - startUnix, 1);
  const minStep = Math.ceil(rangeS / MAX_POINTS);
  for (const step of STEP_CANDIDATES_S) {
    if (step >= minStep) return step;
  }
  return Math.ceil(minStep / (24 * 3600)) * 24 * 3600;
}

/** Format a step in seconds as a Prometheus duration string. */
function formatStep(stepS: number): string {
  if (stepS % 3600 === 0) return `${stepS / 3600}h`;
  if (stepS % 60 === 0) return `${stepS / 60}m`;
  return `${stepS}s`;
}

/** Height per swimlane row (px). */
const LANE_HEIGHT = 50;

/** Colors for series (PatternFly-inspired, using direct hex for SVG fill). */
const SERIES_COLORS = [
  "#06c", // blue
  "#f0ab00", // gold
  "#3e8635", // green
  "#a18fff", // purple
  "#009596", // teal
  "#ec7a08", // orange
  "#c9190b", // red
  "#005f60", // dark cyan
];

// ---- Chart data helpers ----

interface ChartDataPoint {
  x: Date;
  /** y encodes the lane: value = laneIndex when not firing, laneIndex+1 when firing. */
  y: number;
  name: string;
}

/**
 * Build chart data points for a single series in a swimlane row.
 *
 * The y value is offset by `laneIndex` so each series occupies its own
 * vertical band:  lane 0 → [0,1], lane 1 → [1,2], etc.
 */
function seriesToLaneData(
  firingSet: Set<number>,
  startUnix: number,
  endUnix: number,
  laneIndex: number,
  label: string,
  stepS: number,
): ChartDataPoint[] {
  const points: ChartDataPoint[] = [];
  for (let t = startUnix; t <= endUnix; t += stepS) {
    points.push({
      x: new Date(t * 1000),
      y: firingSet.has(t) ? laneIndex + 1 : laneIndex,
      name: label,
    });
  }
  return points;
}

// ---- Time formatting ----

function formatTimeLabel(date: Date, includeDate = false): string {
  const time = date.toLocaleTimeString([], {
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  });
  if (!includeDate) return time;
  return `${date.toLocaleDateString([], { month: "short", day: "numeric" })} ${time}`;
}

// SVG colors inherit the host's current PatternFly theme.
const TEXT_STYLE = {
  fill: "var(--pf-t--global--text--color--regular)",
  fontSize: 11,
  fontFamily: "RedHatText, Overpass, overpass, helvetica, arial, sans-serif",
};

// ---- Props ----

export interface AlertsTimelineChartProps {
  matchers: LabelMatcher[];
  since: string;
  until?: string;
}

// ---- Component ----

export default function AlertsTimelineChart({
  matchers,
  since,
  until,
}: AlertsTimelineChartProps) {
  const client = useInfrapadClient();
  const [seriesData, setSeriesData] = useState<PromRangeSeries[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const containerRef = useRef<HTMLDivElement>(null);
  const [chartWidth, setChartWidth] = useState(600);

  // Time bounds
  const isOngoing = !until || until.startsWith("0001-01-01");
  const startUnix = useMemo(() => isoToUnix(since) - TIME_PAD_S, [since]);
  // Freeze the ongoing window for this mounted chart; state updates must not refetch it.
  const endUnix = useMemo(() => isOngoing
    ? Math.floor(Date.now() / 1000) + TIME_PAD_S
    : isoToUnix(until) + TIME_PAD_S, [isOngoing, until]);
  const stepS = useMemo(() => computeStepS(startUnix, endUnix), [startUnix, endUnix]);

  // Responsive width
  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    const ro = new ResizeObserver((entries) => {
      for (const entry of entries) {
        setChartWidth(Math.max(entry.contentRect.width, 300));
      }
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  // Fetch from Prometheus
  const fetchData = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const query = buildAlertsQuery(matchers);
      const result = await client.queryRange(query, startUnix, endUnix, formatStep(stepS));
      setSeriesData(result);
    } catch (err) {
      setError(
        err instanceof Error ? err.message : "Failed to query Prometheus",
      );
    } finally {
      setLoading(false);
    }
  }, [client, matchers, startUnix, endUnix, stepS]);

  useEffect(() => {
    fetchData();
  }, [fetchData]);

  // Build swimlane data
  const lanes = useMemo(() => {
    return seriesData.map((s, i) => {
      const label = seriesLabel(s.metric);
      const firingSet = new Set(s.values.map(([ts]) => ts));
      return {
        label,
        color: SERIES_COLORS[i % SERIES_COLORS.length],
        data: seriesToLaneData(firingSet, startUnix, endUnix, i, label, stepS),
      };
    });
  }, [seriesData, startUnix, endUnix, stepS]);

  // Time axis ticks — aim for ~6
  const tickValues = useMemo(() => {
    const rangeS = endUnix - startUnix;
    const tickCount = Math.min(6, Math.max(2, Math.floor(rangeS / 60)));
    const tickStep = rangeS / tickCount;
    const ticks: Date[] = [];
    for (let i = 0; i <= tickCount; i++) {
      ticks.push(new Date((startUnix + i * tickStep) * 1000));
    }
    return ticks;
  }, [startUnix, endUnix]);

  if (loading) {
    return (
      <div className="infrapad-alerts-chart-container" ref={containerRef}>
        <Bullseye>
          <Spinner size="md" />
          <span className="pf-v6-u-ml-sm pf-v6-u-font-size-sm">
            Loading alerts data…
          </span>
        </Bullseye>
      </div>
    );
  }

  if (error) {
    return (
      <div className="infrapad-alerts-chart-container" ref={containerRef}>
        <Alert
          variant="warning"
          isInline
          isPlain
          title="Prometheus query failed"
        >
          {error}
        </Alert>
      </div>
    );
  }

  if (lanes.length === 0) {
    return (
      <div className="infrapad-alerts-chart-container" ref={containerRef}>
        <Alert
          variant="info"
          isInline
          isPlain
          title="No alert data found for this time range"
        />
      </div>
    );
  }

  const chartHeight = Math.max(120, 40 + lanes.length * LANE_HEIGHT + 40);

  return (
    <div className="infrapad-alerts-chart-container" ref={containerRef}>
      <Chart
        height={chartHeight}
        width={chartWidth}
        padding={{ top: 10, bottom: 40, left: 20, right: 20 }}
        domain={{
          x: [new Date(startUnix * 1000), new Date(endUnix * 1000)],
          y: [0, lanes.length],
        }}
        themeColor={ChartThemeColor.multiOrdered}
        containerComponent={
          <ChartVoronoiContainer
            labels={({ datum }: { datum: ChartDataPoint }) => {
              return `${datum.name}\n${formatTimeLabel(datum.x)}`;
            }}
            constrainToVisibleArea
          />
        }
      >
        {/* Time axis (bottom) */}
        <ChartAxis
          scale="time"
          tickValues={tickValues}
          tickFormat={(t: Date) => formatTimeLabel(t, endUnix - startUnix > 48 * 3600)}
          style={{
            tickLabels: TEXT_STYLE,
            axis: { stroke: "var(--pf-t--global--border--color--default)" },
            grid: { stroke: "var(--pf-t--global--border--color--default)", strokeDasharray: "4,4" },
          }}
        />

        {/* Y-axis — no tick labels; series info available via hover tooltip */}
        <ChartAxis
          dependentAxis
          tickValues={[]}
          style={{
            tickLabels: { fill: "none" },
            axis: { stroke: "var(--pf-t--global--border--color--default)" },
            grid: { stroke: "none" },
          }}
        />

        {/* One filled area per series lane */}
        {lanes.map((lane, i) => (
          <ChartArea
            key={i}
            data={lane.data}
            interpolation="stepAfter"
            style={{
              data: {
                fill: lane.color,
                fillOpacity: 0.45,
                stroke: lane.color,
                strokeWidth: 1.5,
              },
            }}
            // baseline at the bottom of this lane
            y0={() => i}
          />
        ))}
      </Chart>
    </div>
  );
}
