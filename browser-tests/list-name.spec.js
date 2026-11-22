const { test, expect, setListName } = require("./fixtures");
const h = require("./helpers");

test("the export link follows every rename of the List", async ({ page }) => {
  await page.goto("/");
  await h.showEntries(page);

  for (const name of ["First", "Second", "Third"]) {
    await h.renameList(page, name);
    await h.openImportExport(page);
    await expect.poll(() => h.exportLinkNames(page)).toEqual([name]);
    await h.closeImportExport(page);
  }
});

test("the page arrives with the current List name already filled in", async ({ page, request }) => {
  await setListName(request, "Chess Club");
  // Hold back the page's own List name lookup: whatever the field shows
  // meanwhile is what the server rendered.
  await page.route("**/api/list-name", () => {});

  await page.goto("/");

  await expect(page.locator("#list-name")).toHaveValue("Chess Club");
  await expect.poll(() => h.exportLinkNames(page)).toEqual(["Chess Club"]);
});
