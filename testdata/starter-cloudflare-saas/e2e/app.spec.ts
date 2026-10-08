import { expect, type Page, test } from "@playwright/test";

const SHOTS = "test-results/screenshots";

async function signIn(page: Page, email: string) {
  await page.goto("/login");
  await page.getByLabel("Email").fill(email);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("heading", { name: "Items" })).toBeVisible();
}

const uniqueEmail = () => `e2e-${crypto.randomUUID()}@example.com`;

test("unauthenticated visitors are sent to the login page, and dev login signs them in", async ({ page }) => {
  await page.goto("/");
  await expect(page).toHaveURL(/\/login$/);
  await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
  await expect(page.getByText("Stub auth")).toBeVisible();
  await page.screenshot({ path: `${SHOTS}/login.png`, fullPage: true });

  const email = uniqueEmail();
  await signIn(page, email);
  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByTestId("user-email")).toHaveText(email);

  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page).toHaveURL(/\/login$/);
});

test("the home page lists items that already exist (created through the API)", async ({ page }) => {
  await signIn(page, uniqueEmail());
  for (const title of ["Alpha from the API", "Beta from the API"]) {
    const created = await page.request.post("/api/items", { data: { title } });
    expect(created.status()).toBe(201);
  }
  await page.goto("/");
  const items = page.getByTestId("item");
  await expect(items).toHaveCount(2);
  await expect(items.first()).toContainText("Beta from the API"); // newest first
  await expect(items.nth(1)).toContainText("Alpha from the API");
  await page.screenshot({ path: `${SHOTS}/home-with-items.png`, fullPage: true });
});

test("create flow: add an item, see it listed, mark it done, persist across reload, delete it", async ({ page }) => {
  await signIn(page, uniqueEmail());
  await expect(page.getByText("No items yet")).toBeVisible();
  await page.screenshot({ path: `${SHOTS}/home-empty.png`, fullPage: true });

  const title = `Buy oat milk ${crypto.randomUUID().slice(0, 8)}`;
  await page.getByLabel("New item title").fill(title);
  await page.getByRole("button", { name: "Add" }).click();

  const item = page.getByTestId("item").filter({ hasText: title });
  await expect(item).toBeVisible();
  await expect(page.getByLabel("New item title")).toHaveValue("");
  await page.screenshot({ path: `${SHOTS}/home-after-create.png`, fullPage: true });

  await item.getByRole("button", { name: `Mark "${title}" done` }).click();
  await expect(item).toHaveClass(/done/);

  await page.reload();
  await expect(page.getByTestId("item").filter({ hasText: title })).toHaveClass(/done/);

  await page.getByRole("button", { name: `Delete "${title}"` }).click();
  await expect(page.getByTestId("item").filter({ hasText: title })).toHaveCount(0);
  await expect(page.getByText("No items yet")).toBeVisible();
});

test("the API is served by the same Worker", async ({ request }) => {
  const health = await request.get("/api/health");
  expect(health.ok()).toBe(true);
  expect(await health.json()).toMatchObject({ status: "ok", database: true });
  expect((await request.get("/api/items")).status()).toBe(401);
});
