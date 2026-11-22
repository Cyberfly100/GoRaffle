const { test, expect, seedEntries } = require("./fixtures");
const h = require("./helpers");

const NAMES = ["Ada", "Grace", "Linus", "Margaret", "Ken"];

for (const orientation of ["horizontal", "vertical"]) {
  test(`the ${orientation} Reel stops with the winner centred in its window`, async ({ page, request }) => {
    await seedEntries(request, NAMES);
    if (orientation === "vertical") await h.preferVerticalReel(page);
    await page.goto("/");

    const landing = h.nextLanding(page);
    const winner = await h.clickDraw(page);
    expect(await landing).toBe(winner);
    await expect(page.locator("#result .result-sub")).toHaveText("pick #1", { timeout: 10_000 });
  });
}

test("double-clicking Draw makes one Pick with one Reveal, and Draw stays disabled until the winner lands", async ({
  page,
  request,
}) => {
  await seedEntries(request, NAMES);
  await page.goto("/");
  await expect(page.locator("#ws-status")).toHaveClass(/\bconnected\b/);
  const draws = [];
  page.on("request", (r) => r.url().endsWith("/api/draw") && draws.push(r));

  const landing = h.nextLanding(page);
  await page.locator("#draw-btn").dblclick();
  await expect(page.locator("#draw-btn")).toBeDisabled();
  await h.reelMoving(page);
  await expect(page.locator("#draw-btn")).toBeDisabled();

  const landed = await landing;
  await expect(page.locator("#result .result-sub")).toHaveText("pick #1", { timeout: 10_000 });
  await expect(page.locator("#draw-btn")).toBeEnabled();

  expect(draws).toHaveLength(1);
  const picks = await h.history(request);
  expect(picks).toHaveLength(1);
  expect(landed).toBe(picks[0].entry_name);
});

test("Draw is enabled again when the Draw fails", async ({ page }) => {
  await page.goto("/");
  await expect(page.locator("#ws-status")).toHaveClass(/\bconnected\b/);
  await page.locator("#draw-btn").click();
  await expect(h.toast(page)).toContainText("no eligible entries");
  await expect(page.locator("#draw-btn")).toBeEnabled();
});

test("a second Draw made elsewhere while the first is landing gets its own Reveal after it", async ({
  page,
  request,
}) => {
  await seedEntries(request, NAMES);
  await page.goto("/");

  const first = h.nextLanding(page);
  const firstWinner = await h.clickDraw(page);
  // The first winner arrives after the Suspense; draw again as it lands.
  await page.waitForTimeout(2300);
  const second = await request.post("/api/draw", { data: {} });
  const secondWinner = (await second.json()).winner.name;

  expect(await first).toBe(firstWinner);
  expect(await h.nextLanding(page)).toBe(secondWinner);
  await expect(page.locator("#result .result-sub")).toHaveText("pick #2", { timeout: 10_000 });
});

test("switching the Reel's orientation is refused during a Reveal and allowed after", async ({ page, request }) => {
  await seedEntries(request, NAMES);
  await page.goto("/");
  const toggle = page.locator("#reel-toggle");
  await expect(toggle).toHaveAttribute("aria-pressed", "false");

  const landing = h.nextLanding(page);
  await h.clickDraw(page);
  await h.reelMoving(page);
  await toggle.click();
  await expect(h.toast(page)).toHaveText("Wait for the draw to finish.");
  await expect(toggle).toHaveAttribute("aria-pressed", "false");

  await landing;
  await expect(page.locator("#draw-btn")).toBeEnabled({ timeout: 10_000 });
  await toggle.click();
  await expect(toggle).toHaveAttribute("aria-pressed", "true");
});

test("with reduced motion the winner is shown on the resting Reel without reloading", async ({ page, request }) => {
  await seedEntries(request, NAMES);
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.goto("/");
  await page.evaluate(() => (window.__sameDocument = true));
  // The Reel itself must show the winner: a re-rendered result can't help.
  await page.route("**/partials/result", (route) => route.abort());

  const winner = await h.clickDraw(page);
  await expect(page.locator("#result .reel-cell.is-winner")).toHaveText(winner, { timeout: 10_000 });
  expect(await page.evaluate(() => window.__sameDocument)).toBe(true);
});

test("a refresh of the result that lands mid-spin leaves the Reel running", async ({ page, request }) => {
  await seedEntries(request, NAMES);
  await page.goto("/");
  await expect(page.locator("#ws-status")).toHaveClass(/\bconnected\b/);

  // Hold the next result refresh until the Reel is spinning.
  let release;
  const held = new Promise((r) => (release = r));
  let heldOne = false;
  await page.route("**/partials/result", async (route) => {
    if (heldOne) return route.continue();
    heldOne = true;
    const resp = await route.fetch();
    await held;
    await route.fulfill({ response: resp });
  });
  const refreshing = page.waitForRequest("**/partials/result");
  // A rename makes the result stale (it shows Entry names).
  await h.otherBrowserChangesEntry(request, NAMES[0], { name: "Barbara" });
  await refreshing;

  const landing = h.nextLanding(page);
  const winner = await h.clickDraw(page);
  await h.reelMoving(page);
  release();

  expect(await landing).toBe(winner);
  await expect(page.locator("#result .result-sub")).toHaveText("pick #1", { timeout: 10_000 });
});
