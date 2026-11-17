import {
  Chart,
  ChartArea,
  ChartAxis,
  ChartThemeColor,
  ChartVoronoiContainer,
} from "@patternfly/react-charts/victory";
import { Alert } from "@patternfly/react-core";
import { useEffect, useMemo, useRef, useState } from "react";

import type { QueryResults } from "./alertsData.js";
import { isoToUnix, samplesToTransitions, seriesLabel } from "./alertsData.js";
import { SERIES_COLORS, TEXT_STYLE } from "./chartTheme.js";

// ---- Constants ----

/** Height per swimlane row (px). */
const LANE_HEIGHT = 50;

// ---- Chart data helpers ----

interface ChartDataPoint {
  x: Date;
  /** y encodes the lane: value = laneIndex when not firing, laneIndex+1 when firing. */
  y: number;
  name: string;
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

// ---- Props ----

export interface AlertsTimelineChartProps {
  /** Stored query results; absent when no snapshot was submitted. */
  queryResults?: QueryResults;
}

// ---- Component ----

/**
 * Render stored AlertsMatcher query results as swimlanes. Never queries
 * Prometheus: the window and resolution come from the stored results.
 */
export default function AlertsTimelineChart({ queryResults }: AlertsTimelineChartProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [chartWidth, setChartWidth] = useState(600);

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

  const startUnix = queryResults ? isoToUnix(queryResults.start) : 0;
  const endUnix = queryResults ? isoToUnix(queryResults.end) : 0;

  // Build swimlane data
  const lanes = useMemo(() => {
    if (!queryResults) return [];
    return queryResults.series.map((s, i) => {
      const label = seriesLabel(s.labels);
      const transitions = samplesToTransitions(s.samples, startUnix, endUnix, queryResults.step_seconds);
      return {
        label,
        color: SERIES_COLORS[i % SERIES_COLORS.length],
        data: transitions.map(({ t, active }): ChartDataPoint => ({
          x: new Date(t * 1000),
          y: active ? i + 1 : i,
          name: label,
        })),
      };
    });
  }, [queryResults, startUnix, endUnix]);

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

  if (!queryResults) {
    return (
      <div className="infrapad-alerts-chart-container" ref={containerRef}>
        <Alert variant="info" isInline isPlain title="No snapshot submitted." />
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
