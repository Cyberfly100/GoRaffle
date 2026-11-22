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
});
