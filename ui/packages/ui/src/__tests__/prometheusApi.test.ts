import { describe, expect, it } from "vitest";

import { buildAlertsQuery } from "../prometheusApi.js";

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
