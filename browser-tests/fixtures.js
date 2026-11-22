// Shared fixtures for every browser test. Import `test` and `expect` from
// here, never from @playwright/test directly, so each test gets:
//   - a reset app (scores, history, entries and List name) before it starts;
//   - a failure on any uncaught page error (the suite-wide safety net).
// Test state is set up through the app's own HTTP interface, never SQL.
const base = require("@playwright/test");

const DEFAULT_LIST_NAME = "Entries";

// Puts the app back to an empty List with the default name.
async function resetApp(request) {
  await ok(request.post("/api/reset"));
  await ok(request.delete("/api/entries"));
  await setListName(request, DEFAULT_LIST_NAME);
}

// Imports Entries by name through the import endpoint.
async function seedEntries(request, names) {
  await ok(request.post("/api/import", { data: { entries: names.map((name) => ({ name })) } }));
}

async function setListName(request, name) {
  await ok(request.put("/api/list-name", { data: { name } }));
}

async function ok(pending) {
  const resp = await pending;
  if (!resp.ok()) throw new Error(`${resp.url()} -> ${resp.status()}: ${await resp.text()}`);
  return resp;
}

const test = base.test.extend({
  page: async ({ page, request }, use) => {
    await resetApp(request);
    const pageErrors = [];
    page.on("pageerror", (err) => pageErrors.push(err));
    await use(page);
    base.expect(pageErrors.map((e) => e.stack || String(e)), "uncaught page errors").toEqual([]);
  },
});

module.exports = { test, expect: base.expect, resetApp, seedEntries, setListName, DEFAULT_LIST_NAME };
