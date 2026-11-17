// Shared styling for the stored-data charts.

/** Colors for series (PatternFly-inspired, using direct hex for SVG fill). */
export const SERIES_COLORS = [
  "#06c", // blue
  "#f0ab00", // gold
  "#3e8635", // green
  "#a18fff", // purple
  "#009596", // teal
  "#ec7a08", // orange
  "#c9190b", // red
  "#005f60", // dark cyan
];

// SVG colors inherit the host's current PatternFly theme.
export const TEXT_STYLE = {
  fill: "var(--pf-t--global--text--color--regular)",
  fontSize: 11,
  fontFamily: "RedHatText, Overpass, overpass, helvetica, arial, sans-serif",
};

export const AXIS_STYLE = { stroke: "var(--pf-t--global--border--color--default)" };

export const GRID_STYLE = {
  stroke: "var(--pf-t--global--border--color--default)",
  strokeDasharray: "4,4",
};
