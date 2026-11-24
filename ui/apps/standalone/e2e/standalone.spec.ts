import { expect, test, type Page } from "@playwright/test";

const authPort = process.env.INFRAPAD_E2E_AUTH_PORT ?? "18089";
const authOrigin = `http://localhost:${authPort}`;

const markdownText = [
  "**Service recovered** after the deployment was rolled back. <script>window.infrapadXss = true</script>",
  "",
  "See [runbook](/runbooks/endpoint-down), [dashboard](https://grafana.example/d/1), and [bad link](javascript:window.infrapadXss=true).",
  '<a href="https://example.com/new-tab" target="_blank">kept html link</a> <img src="x" onerror="window.infrapadXss = true">',
  "",
  "# Summary",
  "## Evidence",
  "### Follow-up",
].join("\n");

const threadURL = "https://slack.example/archives/C1/p1735787045000100";
const agentMarkdown = "**Recommended next step before destructive cleanup:**\n\nRoot cause: <b>bad deploy</b>";
const attachmentPreviews = "\n\n> Attachment: **[Incident](https://example.com/incident)** · PagerDuty\n> **Status:** Resolved\n" +
  "\n> Attachment: **[Runbook](https://example.com/runbook)** · Docs\n> Troubleshooting preview";
const conversationItems = [
  { timestamp: "2025-01-02T03:05:00Z", key: "slack-message:1", content: { author: "Alice", text: "Is it **down**?" } },
  { timestamp: "2025-01-02T03:05:10Z", key: "slack-message:2", content: { author: "Alice", text: "Checking the dashboard." + attachmentPreviews } },
  { timestamp: "2025-01-02T03:05:20Z", key: "slack-message:3", content: { author: "Bob", text: "Confirmed." } },
  { timestamp: "2025-01-02T03:05:30Z", key: "slack-message:4", content: { author: "Alice", text: "Rolling back." } },
];
const conversationMarkdown = "- **Alice** · 2025-01-02 03:05:00 UTC  \n  Is it **down**?\n\n  Checking the dashboard.\n\n" +
  "  - [Incident](https://example.com/incident)\n\n  - [Runbook](https://example.com/runbook)\n" +
  "- **Bob** · 2025-01-02 03:05:20 UTC  \n  Confirmed.\n" +
  "- **Alice** · 2025-01-02 03:05:30 UTC  \n  Rolling back.\n";

// Server-shaped Slack/agent blocks (as produced by the registered presenters).
const slackBlocks = [
  {
    name: "documents/doc-1/blocks/3",
    blockNumber: 3,
    revisionNumber: 1,
    type: "Slack/Thread",
    record: { timestamp: "2025-01-02T03:04:06Z", content: { url: threadURL } },
    presentation: { cards: [{ title: "Slack thread", markdown: "", fields: [], links: [{ label: "Open in Slack", url: threadURL }] }] },
    markdown: `[Slack thread](${threadURL})\n`,
  },
  {
    name: "documents/doc-1/blocks/4",
    blockNumber: 4,
    revisionNumber: 1,
    type: "Slack/Conversation",
    parentBlockNumber: 3,
    record: { items: conversationItems },
    presentation: { cards: [{
      title: "Slack conversation",
      markdown: conversationMarkdown,
      fields: [],
      links: [],
    }] },
    markdown: conversationItems.map(({ timestamp, content }) =>
      `- **${content.author}** · ${timestamp.replace("T", " ").replace("Z", " UTC")}  \n  ${content.text}\n`).join(""),
  },
  {
    name: "documents/doc-1/blocks/5",
    blockNumber: 5,
    revisionNumber: 1,
    type: "AIAgent/Message",
    parentBlockNumber: 3,
    record: { timestamp: "2025-01-02T03:06:00Z", key: "slack-message:2", content: { agent: "investigation-agent", text: agentMarkdown } },
    presentation: { cards: [{
      title: "investigation-agent <b>plain</b>",
      markdown: agentMarkdown,
      fields: [{ label: "Confidence", value: "<i>high</i>" }, { label: "Scope", value: "us-east" }],
      links: [{ label: "Open source", url: threadURL }, { label: "Unsafe source", url: "javascript:window.infrapadXss=true" }],
    }] },
    markdown: agentMarkdown,
  },
];

const document = {
  name: "documents/doc-1",
  status: "active",
  title: "Incident response notes",
  namespace: "operations",
  createdAt: "2025-01-02T03:04:05Z",
  blocks: [
    {
      name: "documents/doc-1/blocks/1",
      blockNumber: 1,
      revisionNumber: 2,
      type: "Core/Markdown",
      status: "published",
      record: {
        timestamp: "2025-01-02T03:04:05Z",
        content: { text: markdownText },
      },
      // Core/Markdown has no presenter: the server omits presentation.
      presentation: null,
      markdown: markdownText,
    },
    {
      name: "documents/doc-1/blocks/2",
      blockNumber: 2,
      revisionNumber: 1,
      type: "Prometheus/AlertsMatcher",
      record: {
        timestamp: "2025-01-02T03:04:05Z",
        content: {
          labels_matchers: [{ alertname: ["EndpointDown"], severity: ["critical"] }],
          since: "2025-01-02T03:00:00Z",
          until: "2025-01-02T03:05:00Z",
          query_results: {
            start: "2025-01-02T02:58:00Z",
            end: "2025-01-02T03:07:00Z",
            step_seconds: 15,
            series: [
              { labels: { alertname: "EndpointDown", severity: "critical" }, samples: [[1735786800, 1], [1735786815, 1]] },
            ],
          },
        },
      },
    },
    ...slackBlocks,
  ],
};

type MockDocument = { name: string; blocks: Array<Record<string, unknown>> };

// Stub document responses while exercising the real auth proxy. Charts render
// stored query results, so any Prometheus range query is recorded as a failure.
async function mockServices(page: Page, doc: MockDocument = document) {
  const prometheusRequests: string[] = [];
  page.on("request", (request) => {
    const url = request.url();
    if (url.includes("/api/v1/query_range") || url.startsWith("http://localhost:9090")) prometheusRequests.push(url);
  });
  await page.route("**/v1/documents**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    const body = path.endsWith("/blocks/1/history")
      ? { blocks: [doc.blocks[0], { ...doc.blocks[0], revisionNumber: 1, record: { timestamp: "2025-01-02T03:04:05Z", content: { text: "Service down." } } }] }
      : path === "/v1/documents" ? { documents: [doc] } : { document: doc };
    await route.fulfill({ contentType: "application/json", body: JSON.stringify(body) });
  });
  return prometheusRequests;
}

// Host presentation: Markdown fallback, fixed card layout, sanitization, and
// the shared HTTP(S)/relative link policy (current tab).
async function expectPresentation(page: Page) {
  const fallback = page.locator(".infrapad-block-card").nth(0).locator(".infrapad-markdown-content");
  await expect(fallback.locator("strong")).toHaveText("Service recovered");
  await expect(fallback.locator("strong")).toHaveCSS("font-weight", "600");
  for (const heading of ["h1", "h2", "h3"]) {
    await expect(fallback.locator(heading)).toHaveCSS("font-weight", "800");
  }
  await expect(fallback.locator("script")).toHaveCount(0);
  await expect(fallback.getByRole("link", { name: "runbook" })).toHaveAttribute("href", "/runbooks/endpoint-down");
  await expect(fallback.getByRole("link", { name: "dashboard" })).toHaveAttribute("href", "https://grafana.example/d/1");
  await expect(fallback.getByRole("link", { name: "kept html link" })).toHaveAttribute("href", "https://example.com/new-tab");
  await expect(fallback.getByRole("link", { name: "kept html link" })).not.toHaveAttribute("target", /.*/);
  await expect(fallback.getByText("bad link")).toBeVisible();
  await expect(fallback.getByRole("link", { name: "bad link" })).toHaveCount(0);
  await expect(fallback.locator("img[onerror]")).toHaveCount(0);

  const cards = page.locator(".infrapad-presentation-card");
  await expect(cards).toHaveCount(3);
  await expect(cards.nth(0).getByRole("heading", { name: "Slack thread" })).toBeVisible();
  await expect(cards.nth(0).getByRole("link", { name: "Open in Slack" })).toHaveAttribute("href", threadURL);
  await expect(cards.nth(1).getByRole("heading", { name: "Slack conversation" })).toBeVisible();
  await expect(cards.nth(1).locator(".infrapad-markdown-content strong").first()).toHaveText("Alice");
  await expect(cards.nth(1).locator(".infrapad-markdown-content strong").first()).toHaveCSS("font-weight", "600");
  const conversation = cards.nth(1).locator(".infrapad-markdown-content");
  // Consecutive Alice messages share one header/bullet; after Bob, Alice
  // gets a new header. The follow-up remains a separate paragraph.
  const groups = conversation.locator(":scope > ul > li");
  await expect(groups).toHaveCount(3);
  await expect(groups.locator(":scope > p:first-child > strong:first-child")).toHaveText(["Alice", "Bob", "Alice"]);
  await expect(groups.first().locator(":scope > p")).toHaveCount(2);
  await expect(groups.first().locator(":scope > p").nth(1)).toHaveText("Checking the dashboard.");
  const attachments = groups.first().locator(":scope > ul > li");
  await expect(attachments).toHaveCount(2);
  await expect(attachments.getByRole("link")).toHaveText(["Incident", "Runbook"]);
  await expect(attachments.getByRole("link", { name: "Incident" })).toHaveAttribute("href", "https://example.com/incident");
  await expect(attachments.getByRole("link", { name: "Runbook" })).toHaveAttribute("href", "https://example.com/runbook");
  await expect(conversation.locator("blockquote")).toHaveCount(0);
  await expect(conversation).not.toContainText("Status:");
  await expect(conversation).not.toContainText("Troubleshooting preview");
  await expect(conversation.locator("br")).toHaveCount(3);
  const authorBounds = await conversation.getByText("Alice", { exact: true }).first().boundingBox();
  const messageBounds = await conversation.getByText("down", { exact: true }).boundingBox();
  expect(authorBounds).not.toBeNull();
  expect(messageBounds).not.toBeNull();
  expect(messageBounds!.y).toBeGreaterThanOrEqual(authorBounds!.y + authorBounds!.height);
  await expect(cards.nth(1).locator(".infrapad-presentation-fields")).toHaveCount(0);

  // Agent card: title → Markdown → fields → links, with plain-text title,
  // labels, and values, and unsafe links rendered as text.
  const agent = cards.nth(2);
  const sections = await agent.evaluate((node) => Array.from(node.children).map((child) => child.className));
  expect(sections.map((c) => c.match(/infrapad-presentation-card-title|infrapad-markdown-content|infrapad-presentation-fields|infrapad-presentation-links/)?.[0]))
    .toEqual(["infrapad-presentation-card-title", "infrapad-markdown-content", "infrapad-presentation-fields", "infrapad-presentation-links"]);
  await expect(agent.getByRole("heading")).toHaveText("investigation-agent <b>plain</b>");
  await expect(agent.locator(".infrapad-markdown-content strong")).toHaveText("Recommended next step before destructive cleanup:");
  await expect(agent.locator(".infrapad-markdown-content strong")).toHaveCSS("font-weight", "600");
  await expect(agent.locator(".infrapad-markdown-content b")).toHaveText("bad deploy");
  await expect(agent.locator(".infrapad-markdown-content b")).toHaveCSS("font-weight", "600");
  await expect(agent.locator(".pf-v6-c-description-list__group")).toHaveText(["Confidence<i>high</i>", "Scopeus-east"]);
  await expect(agent.getByRole("link", { name: "Open source" })).toHaveAttribute("href", threadURL);
  await expect(agent.getByRole("link", { name: "Open source" })).not.toHaveAttribute("target", /.*/);
  await expect(agent.getByRole("link", { name: "Unsafe source" })).toHaveCount(0);
  await expect(agent.getByText("Unsafe source")).toBeVisible();

  // No raw-JSON fallback for registered extension content.
  await expect(page.getByText('"author"')).toHaveCount(0);
  expect(await page.evaluate(() => (window as Window & { infrapadXss?: boolean }).infrapadXss)).toBeUndefined();
}

async function openAuthenticatedDocument(page: Page) {
  await page.goto("/ui/documents/doc-1");
  await expect(page).toHaveURL(/\/auth\?returnTo=/);
  await page.getByLabel("Username").fill("alice");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByRole("heading", { name: document.title })).toBeVisible();
}

test("dummy login opens and reloads an InfraPad document", async ({ page }) => {
  const prometheusRequests = await mockServices(page);

  // Vite's root resolves to the same canonical UI route as built Go serving.
  await page.goto("/");
  await expect(page.locator('input[name="returnTo"]')).toHaveValue("/ui/documents");

  // Unauthenticated deep links should retain their full return path.
  const deepPath = "/ui/documents/doc-1?view=history#revision-2";
  await page.goto(deepPath);

  await expect(page).toHaveURL(/\/auth\?returnTo=/);
  await expect(page.locator('input[name="returnTo"]')).toHaveValue(deepPath);

  // Even a tampered form cannot turn the local login into an open redirect.
  await page.locator('input[name="returnTo"]').evaluate((input: HTMLInputElement) => {
    input.value = "//example.com/steal";
  });
  await page.getByLabel("Username").fill("alice");
  await page.getByLabel("Email (optional)").fill("alice@example.com");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page).toHaveURL(`${authOrigin}/auth`);
  await page.getByRole("button", { name: "Log out" }).click();

  // A normal login should return to the document and show the authenticated user.
  await page.goto(deepPath);
  await expect(page.locator('input[name="returnTo"]')).toHaveValue(deepPath);
  await page.getByLabel("Username").fill("alice");
  await page.getByLabel("Email (optional)").fill("alice@example.com");
  await page.getByRole("button", { name: "Log in" }).click();

  await expect(page).toHaveURL(`${authOrigin}${deepPath}`);
  await expect(page.getByLabel("Signed in as alice")).toBeVisible();
  // The configuration is supplied by Go through Vite and the authentication proxy.
  const config = await page.evaluate(async () => (await fetch("/ui/config")).json());
  expect(config.identity).toEqual({ username: "alice", email: "alice@example.com" });
  expect(config.services).toEqual({ infrapadApiBaseUrl: "/v1" });
  await expect(page.getByRole("heading", { name: document.title })).toBeVisible();

  // List/detail navigation should render the document content.
  await page.getByRole("link", { name: "Documents" }).click();
  await expect(page).toHaveURL(`${authOrigin}/ui/documents`);
  await expect(page.getByRole("grid", { name: "Infrapad documents" })).toBeVisible();
  await expect(page.getByRole("columnheader", { name: "Namespace" })).toBeVisible();
  await expect(page.getByRole("link", { name: document.title })).toBeVisible();
  await page.getByRole("link", { name: document.title }).click();
  await expect(page).toHaveURL(`${authOrigin}/ui/documents/doc-1`);
  await expectPresentation(page);
  await expect(page.getByText("alertname=EndpointDown, severity=critical")).toBeVisible();
  await expect(page.locator(".infrapad-alerts-chart-container svg").first()).toBeVisible();
  await page.getByRole("button", { name: /rev 2/ }).click();
  await expect(page.getByText("Revision History")).toBeVisible();
  await expect(page.locator(".infrapad-revision-text-content")).toHaveText("Service down.");
  await expect(page.locator(".infrapad-diff-line--added").first()).toContainText("Service recovered");

  // Directly reloading the detail route should still load the document.
  await page.reload();
  await expect(page.getByRole("heading", { name: document.title })).toBeVisible();
  await expect(page.locator(".infrapad-alerts-chart-container svg").first()).toBeVisible();
  expect(prometheusRequests).toEqual([]);
});

test("block headers display ordering timestamps rather than database creation times", async ({ page }) => {
  const createdAt = "2025-02-01T12:00:00Z";
  const blocks = document.blocks.map((block) => ({ ...block, createdAt }));
  await mockServices(page, { ...document, blocks });
  await openAuthenticatedDocument(page);

  const headers = page.locator(".infrapad-block-header");
  for (const [index, block] of blocks.entries()) {
    const record = block.record;
    const timestamp = "items" in record ? record.items[0].timestamp : record.timestamp;
    const time = headers.nth(index).locator("time");
    await expect(time).toHaveAttribute("datetime", timestamp);
    const formatted = await page.evaluate((iso) => new Date(iso).toLocaleString(undefined, {
      year: "numeric", month: "short", day: "numeric", hour: "2-digit", minute: "2-digit",
    }), timestamp);
    await expect(time).toHaveText(formatted);
    await expect(time).not.toHaveAttribute("datetime", createdAt);
  }
});

test("alerts matchers render stored query results offline", async ({ page }) => {
  const matcher = (blockNumber: number, content: Record<string, unknown>) => ({
    name: `documents/doc-2/blocks/${blockNumber}`,
    blockNumber,
    revisionNumber: 1,
    type: "Prometheus/AlertsMatcher",
    record: { timestamp: "2025-01-02T03:04:05Z", content },
  });
  const queryResults = {
    start: "2025-01-02T03:00:00Z",
    end: "2025-01-02T03:10:00Z",
    step_seconds: 30,
    series: [
      { labels: { alertname: "EndpointDown" }, samples: [[1735786830.5, 1], [1735786860.5, 0]] },
      { labels: { alertname: "Quiet" }, samples: [[1735786830, 0]] },
      { labels: { alertname: "NoSamples" }, samples: [] },
    ],
  };
  const doc = {
    name: "documents/doc-2",
    status: "active",
    title: "Offline alerts",
    createdAt: "2025-01-02T03:04:05Z",
    blocks: [
      // Label-free ongoing fallback matcher (as emitted by PagerDuty) with stored results.
      matcher(1, { since: "2025-01-02T03:01:00Z", query_results: queryResults }),
      matcher(2, { labels_matchers: [{ alertname: ["Missing"] }], since: "2025-01-02T03:01:00Z" }),
      matcher(3, { labels_matchers: [{ alertname: ["Empty"] }], since: "2025-01-02T03:01:00Z", until: "2025-01-02T03:02:00Z",
        query_results: { ...queryResults, series: [] } }),
    ],
  };
  const prometheusRequests = await mockServices(page, doc);

  await page.goto("/ui/documents/doc-2");
  await page.getByLabel("Username").fill("alice");
  await page.getByRole("button", { name: "Log in" }).click();

  for (let pass = 0; pass < 2; pass++) {
    await expect(page.getByRole("heading", { name: doc.title })).toBeVisible();
    const cards = page.locator(".infrapad-block-card");
    await expect(cards).toHaveCount(3);

    // Populated: incident bounds and the fixed stored window/step are shown separately.
    const populated = cards.nth(0);
    // Match the description term, not concatenated card text ("Alert MatcherSince").
    await expect(populated.getByRole("term").filter({ hasText: /^Matchers$/ })).toHaveCount(0);
    await expect(cards.nth(1).getByRole("term").filter({ hasText: /^Matchers$/ })).toBeVisible();
    await expect(populated.getByText("Ongoing")).toBeVisible();
    await expect(populated.locator(".infrapad-alerts-query-window time").nth(0)).toHaveAttribute("datetime", queryResults.start);
    await expect(populated.locator(".infrapad-alerts-query-window time").nth(1)).toHaveAttribute("datetime", queryResults.end);
    await expect(populated.locator(".infrapad-alerts-query-step")).toHaveText("30s");
    const chart = populated.locator(".infrapad-alerts-chart-container svg").first();
    await expect(chart).toBeVisible();
    // One lane per series, including inactive ones (Victory draws fill + outline
    // paths per area); the axis starts at the stored start (no padding).
    await expect(chart.locator('path[style*="fill-opacity: 0.45"]')).toHaveCount(2 * queryResults.series.length);
    const firstTick = await page.evaluate((start) => new Date(start).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false }), queryResults.start);
    await expect(chart.locator("text").first()).toHaveText(firstTick);

    await expect(cards.nth(1).getByText("No snapshot submitted.")).toBeVisible();
    await expect(cards.nth(1).locator(".infrapad-alerts-query-window")).toHaveCount(0);
    await expect(cards.nth(2).getByText("No alert data found for this time range")).toBeVisible();
    await expect(cards.nth(2).locator(".infrapad-alerts-query-step")).toHaveText("30s");

    if (pass === 0) await page.reload();
  }
  expect(prometheusRequests).toEqual([]);
});

test("time series charts render stored snapshots offline", async ({ page }) => {
  const query = {
    expression: "histogram_quantile(0.99, rate(etcd_wal_bucket[5m]))",
    source_url: "https://prometheus.example.com",
    start: "2026-11-13T10:00:00Z",
    end: "2026-11-13T11:00:00Z",
    step_seconds: 60,
  };
  const t0 = 1794564000; // 2026-11-13T10:00:00Z
  const base = { __name__: "wal", job: "etcd" };
  const chart = (blockNumber: number, content: Record<string, unknown>) => ({
    name: `documents/doc-3/blocks/${blockNumber}`,
    blockNumber,
    revisionNumber: 1,
    type: "Prometheus/TimeSeriesChart",
    record: { timestamp: "2026-11-13T11:00:00Z", content: { query, ...content } },
  });
  const doc = {
    name: "documents/doc-3",
    status: "active",
    title: "Stored charts",
    createdAt: "2026-11-13T11:00:00Z",
    blocks: [
      chart(1, {
        title: "WAL fsync p99",
        unit: "s",
        query_results: { series: [
          // Null and missing-interval gaps, plus an isolated sample.
          { labels: { ...base, pod: "etcd-1", instance: "10.0.0.1:2381" },
            samples: [[t0, 0.01], [t0 + 60, 0.012], [t0 + 120, null], [t0 + 180, 0.02], [t0 + 240, 0.018], [t0 + 600, 0.015]] },
          // Pod replacement: same pod label, distinct series.
          { labels: { ...base, pod: "etcd-1", instance: "10.0.0.2:2381" }, samples: [[t0 + 1200, 0.005], [t0 + 1260, 0.006]] },
          { labels: { ...base, pod: "etcd-0", instance: "10.0.0.3:2381" }, samples: [[t0 + 1800, 0.004]] },
        ] },
      }),
      chart(2, {}),
      chart(3, { query_results: { series: [] } }),
      chart(4, { query_results: { series: [{ labels: { ...base, pod: "etcd-2" }, samples: [[t0, null]] }] } }),
    ],
  };
  const prometheusRequests = await mockServices(page, doc);
  await page.goto("/ui/documents/doc-3");
  await page.getByLabel("Username").fill("alice");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByRole("heading", { name: doc.title })).toBeVisible();

  const cards = page.locator(".infrapad-block-card");
  await expect(cards).toHaveCount(4);
  const populated = cards.nth(0);
  await expect(populated.getByText("Time Series Chart")).toBeVisible();
  await expect(populated.getByRole("heading", { name: "WAL fsync p99" })).toBeVisible();
  const svg = populated.locator(".infrapad-timeseries-chart svg").first();
  await expect(svg).toBeVisible();
  await expect(svg.getByText("Time (UTC)")).toBeVisible();
  await expect(svg.locator("text").filter({ hasText: /^10:00$/ })).toHaveCount(1);
  await expect(svg.locator("text").filter({ hasText: /^s$/ })).toHaveCount(1);

  // Legend: compact distinguishing names, full labels on hover.
  const legend = populated.getByRole("list", { name: "Series" }).getByRole("listitem");
  await expect(legend).toHaveText([
    "instance=10.0.0.1:2381, pod=etcd-1",
    "instance=10.0.0.2:2381, pod=etcd-1",
    "instance=10.0.0.3:2381, pod=etcd-0",
  ]);
  await legend.nth(1).locator(".infrapad-timeseries-legend-item").hover();
  await expect(page.getByRole("tooltip")).toHaveText('wal{instance="10.0.0.2:2381", job="etcd", pod="etcd-1"}');

  // Point tooltip: series, UTC timestamp, raw value with unit.
  const box = (await svg.boundingBox())!;
  // Padding is left 80 / right 30; the isolated t0+1800 sample is mid-window.
  await page.mouse.move(box.x + 80 + (box.width - 110) / 2, box.y + box.height / 2);
  const chartArea = populated.locator(".infrapad-timeseries-chart");
  await expect(chartArea.getByText("instance=10.0.0.3:2381, pod=etcd-0")).toBeVisible();
  await expect(chartArea.getByText("2026-11-13 10:30:00 UTC")).toBeVisible();
  await expect(chartArea.getByText("0.004 s")).toBeVisible();

  // The expression stays visible; read-only query metadata is collapsed and in UTC.
  const expression = populated.getByLabel("Query expression");
  await expect(expression).toHaveText(query.expression);
  await expect(expression).toBeVisible();
  const details = populated.getByRole("button", { name: "Details", exact: true });
  const start = populated.getByText("2026-11-13 10:00:00 UTC");
  const end = populated.getByText("2026-11-13 11:00:00 UTC");
  const step = populated.getByText("60s");
  const source = populated.getByText(query.source_url);
  await expect(details).toHaveAttribute("aria-expanded", "false");
  for (const field of [start, end, step, source]) await expect(field).toBeHidden();

  await details.click();
  await expect(details).toHaveAttribute("aria-expanded", "true");
  await expect(expression).toBeVisible();
  for (const field of [start, end, step, source]) await expect(field).toBeVisible();

  await details.click();
  await expect(details).toHaveAttribute("aria-expanded", "false");
  for (const field of [start, end, step, source]) await expect(field).toBeHidden();
  await expect(expression).toBeVisible();

  await expect(cards.nth(1).getByText("No snapshot submitted.")).toBeVisible();
  await expect(cards.nth(2).getByText("No series found for this time range.")).toBeVisible();
  await expect(cards.nth(3).getByText("No numeric samples in this snapshot.")).toBeVisible();
  await expect(cards.nth(3).getByRole("list", { name: "Series" }).getByRole("listitem")).toHaveText(['wal{job="etcd", pod="etcd-2"}']);
  expect(prometheusRequests).toEqual([]);
});

test("appearance settings apply across documents and persist", async ({ page }) => {
  await mockServices(page);

  await openAuthenticatedDocument(page);

  const root = page.locator("html");
  const masthead = page.locator(".pf-v6-c-masthead");
  const card = page.locator(".pf-v6-c-card").first();
  const backgrounds = async () => ({
    masthead: await masthead.evaluate((node) => getComputedStyle(node).backgroundColor),
    document: await card.evaluate((node) => getComputedStyle(node).backgroundColor),
  });

  // System follows OS changes even when the picker is closed.
  await page.emulateMedia({ colorScheme: "light", forcedColors: "none" });
  await expect(root).not.toHaveClass(/pf-v6-theme-dark|pf-v6-theme-high-contrast/);
  const original = await backgrounds();
  const axisLabel = page.locator(".infrapad-alerts-chart-container svg text").first();
  await expect(axisLabel).toBeVisible();
  const lightAxisColor = await axisLabel.evaluate((node) => getComputedStyle(node).fill);
  await page.emulateMedia({ colorScheme: "dark", forcedColors: "none" });
  await expect(root).toHaveClass(/pf-v6-theme-dark/);
  await expect.poll(() => axisLabel.evaluate((node) => getComputedStyle(node).fill)).not.toBe(lightAxisColor);
  await page.emulateMedia({ forcedColors: "active" });
  await expect(root).toHaveClass(/pf-v6-theme-high-contrast/);
  await page.emulateMedia({ colorScheme: "light", forcedColors: "none" });
  await expect(root).not.toHaveClass(/pf-v6-theme-dark|pf-v6-theme-high-contrast/);

  // Independent non-default choices should change both header and document colors.
  await page.getByRole("button", { name: "Theme settings" }).click();
  const picker = page.getByRole("dialog", { name: "Appearance" });
  await picker.getByRole("group", { name: "Theme" }).getByRole("button", { name: "Project Felt" }).click();
  await picker.getByRole("group", { name: "Color scheme" }).getByRole("button", { name: "Dark" }).click();
  await picker.getByRole("group", { name: "Contrast mode" }).getByRole("button", { name: "Glass" }).click();
  await expect(root).toHaveClass(/pf-v6-theme-felt/);
  await expect(root).toHaveClass(/pf-v6-theme-dark/);
  await expect(root).toHaveClass(/pf-v6-theme-glass/);
  const updated = await backgrounds();
  expect(updated.masthead).not.toBe(original.masthead);
  expect(updated.document).not.toBe(original.document);
  // Escape closes the picker; the list inherits the same document surface colors.
  await page.keyboard.press("Escape");
  await expect(picker).toBeHidden();
  await page.getByRole("link", { name: "Documents" }).click();
  await expect(page.getByRole("link", { name: document.title })).toBeVisible();
  await expect(root).toHaveClass(/pf-v6-theme-dark/);
  await expect(page.getByRole("grid", { name: "Infrapad documents" })).toBeVisible();
  await page.getByRole("link", { name: document.title }).click();

  // Reload should restore all three choices before showing the document.
  await page.reload();
  await expect(page.getByRole("heading", { name: document.title })).toBeVisible();
  await expect(root).toHaveClass(/pf-v6-theme-felt/);
  await expect(root).toHaveClass(/pf-v6-theme-dark/);
  await expect(root).toHaveClass(/pf-v6-theme-glass/);
  await expect.poll(backgrounds).toEqual(updated);
  await page.getByRole("button", { name: "Theme settings" }).click();
  const contrast = picker.getByRole("group", { name: "Contrast mode" });
  await expect(contrast.getByRole("button", { name: "Glass" })).toHaveAttribute("aria-pressed", "true");
  // Explicit Default ignores forced colors; System responds to them again.
  await contrast.getByRole("button", { name: "Default" }).click();
  await page.emulateMedia({ forcedColors: "active" });
  await expect(root).not.toHaveClass(/pf-v6-theme-high-contrast|pf-v6-theme-glass/);
  await contrast.getByRole("button", { name: "System" }).click();
  await expect(root).toHaveClass(/pf-v6-theme-high-contrast/);
  await page.emulateMedia({ forcedColors: "none" });
  // Outside click dismisses the picker.
  await page.getByRole("heading", { name: document.title }).click();
  await expect(picker).toBeHidden();

  // The masthead controls remain usable without horizontal overflow on small screens.
  await page.setViewportSize({ width: 360, height: 700 });
  await expect(page.getByRole("button", { name: "Theme settings" })).toBeVisible();
  await expect(page.getByLabel("Signed in as alice")).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});
