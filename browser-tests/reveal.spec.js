// The Reveal: one event per Draw that every browser plays once, with
// nothing about the winner shown before the Reel has landed.
const { test, expect, seedEntries } = require("./fixtures");
const h = require("./helpers");

const NAMES = ["Ada", "Grace", "Linus", "Margaret", "Ken"];

test("a Draw starts the Reel in every open browser and lands the same winner", async ({ page, request, browser }) => {
  await seedEntries(request, NAMES);
  const audience = await browser.newPage();
  try {
    await page.goto("/");
    await audience.goto("/");
    await expect(audience.locator("#ws-status")).toHaveClass(/\bconnected\b/);

    const hostLanding = h.nextLanding(page);
    const audienceLanding = h.nextLanding(audience);
    const winner = await h.clickDraw(page);
    expect(await hostLanding).toBe(winner);
    expect(await audienceLanding).toBe(winner);
    await expect(audience.locator("#result .result-sub")).toHaveText("pick #1", { timeout: 10_000 });
  } finally {
    await audience.close();
  }
});

test("an Entry edit during the Suspense shows only after the winner has landed", async ({ page, request }) => {
  await seedEntries(request, NAMES);
  await h.showEntriesOnLoad(page);
  await page.goto("/");
  const entries = await (await request.get("/api/entries")).json();
  const before = await h.pickCountsInTable(page);

  const landing = h.nextLanding(page);
  const winner = await h.clickDraw(page);
  await h.reelMoving(page);
  const edited = entries.find((e) => e.name !== winner);
  await request.put(`/api/entries/${edited.id}`, { data: { name: "Barbara" } });

  // The edit's change event is in; nothing the audience sees has moved on.
  await page.waitForTimeout(500);
  expect(await h.entryNamesInTable(page)).not.toContain("Barbara");
  expect(await h.historyNames(page)).not.toContain(winner);
  expect(await h.pickCountsInTable(page)).toEqual(before);
  await expect(page.locator("#result .result-sub")).not.toHaveText("pick #1");

  expect(await landing).toBe(winner);
  await expect(page.locator("#result .result-sub")).toHaveText("pick #1", { timeout: 10_000 });
  await expect.poll(() => h.entryNamesInTable(page)).toContain("Barbara");
  await expect.poll(() => h.historyNames(page)).toEqual([winner]);
  expect((await h.pickCountsInTable(page))[winner]).toBe(1);
});

test("with its websocket blocked, the drawing browser still plays the full Reveal", async ({ page, request }) => {
  await seedEntries(request, NAMES);
  await h.blockWebSocket(page);
  await page.goto("/");

  const landing = h.nextLanding(page);
  const reply = page.waitForResponse((r) => r.url().endsWith("/api/draw"));
  await page.locator("#draw-btn").click();
  const winner = (await (await reply).json()).winner.name;
  await h.reelMoving(page);
  await expect(page.locator("#draw-btn")).toBeDisabled();

  expect(await landing).toBe(winner);
  await expect(page.locator("#result .result-sub")).toHaveText("pick #1", { timeout: 10_000 });
  await expect(page.locator("#draw-btn")).toBeEnabled();
});

test("a browser reloaded during the Suspense replays the Reveal", async ({ page, request }) => {
  await seedEntries(request, NAMES);
  await page.goto("/");
  const winner = await h.clickDraw(page);
  await h.reelMoving(page);

  await page.reload();
  expect(await h.nextLanding(page)).toBe(winner);
  await expect(page.locator("#result .result-sub")).toHaveText("pick #1", { timeout: 10_000 });
});

test("a browser opened after the replay window shows the last Pick with the Reel at rest", async ({ page, request }) => {
  await seedEntries(request, NAMES);
  const drawnAt = Date.now();
  const winner = (await (await request.post("/api/draw", { data: {} })).json()).winner.name;
  await h.waitOutReplayWindow(page, drawnAt);

  await page.goto("/");
  await expect(page.locator("#ws-status")).toHaveClass(/\bconnected\b/);
  expect(await h.reelStaysStill(page, 2500)).toBe(true);
  expect((await h.restingReel(page)).winner).toBe(winner);
  await expect(page.locator("#result .result-sub")).toHaveText("pick #1");
});

test("a Reveal that arrives over both HTTP and the websocket plays once", async ({ page, request }) => {
  await seedEntries(request, NAMES);
  await page.goto("/");

  const landing = h.nextLanding(page);
  const winner = await h.clickDraw(page);
  expect(await landing).toBe(winner);
  expect(await h.reelStaysStill(page, 4000)).toBe(true);
  expect((await h.restingReel(page)).winner).toBe(winner);
});
