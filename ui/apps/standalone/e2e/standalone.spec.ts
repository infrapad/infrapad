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
    record: { items: [{ timestamp: "2025-01-02T03:05:00Z", key: "slack-message:1", content: { author: "Alice", text: "Is it **down**?" } }] },
    presentation: { cards: [{
      title: "Slack conversation",
      markdown: "- **Alice** · 2025-01-02 03:05:00 UTC\n  Is it **down**?\n",
      fields: [{ label: "Messages", value: "1" }],
      links: [],
    }] },
    markdown: "- **Alice** · 2025-01-02 03:05:00 UTC\n  Is it **down**?\n",
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
        },
      },
    },
    ...slackBlocks,
  ],
};

// Stub document and Prometheus responses while exercising the real auth proxy.
async function mockServices(page: Page) {
  await page.route("**/v1/documents**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    const body = path.endsWith("/blocks/1/history")
      ? { blocks: [document.blocks[0], { ...document.blocks[0], revisionNumber: 1, record: { timestamp: "2025-01-02T03:04:05Z", content: { text: "Service down." } } }] }
      : path === "/v1/documents" ? { documents: [document] } : { document };
    await route.fulfill({ contentType: "application/json", body: JSON.stringify(body) });
  });
  await page.route("http://localhost:9090/**", async (route) => {
    const url = new URL(route.request().url());
    expect(url.pathname).toBe("/api/v1/query_range");
    expect(url.searchParams.get("query")).toBe('ALERTS{alertname="EndpointDown", severity="critical"}');
    expect(url.searchParams.get("step")).toBe("15s");
    const start = Number(url.searchParams.get("start"));
    expect(start).toBe(Math.floor(Date.parse("2025-01-02T03:00:00Z") / 1000) - 120);
    expect(Number(url.searchParams.get("end"))).toBe(Math.floor(Date.parse("2025-01-02T03:05:00Z") / 1000) + 120);
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({ status: "success", data: { resultType: "matrix", result: [
        { metric: { alertname: "EndpointDown", severity: "critical" }, values: [[start + 120, "1"]] },
      ] } }),
    });
  });
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
  await expect(cards.nth(1).locator(".pf-v6-c-description-list__group")).toHaveText(["Messages1"]);

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
  await mockServices(page);

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
  expect(config.services).toEqual({ infrapadApiBaseUrl: "/v1", prometheusApiBaseUrl: "http://localhost:9090" });
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
