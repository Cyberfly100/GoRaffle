const { test, expect } = require("./fixtures");
const h = require("./helpers");

// "Helpers" hashes to the palette slot tc-18: pastel in light, near-black in
// dark. A dark-system host choosing light mode must still see the pastel.
const HELPERS_PASTEL = "rgb(255, 241, 242)";

test.describe("the theme toggle", () => {
  test("with a dark system on auto, the toggle shows only the moon", async ({ page }) => {
    await page.emulateMedia({ colorScheme: "dark" });
    await page.goto("/");

    // The icon follows the theme in effect — explicit or the system's — the
    // same convention as the reel toggle's orientation icon.
    await expect(page.locator("#theme-toggle .icon-theme-sun")).toBeHidden();
    await expect(page.locator("#theme-toggle .icon-theme-moon")).toBeVisible();
  });

  test("with a dark system, choosing light mode keeps every Tag pill pastel", async ({ page, request }) => {
    const seeded = await request.post("/api/import", { data: { entries: [{ name: "Ann", tags: ["Helpers"] }] } });
    expect(seeded.ok()).toBeTruthy();
    await h.showEntriesOnLoad(page);
    await page.emulateMedia({ colorScheme: "dark" });
    await page.goto("/");

    // One click from auto (system dark) selects light, the way the host does it.
    await page.locator("#theme-toggle").click();
    await expect(page.locator("#theme-toggle .icon-theme-sun")).toBeVisible();
    await expect(page.locator("#theme-toggle .icon-theme-moon")).toBeHidden();

    // The 200ms background transition may still be mid-fade, so poll.
    await expect
      .poll(() => h.tagBubbleBackground(page, "Helpers"))
      .toBe(HELPERS_PASTEL);
  });
});
