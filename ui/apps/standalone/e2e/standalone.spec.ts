import { expect, test } from "@playwright/test";

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
      revisionNumber: 1,
      type: "markdown",
      status: "published",
      content: { text: "Service recovered after the deployment was rolled back." },
    },
  ],
};

test("dummy login opens and reloads an InfraPad document", async ({ page }) => {
  await page.route("**/v1/documents**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify(path === "/v1/documents" ? { documents: [document] } : { document }),
    });
  });
  await page.route("http://localhost:9090/**", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({ status: "success", data: { resultType: "matrix", result: [] } }),
    });
  });

  const deepPath = "/documents/doc-1?view=history#revision-2";
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

  await page.goto(deepPath);
  await expect(page.locator('input[name="returnTo"]')).toHaveValue(deepPath);
  await page.getByLabel("Username").fill("alice");
  await page.getByLabel("Email (optional)").fill("alice@example.com");
  await page.getByRole("button", { name: "Log in" }).click();

  await expect(page).toHaveURL(`${authOrigin}${deepPath}`);
  await expect(page.getByLabel("Signed in as alice")).toBeVisible();
  await expect(page.getByRole("heading", { name: document.title })).toBeVisible();

  await page.getByRole("link", { name: "Documents" }).click();
  await expect(page).toHaveURL(`${authOrigin}/documents`);
  await expect(page.getByRole("link", { name: document.title })).toBeVisible();
  await page.getByRole("link", { name: document.title }).click();
  await expect(page).toHaveURL(`${authOrigin}/documents/doc-1`);
  await expect(page.getByText("Service recovered after the deployment was rolled back.")).toBeVisible();

  await page.reload();
  await expect(page.getByRole("heading", { name: document.title })).toBeVisible();
});
