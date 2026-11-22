// Import and the Reveal replay: a browser that connects right after an
// Import is never shown a Reveal from before it. The Import replaced the
// List, so the Pick the remembered Reveal points at no longer stands —
// the replay path already checks that (PickStands); these tests pin the
// guarantee to the Import.
// Every step a script could do over HTTP, per the browser-test notes.
const { test, expect, seedEntries } = require("./fixtures");
const h = require("./helpers");

test("a browser connecting right after an Import receives no Reveal from before it", async ({ page, request, browser }) => {
  await h.showEntriesOnLoad(page);
  await seedEntries(request, ["Ada", "Grace"]);

  // A Draw happens: its Reveal is remembered for browsers connecting
  // within the replay window (~6s).
  const drawn = await request.post("/api/draw", { data: {} });
  expect(drawn.ok()).toBe(true);

  // Then the host imports a List file without a History, the way a script
  // posts one straight to the import endpoint. The drawn Pick no longer
  // stands.
  const imported = await request.post("/api/import", {
    data: { list_name: "Fresh list", entries: [{ name: "Mira" }, { name: "Jules" }] },
  });
  expect(imported.status()).toBe(201);

  // A browser connecting right after the Import — well inside that
  // replay window — is not shown the old Reveal: the Reel stays at rest
  // and no winner is centred in it.
  await page.goto("/");
  await expect(page.locator("#ws-status")).toHaveClass(/\bconnected\b/);
  expect(await h.reelStaysStill(page, 2500)).toBe(true);
  expect((await h.restingReel(page)).winner).toBe(null);
  await expect(page.locator("#result .result-sub")).toHaveText("");

  // It sees the imported List instead: the new Entries and an empty
  // History, with no Pick anywhere.
  await expect.poll(() => h.entryNamesInTable(page)).toEqual(["Jules", "Mira"]);
  await expect.poll(() => h.historyNames(page)).toEqual([]);

  // The replay machinery itself still works at that moment: a fresh
  // Draw's Reveal DOES reach a browser connecting within the window. So
  // the silence above was the Import's doing, not a closed window or a
  // broken replay.
  const winner = (await (await request.post("/api/draw", { data: {} })).json()).winner.name;
  const latecomer = await browser.newPage();
  try {
    await latecomer.goto("/");
    expect(await h.nextLanding(latecomer)).toBe(winner);
    await expect(latecomer.locator("#result .result-sub")).toHaveText("pick #1", { timeout: 10_000 });
  } finally {
    await latecomer.close();
  }
});
