const { test, expect } = require("./fixtures");
const h = require("./helpers");

test.describe("import", () => {
  test("a JSON import shows its toast, resets the form and shows the Entries", async ({ page }) => {
    await h.showEntriesOnLoad(page);
    await page.goto("/");
    await h.openImportExport(page);

    await h.importFile(page, h.jsonFile("people.json", { entries: [{ name: "Ada" }, { name: "Grace" }] }));

    await expect(h.toast(page)).toHaveText("Imported: 2 added, 0 updated.");
    await expect(page.locator("#file-chosen")).toHaveText("No file chosen");
    await expect(page.locator("#import-btn")).toBeDisabled();
    await expect(page.locator("#import-file")).toHaveValue("");
    await h.closeImportExport(page);
    await expect.poll(() => h.entryNamesInTable(page)).toEqual(["Ada", "Grace"]);
  });

  test("a CSV import shows its toast, resets the form and shows the Entries", async ({ page }) => {
    await h.showEntriesOnLoad(page);
    await page.goto("/");
    await h.openImportExport(page);

    await h.importFile(page, h.csvFile("people.csv", "name,pick_count,excluded,tags\nAda,0,false,\nGrace,1,false,team\n"));

    await expect(h.toast(page)).toHaveText("Imported: 2 added, 0 updated.");
    await expect(page.locator("#file-chosen")).toHaveText("No file chosen");
    await expect(page.locator("#import-btn")).toBeDisabled();
    await h.closeImportExport(page);
    await expect.poll(() => h.entryNamesInTable(page)).toEqual(["Ada", "Grace"]);
  });

  test("without a live websocket the page refreshes to show the imported Entries", async ({ page }) => {
    await h.blockWebSocket(page);
    await h.showEntriesOnLoad(page);
    await page.goto("/");
    await h.openImportExport(page);

    await h.importFile(page, h.jsonFile("people.json", ["Ada", "Grace"]));

    await expect(h.toast(page)).toHaveText("Imported: 2 added, 0 updated.");
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

    await expect(h.toast(page)).toHaveText("Imported: 1 added, 0 updated.");
    await expect(page.locator("#list-name")).toHaveValue("Chess Club");
    await expect.poll(() => h.exportLinkNames(page)).toEqual(["Chess Club"]);
  });
});
