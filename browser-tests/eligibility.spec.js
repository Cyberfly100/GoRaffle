// The table shows what the next Draw considers: the server marks each Entry
// as in the Eligible pool and in the Pot for the host's Tag filter. The Pot is
// marked only while it is smaller than the Eligible pool.
const { test, expect } = require("./fixtures");
const h = require("./helpers");

async function seedTeam(request) {
  const resp = await request.post("/api/import", {
    data: {
      entries: [
        { name: "Ada", pick_count: 1, tags: ["backend", "senior"] },
        { name: "Bob", pick_count: 0, tags: ["backend"] },
        { name: "Cy", pick_count: 1, tags: ["frontend"] },
        { name: "Dee", pick_count: 0, tags: ["frontend", "senior"] },
        { name: "Eve", pick_count: 0, tags: ["senior"], excluded: true },
      ],
    },
  });
  expect(resp.ok()).toBe(true);
}

test.beforeEach(async ({ page, request }) => {
  await seedTeam(request);
  await h.showEntriesOnLoad(page);
  await page.goto("/");
  await h.waitForLiveUpdates(page);
});

test("choosing a Tag filter highlights the Eligible pool and its Pot", async ({ page }) => {
  await expect.poll(() => h.tableMarking(page)).toEqual({
    eligible: ["Ada", "Bob", "Cy", "Dee"],
    pot: ["Bob", "Dee"],
  });

  await h.chooseFilterTag(page, "must_have", "senior");
  await expect.poll(() => h.tableMarking(page)).toEqual({ eligible: ["Ada", "Dee"], pot: ["Dee"] });

  // Ada alone is the Eligible pool, so nobody is marked as in the Pot.
  await h.chooseFilterTag(page, "any_of", "backend");
  await expect.poll(() => h.tableMarking(page)).toEqual({ eligible: ["Ada"], pot: [] });
});

test("the Pot marking moves after a Draw", async ({ page }) => {
  await h.chooseFilterTag(page, "any_of", "frontend");
  await expect.poll(() => h.tableMarking(page)).toEqual({ eligible: ["Cy", "Dee"], pot: ["Dee"] });

  expect(await h.clickDraw(page)).toBe("Dee");

  // Cy and Dee both have one Pick now: the whole Eligible pool is the Pot.
  await expect.poll(() => h.tableMarking(page), { timeout: 15_000 }).toEqual({
    eligible: ["Cy", "Dee"],
    pot: [],
  });
});

test("after a Reset nobody is marked as in the Pot", async ({ page }) => {
  await expect.poll(() => h.tableMarking(page)).toEqual({
    eligible: ["Ada", "Bob", "Cy", "Dee"],
    pot: ["Bob", "Dee"],
  });

  await h.clickReset(page);

  await expect.poll(() => h.pickCountsInTable(page)).toEqual({ Ada: 0, Bob: 0, Cy: 0, Dee: 0, Eve: 0 });
  expect(await h.tableMarking(page)).toEqual({ eligible: ["Ada", "Bob", "Cy", "Dee"], pot: [] });
});

test("a table refresh from another browser keeps the Tag filter's highlighting", async ({ page, request }) => {
  await h.chooseFilterTag(page, "must_have", "frontend");
  await expect.poll(() => h.tableMarking(page)).toEqual({ eligible: ["Cy", "Dee"], pot: ["Dee"] });

  await h.otherBrowserChangesEntry(request, "Dee", { pick_count: 3 });

  await expect(h.pickCountField(page, "Dee")).toHaveValue("3");
  expect(await h.tableMarking(page)).toEqual({ eligible: ["Cy", "Dee"], pot: ["Cy"] });
});
