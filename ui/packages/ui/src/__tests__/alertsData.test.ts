import { describe, expect, it } from "vitest";

import type { LaneTransition, QuerySample } from "../alertsData.js";
import { samplesToTransitions, seriesLabel } from "../alertsData.js";

const on = (t: number): LaneTransition => ({ t, active: true });
const off = (t: number): LaneTransition => ({ t, active: false });

describe("samplesToTransitions", () => {
  // Query window [100, 200] with a 10s step unless overridden.
  const cases: Array<{ name: string; samples: QuerySample[]; step?: number; want: LaneTransition[] }> = [
    { name: "empty samples stay inactive", samples: [], want: [off(100), off(200)] },
    { name: "nonpositive values stay inactive", samples: [[110, 0], [120, -1]], want: [off(100), off(200)] },
    { name: "positive sample covers one step", samples: [[110, 1]], want: [off(100), on(110), off(120), off(200)] },
    { name: "consecutive samples merge", samples: [[100, 1], [110, 2], [120, 0.5]], want: [on(100), off(130), off(200)] },
    { name: "gap beyond one step is inactive", samples: [[110, 1], [150, 1]], want: [off(100), on(110), off(120), on(150), off(160), off(200)] },
    { name: "next sample ends coverage early", samples: [[110, 1], [115, 0]], want: [off(100), on(110), off(115), off(200)] },
    { name: "null sample interrupts coverage", samples: [[110, 1], [115, null], [120, 1]], want: [off(100), on(110), off(115), on(120), off(130), off(200)] },
    { name: "all-null samples stay inactive", samples: [[110, null], [120, null]], want: [off(100), off(200)] },
    {
      name: "fractional off-grid timestamps and step",
      samples: [[103.25, 1], [105.75, 1]],
      step: 2.5,
      want: [off(100), on(103.25), off(108.25), off(200)],
    },
    { name: "clipped at query end", samples: [[195, 1], [200, 1]], want: [off(100), on(195), off(200)] },
  ];

  for (const tc of cases) {
    it(tc.name, () => {
      expect(samplesToTransitions(tc.samples, 100, 200, tc.step ?? 10)).toEqual(tc.want);
    });
  }
});

describe("seriesLabel", () => {
  it("returns label pairs, skipping noisy labels", () => {
    const labels = {
      __name__: "ALERTS",
      alertname: "MonitoredAppEndpointDown",
      alertstate: "firing",
      endpoint: "endpoint1",
      instance: "monitored-app:8080",
      job: "monitored-app",
      severity: "critical",
    };
    expect(seriesLabel(labels)).toBe('alertname="MonitoredAppEndpointDown", endpoint="endpoint1"');
  });

  it("returns 'ALERTS' when all labels are filtered out", () => {
    expect(seriesLabel({ __name__: "ALERTS", alertstate: "firing", severity: "warning" })).toBe("ALERTS");
  });
});
