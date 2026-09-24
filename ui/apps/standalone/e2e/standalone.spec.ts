import { expect, test, type Page } from "@playwright/test";

const authPort = process.env.INFRAPAD_E2E_AUTH_PORT ?? "18089";
const authOrigin = `http://localhost:${authPort}`;

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
      type: "markdown",
      status: "published",
      content: { text: "**Service recovered** after the deployment was rolled back. <script>window.infrapadXss = true</script>" },
    },
    {
      name: "documents/doc-1/blocks/2",
      blockNumber: 2,
      revisionNumber: 1,
      type: "alerts_matcher",
      content: {
        LabelsMatchers: [{ name: ["EndpointDown"], severity: ["critical"] }],
        Since: "2025-01-02T03:00:00Z",
        Until: "2025-01-02T03:05:00Z",
      },
    },
  ],
};

// Stub document and Prometheus responses while exercising the real auth proxy.
async function mockServices(page: Page) {
  await page.route("**/v1/documents**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    const body = path.endsWith("/blocks/1/history")
      ? { blocks: [document.blocks[0], { ...document.blocks[0], revisionNumber: 1, content: { text: "Service down." } }] }
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
  await expect(page.locator(".infrapad-markdown-content strong")).toHaveText("Service recovered");
  expect(await page.evaluate(() => (window as Window & { infrapadXss?: boolean }).infrapadXss)).toBeUndefined();
  await expect(page.locator(".infrapad-markdown-content script")).toHaveCount(0);
  await expect(page.getByText("name=EndpointDown, severity=critical")).toBeVisible();
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
