const { test, expect, seedEntries, setListName } = require("./fixtures");
const h = require("./helpers");

test.describe("import warning", () => {
  test("choosing a file shows the warning; Cancel changes nothing", async ({ page, request }) => {
    await seedEntries(request, ["Ann"]);
    await h.showEntriesOnLoad(page);
    await page.goto("/");
    await h.openImportExport(page);

    await page
      .locator("#import-file")
      .setInputFiles(h.jsonFile("people.json", { list_name: "ZedList", entries: [{ name: "Zed" }] }));

    await expect(page.locator("#import-confirm")).toBeVisible();
    await expect(page.locator("#import-confirm h2")).toHaveText("Import replaces the entries");
    await expect(page.locator("#import-confirm-text")).toContainText("replaces all Entries and the History");
    await expect(page.locator("#file-chosen")).toHaveText("people.json");

    await page.locator("#import-cancel-btn").click();
    await expect(page.locator("#import-confirm")).toBeHidden();
    await expect(page.locator("#file-chosen")).toHaveText("No file chosen");
    await expect(page.locator("#import-file")).toHaveValue("");
    await h.closeImportExport(page);
    await expect(page.locator("#list-name")).toHaveValue("Entries");
    await expect.poll(() => h.entryNamesInTable(page)).toEqual(["Ann"]);
    expect(await h.history(request)).toEqual([]);
  });

  test("an Import confirmed on the warning shows its toast, resets the form and shows the Entries", async ({ page }) => {
    await h.showEntriesOnLoad(page);
    await page.goto("/");
    await h.openImportExport(page);

    await h.importFile(page, h.jsonFile("people.json", { entries: [{ name: "Ada" }, { name: "Grace" }] }));

    await expect(h.toast(page)).toHaveText("Imported: 2 entries, 0 picks.");
    await expect(page.locator("#import-confirm")).toBeHidden();
    // Nothing popup-shaped stays open after a successful Import.
    await expect(page.locator("#ie-dialog")).toBeHidden();
    await expect(page.locator("#file-chosen")).toHaveText("No file chosen");
    await expect(page.locator("#import-file")).toHaveValue("");
    await expect.poll(() => h.entryNamesInTable(page)).toEqual(["Ada", "Grace"]);
  });

  test("\"Export current entries first\" downloads the stored List without closing the warning", async ({ page, request }) => {
    await setListName(request, "Club");
    await h.showEntriesOnLoad(page);
    await page.goto("/");
    await h.openImportExport(page);

    // An edit the host has not committed (no blur, no save): the field says
    // one thing, the stored List still says "Club".
    await page.locator("#list-name").evaluate((el) => (el.value = "Unsaved Name"));

    await page.locator("#import-file").setInputFiles(h.jsonFile("people.json", { entries: [{ name: "Zed" }] }));
    await expect(page.locator("#import-confirm")).toBeVisible();
    await expect(page.locator("#export-first")).toHaveText("Export current entries first");
    // The link keeps the href the template gave it: no ?name= override.
    await expect(page.locator("#export-first")).toHaveAttribute("href", "/api/export?format=json");

    const download = page.waitForEvent("download");
    await page.locator("#export-first").click();
    expect((await download).suggestedFilename()).toBe("Club.json");

    await expect(page.locator("#import-confirm")).toBeVisible();
    await page.locator("#import-cancel-btn").click();
    await expect(page.locator("#import-confirm")).toBeHidden();
    expect(await h.entryNamesInTable(page)).toEqual([]);
  });

  test("the unused-Tags checkbox starts unticked, and ticking it sends the remove-unused-Tags option", async ({ page, request }) => {
    // The current List carries a Tag the imported Entries won't use.
    const seeded = await request.post("/api/import", { data: { entries: [{ name: "Ann", tags: ["old"] }] } });
    expect(seeded.ok()).toBeTruthy();
    await h.showEntriesOnLoad(page);
    await page.goto("/");
    const fetched = h.recordRegionFetches(page);
    await h.openImportExport(page);

    await page.locator("#import-file").setInputFiles(h.jsonFile("people.json", { entries: [{ name: "Ben", tags: ["keep"] }] }));
    await expect(page.locator("#import-confirm")).toBeVisible();
    await expect(page.locator("#remove-unused-tags")).not.toBeChecked();
    await expect(page.locator("label[for='remove-unused-tags']")).toContainText("Also remove Tags that no imported Entry uses");

    await page.locator("#remove-unused-tags").check();
    await page.locator("#import-confirm-btn").click();

    await expect(h.toast(page)).toHaveText("Imported: 1 entry, 0 picks.");
    // The Tag filter bar re-renders from the Import's change event.
    await expect.poll(() => fetched().filter((p) => p === "/partials/filter").length).toBeGreaterThan(0);
    await expect(page.locator("#ie-dialog")).toBeHidden();
    await expect.poll(() => h.availableTags(page)).toEqual(["keep"]);
  });

  test("Picks naming missing Entries show the names and count; skipping imports without those Picks", async ({ page, request }) => {
    await seedEntries(request, ["Ann"]);
    await h.showEntriesOnLoad(page);
    await page.goto("/");
    await h.openImportExport(page);

    await page.locator("#import-file").setInputFiles(
      h.jsonFile("holey.json", {
        entries: [{ name: "Ben" }],
        picks: [
          { entry_name: "Ben", pick_number: 1, picked_at: "2026-11-23T10:00:00Z" },
          { entry_name: "Ghost", pick_number: 2, picked_at: "2026-11-23T11:00:00Z" },
          { entry_name: "ghost", pick_number: 3, picked_at: "2026-11-23T12:00:00Z" },
        ],
      })
    );
    await expect(page.locator("#import-confirm")).toBeVisible();
    await page.locator("#import-confirm-btn").click();

    // The warning stays open and says which names and how many Picks.
    await expect(page.locator("#import-confirm")).toBeVisible();
    await expect(page.locator("#import-missing-text")).toContainText("Ghost");
    await expect(page.locator("#import-missing-text")).toContainText("2 Picks");
    await expect(page.locator("#import-skip-btn")).toBeVisible();
    await expect.poll(() => h.entryNamesInTable(page)).toEqual(["Ann"]);

    await page.locator("#import-skip-btn").click();

    await expect(h.toast(page)).toHaveText("Imported: 1 entry, 1 pick.");
    await expect(page.locator("#import-confirm")).toBeHidden();
    await expect(page.locator("#ie-dialog")).toBeHidden();
    await expect.poll(() => h.entryNamesInTable(page)).toEqual(["Ben"]);
    await expect.poll(() => h.historyNames(page)).toEqual(["Ben"]);
  });

  test("after an Import a second connected browser shows the new List and History", async ({ page, browser }, testInfo) => {
    await h.showEntriesOnLoad(page);
    await page.goto("/");
    await h.openImportExport(page);
    const other = await h.secondBrowser(browser, testInfo);
    await h.waitForLiveUpdates(other);

    await h.importFile(
      page,
      h.jsonFile("moved.json", {
        list_name: "Moved",
        entries: [{ name: "Ada" }, { name: "Grace" }],
        picks: [{ entry_name: "Ada", pick_number: 1, picked_at: "2026-11-23T10:00:00Z" }],
      })
    );

    await expect(h.toast(page)).toHaveText("Imported: 2 entries, 1 pick.");
    await expect(other.locator("#list-name")).toHaveValue("Moved");
    await expect.poll(() => h.entryNamesInTable(other)).toEqual(["Ada", "Grace"]);
    await expect.poll(() => h.historyNames(other)).toEqual(["Ada"]);
    await other.context().close();
  });
});

test.describe("legacy import", () => {
  test("a CSV file still imports as a quiet legacy format", async ({ page }) => {
    await h.showEntriesOnLoad(page);
    await page.goto("/");
    await h.openImportExport(page);

    await h.importFile(page, h.csvFile("people.csv", "name,pick_count,excluded,tags\nAda,0,false,\nGrace,1,false,team\n"));

    await expect(h.toast(page)).toHaveText("Imported: 2 entries, 0 picks.");
    await expect(page.locator("#import-confirm")).toBeHidden();
    await expect(page.locator("#ie-dialog")).toBeHidden();
    await expect.poll(() => h.entryNamesInTable(page)).toEqual(["Ada", "Grace"]);
  });

  test("without a live websocket the page refreshes to show the imported Entries", async ({ page }) => {
    await h.blockWebSocket(page);
    await h.showEntriesOnLoad(page);
    await page.goto("/");
    await h.openImportExport(page);

    await h.importFile(page, h.jsonFile("people.json", ["Ada", "Grace"]));

    await expect(h.toast(page)).toHaveText("Imported: 2 entries, 0 picks.");
    await expect(page.locator("#ie-dialog")).toBeHidden();
    await expect.poll(() => h.entryNamesInTable(page)).toEqual(["Ada", "Grace"]);
  });

  test("an import that fails at the network level shows an error toast", async ({ page }) => {
    await page.route("**/api/import", (route) => route.abort("internetdisconnected"));
    await page.goto("/");
    await h.openImportExport(page);

    await h.importFile(page, h.jsonFile("people.json", ["Ada"]));

    await expect(h.toast(page)).toHaveText("Could not reach the server.");
  });
});

test.describe("the import form", () => {
  test("the file picker offers only JSON, and the help text mentions only List files", async ({ page }) => {
    await h.showEntriesOnLoad(page);
    await page.goto("/");
    await h.openImportExport(page);

    await expect(page.locator("#import-file")).toHaveAttribute("accept", ".json");
    const section = page.locator("#import-export");
    await expect(section).toContainText("List file");
    await expect(section).not.toContainText(/csv/i);
    await expect(page.locator("#import-export .export-row a")).toHaveText("Export");
  });
});

test.describe("import with a List name", () => {
  test("updates the List name field and the export link", async ({ page }) => {
    await page.goto("/");
    await h.openImportExport(page);

    await h.importFile(page, h.jsonFile("club.json", { list_name: "Chess Club", entries: [{ name: "Ada" }] }));

    await expect(h.toast(page)).toHaveText("Imported: 1 entry, 0 picks.");
    await expect(page.locator("#list-name")).toHaveValue("Chess Club");
    await expect.poll(() => h.exportLinkNames(page)).toEqual(["Chess Club"]);
  });
});
