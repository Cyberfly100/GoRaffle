// Region-aware refresh: a change made in one browser refreshes exactly the
// page regions it made stale in every browser, and each region keeps what
// the host had going in it.
const { test, expect, seedEntries, setListName } = require("./fixtures");
const h = require("./helpers");

const manyNames = Array.from({ length: 60 }, (_, i) => `Entry ${String(i + 1).padStart(2, "0")}`);

test.describe("the Entry table", () => {
  test("keeps its scroll position when another browser changes an Entry", async ({ page, request }) => {
    await seedEntries(request, manyNames);
    await h.showEntriesOnLoad(page);
    await page.goto("/");
    await h.waitForLiveUpdates(page);
    const rows = page.locator("#entry-table .rows-scroll");
    await rows.evaluate((el) => (el.scrollTop = 400));
    await expect.poll(() => rows.evaluate((el) => el.scrollTop)).toBe(400);

    const last = manyNames.at(-1);
    await h.otherBrowserChangesEntry(request, last, { pick_count: 7 });

    await expect(h.pickCountField(page, last)).toHaveValue("7");
    expect(await rows.evaluate((el) => el.scrollTop)).toBe(400);
  });

  test("keeps the focused name field, its in-flight edit and the cursor", async ({ page, request }) => {
    await seedEntries(request, ["Ada", "Grace"]);
    await h.showEntriesOnLoad(page);
    await page.goto("/");
    await h.waitForLiveUpdates(page);
    const field = h.nameField(page, "Grace");
    await field.click();
    await field.press("End");
    await field.pressSequentially(" Hopper");
    await field.press("ArrowLeft");
    await field.press("ArrowLeft");

    await h.otherBrowserChangesEntry(request, "Ada", { pick_count: 2 });

    await expect(h.pickCountField(page, "Ada")).toHaveValue("2");
    const focused = await page.evaluate(() => {
      const el = document.activeElement;
      return { field: el.dataset.field, value: el.value, cursor: el.selectionStart };
    });
    expect(focused).toEqual({ field: "name", value: "Grace Hopper", cursor: "Grace Hopp".length });
  });

  test("keeps a multi-line name field's height", async ({ page, request }) => {
    await seedEntries(request, ["A very long name\nthat runs over\nthree lines", "Grace"]);
    await h.showEntriesOnLoad(page);
    await page.goto("/");
    await h.waitForLiveUpdates(page);
    const tall = page.locator('#entry-table textarea[data-field="name"]').first();
    const before = (await tall.boundingBox()).height;

    await h.otherBrowserChangesEntry(request, "Grace", { pick_count: 4 });

    await expect(h.pickCountField(page, "Grace")).toHaveValue("4");
    expect((await tall.boundingBox()).height).toBe(before);
  });

  test("an Entry edit refreshes the table and leaves the result alone", async ({ page, request }) => {
    await seedEntries(request, ["Ada", "Grace"]);
    await h.showEntriesOnLoad(page);
    await page.goto("/");
    await h.waitForLiveUpdates(page);
    const refreshed = h.recordRegionFetches(page);

    await h.otherBrowserChangesEntry(request, "Ada", { pick_count: 3 });

    await expect(h.pickCountField(page, "Ada")).toHaveValue("3");
    // Give any stray refresh time to show up before judging.
    await page.waitForTimeout(500);
    expect(refreshed()).toContain("/partials/table");
    expect(refreshed()).not.toContain("/partials/result");
    expect(refreshed()).not.toContain("/partials/history");
  });
});

test.describe("show/hide from the first paint", () => {
  for (const section of ["history", "entries"]) {
    for (const visible of [true, false]) {
      const state = visible ? "shown" : "hidden";
      test(`${section} saved as ${state} arrives ${state}, before any app script runs`, async ({ page }) => {
        await page.addInitScript(
          ([key, value]) => localStorage.setItem(key, value),
          [`raffle-${section}-visible`, visible ? "1" : "0"]
        );
        // Without the app's module the page shows exactly what it painted
        // first: nothing is left for a script to correct.
        await page.route("**/static/js/app.js", (route) => route.abort());
        await page.goto("/");

        await h.expectSection(page, section, visible);
      });

      test(`${section} saved as ${state} stays ${state} once the app is running`, async ({ page }) => {
        await page.addInitScript(
          ([key, value]) => localStorage.setItem(key, value),
          [`raffle-${section}-visible`, visible ? "1" : "0"]
        );
        await page.goto("/");
        await h.waitForLiveUpdates(page);

        await h.expectSection(page, section, visible);
      });
    }

    test(`the ${section} toggle flips the section and is remembered on reload`, async ({ page }) => {
      await page.goto("/");
      await h.expectSection(page, section, false);
      await page.locator(`#${section}-toggle-btn`).click();
      await h.expectSection(page, section, true);
      await page.reload();
      await h.expectSection(page, section, true);
      await page.locator(`#${section}-toggle-btn`).click();
      await h.expectSection(page, section, false);
    });
  }

  test("the show/hide labels survive a refresh of the result", async ({ page, request }) => {
    await seedEntries(request, ["Ada"]);
    await h.showEntriesOnLoad(page);
    await page.goto("/");
    await h.waitForLiveUpdates(page);
    await h.otherBrowserChangesEntry(request, "Ada", { name: "Ada Lovelace" });
    await expect(h.nameField(page, "Ada Lovelace")).toBeVisible();
    await h.expectSection(page, "entries", true);
    await h.expectSection(page, "history", false);
  });
});

test("a rename of the List in one browser shows in every other browser", async ({ page, browser }, testInfo) => {
  await h.showEntriesOnLoad(page);
  await page.goto("/");
  const other = await h.secondBrowser(browser, testInfo);
  await h.waitForLiveUpdates(other);

  await h.renameList(page, "Chess Club");

  await expect(other.locator("#list-name")).toHaveValue("Chess Club");
  await h.openImportExport(other);
  await expect.poll(() => h.exportLinkNames(other)).toEqual(["Chess Club"]);
  await other.context().close();
});

test("the page arrives with a non-default List name in the field and the export link", async ({ page, request }) => {
  await setListName(request, "Chess Club");
  await page.route("**/static/js/app.js", (route) => route.abort());
  await page.goto("/");
  await expect(page.locator("#list-name")).toHaveValue("Chess Club");
  await expect.poll(() => h.exportLinkNames(page)).toEqual(["Chess Club"]);
});
