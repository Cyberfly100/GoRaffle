const { test, expect, seedEntries } = require("./fixtures");
const h = require("./helpers");

const NAMES = ["Ada", "Grace", "Linus", "Margaret", "Ken"];

test("after a reload the vertical resting Reel shows the winner with its neighbours", async ({ page, request }) => {
  await seedEntries(request, NAMES);
  await h.preferVerticalReel(page);
  await page.goto("/");

  const drawnAt = Date.now();
  await page.locator("#draw-btn").click();
  await expect(page.locator("#result .result-sub")).toHaveText("pick #1", { timeout: 20_000 });
  const reveal = await h.restingReel(page);
  expect(NAMES).toContain(reveal.winner);

  // A reload during the replay window would replay the Reveal.
  await h.waitOutReplayWindow(page, drawnAt);
  await page.reload();

  await expect.poll(() => h.restingReel(page)).toMatchObject({ winner: reveal.winner });
  const rest = await h.restingReel(page);
  expect(rest.above).toHaveLength(3);
  expect(rest.below).toHaveLength(3);
  for (const n of [...rest.above, ...rest.below]) {
    expect(NAMES).toContain(n);
    expect(n).not.toBe(reveal.winner);
  }
});
