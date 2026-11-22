const { test, expect, seedEntries } = require("./fixtures");
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
    await expect(page.locator("#file-chosen")).toHaveText("No file chosen");
    await expect(page.locator("#import-file")).toHaveValue("");
    await h.closeImportExport(page);
    await expect.poll(() => h.entryNamesInTable(page)).toEqual(["Ada", "Grace"]);
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
    await h.closeImportExport(page);
    await expect.poll(() => h.entryNamesInTable(page)).toEqual(["Ada", "Grace"]);
  });

  test("without a live websocket the page refreshes to show the imported Entries", async ({ page }) => {
    await h.blockWebSocket(page);
    await h.showEntriesOnLoad(page);
    await page.goto("/");
    await h.openImportExport(page);

    await h.importFile(page, h.jsonFile("people.json", ["Ada", "Grace"]));

    await expect(h.toast(page)).toHaveText("Imported: 2 entries, 0 picks.");
    await h.closeImportExport(page);
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
