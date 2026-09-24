import { describe, expect, it } from "vitest";

import {
  buildAlertsQuery,
  isoToUnix,
  seriesLabel,
} from "../prometheusApi.js";

describe("buildAlertsQuery", () => {
  it("returns bare ALERTS when matchers is empty", () => {
    expect(buildAlertsQuery([])).toBe("ALERTS");
  });

  it("builds a single-matcher query with name→alertname mapping", () => {
    const matchers = [{ name: ["MonitoredAppEndpointDown"], endpoint: ["endpoint1"] }];
    expect(buildAlertsQuery(matchers)).toBe(
      'ALERTS{alertname="MonitoredAppEndpointDown", endpoint="endpoint1"}',
    );
  });

  it("uses regex matcher for multi-value labels", () => {
    const matchers = [{ name: ["Alert1", "Alert2"] }];
    expect(buildAlertsQuery(matchers)).toBe(
      'ALERTS{alertname=~"Alert1|Alert2"}',
    );
  });

  it("joins multiple matchers with ' or '", () => {
    const matchers = [
      { name: ["MonitoredAppEndpointDown"], endpoint: ["endpoint1"] },
      { name: ["MonitoredAppEndpointDown"], endpoint: ["endpoint2"] },
    ];
    const result = buildAlertsQuery(matchers);
    expect(result).toBe(
      'ALERTS{alertname="MonitoredAppEndpointDown", endpoint="endpoint1"} or ' +
      'ALERTS{alertname="MonitoredAppEndpointDown", endpoint="endpoint2"}',
    );
  });

  it("passes through non-name labels unchanged", () => {
    const matchers = [{ severity: ["critical"], namespace: ["prod"] }];
    expect(buildAlertsQuery(matchers)).toBe(
      'ALERTS{severity="critical", namespace="prod"}',
    );
  });
});

describe("seriesLabel", () => {
  it("returns label pairs, skipping noisy labels", () => {
    const metric = {
      __name__: "ALERTS",
      alertname: "MonitoredAppEndpointDown",
      alertstate: "firing",
      endpoint: "endpoint1",
      instance: "monitored-app:8080",
      job: "monitored-app",
      severity: "critical",
    };
    expect(seriesLabel(metric)).toBe(
      'alertname="MonitoredAppEndpointDown", endpoint="endpoint1"',
    );
  });

  it("returns 'ALERTS' when all labels are filtered out", () => {
    const metric = {
      __name__: "ALERTS",
      alertstate: "firing",
      job: "prometheus",
      instance: "localhost:9090",
      severity: "warning",
    };
    expect(seriesLabel(metric)).toBe("ALERTS");
  });
});

describe("isoToUnix", () => {
  it("converts ISO string to unix seconds", () => {
    // 2026-08-18T15:36:12Z
    const unix = isoToUnix("2026-08-18T15:36:12Z");
    expect(unix).toBe(Math.floor(new Date("2026-08-18T15:36:12Z").getTime() / 1000));
  });

  it("returns NaN for invalid input", () => {
    expect(isoToUnix("not-a-date")).toBeNaN();
  });
});
