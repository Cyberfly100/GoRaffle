// Host-level actions and reads shared by the browser tests. Each one does what
// the host would do (click, choose a file) or reads what the host sees.
const { expect } = require("@playwright/test");

// Makes the Entries table visible from the first paint (the host's saved
// "Show entries" preference) for every page load in this test.
async function showEntriesOnLoad(page) {
  await page.addInitScript(() => localStorage.setItem("raffle-entries-visible", "1"));
}

// Makes the vertical Reel the saved preference for every page load.
async function preferVerticalReel(page) {
  await page.addInitScript(() => localStorage.setItem("raffle-reel-mode", "v"));
}

// Opens the Entries card the way the host does, if it is collapsed.
async function showEntries(page) {
  const toggle = page.locator("#entries-toggle-btn");
  if ((await toggle.textContent()).trim() === "Show entries") await toggle.click();
  await expect(toggle).toHaveText("Hide entries");
}

// Opens the Import / Export dialog (it lives in the Entries card).
async function openImportExport(page) {
  await showEntries(page);
  await page.locator("#ie-open-btn").click();
  await expect(page.locator("#ie-dialog")).toBeVisible();
}

async function closeImportExport(page) {
  await page.locator("#ie-close-btn").click();
  await expect(page.locator("#ie-dialog")).toBeHidden();
}

// Chooses an in-memory file in the import dialog and clicks Import.
// `file` is { name, mimeType, buffer }.
async function importFile(page, file) {
  await page.locator("#import-file").setInputFiles(file);
  await expect(page.locator("#file-chosen")).toHaveText(file.name);
  await page.locator("#import-btn").click();
}

function jsonFile(name, body) {
  return { name, mimeType: "application/json", buffer: Buffer.from(JSON.stringify(body)) };
}

function csvFile(name, text) {
  return { name, mimeType: "text/csv", buffer: Buffer.from(text) };
}

// The toast the host sees.
function toast(page) {
  return page.locator("#toast");
}

// The Entry names shown in the table, in order.
async function entryNamesInTable(page) {
  return page.locator('#entry-table textarea[data-field="name"]').evaluateAll((els) => els.map((e) => e.value));
}

// The two export links' targets, parsed.
async function exportLinkNames(page) {
  const hrefs = await page.locator("#import-export .export-row a").evaluateAll((as) => as.map((a) => a.href));
  return hrefs.map((h) => new URL(h).searchParams.get("name"));
}

// Renames the List the way the host does: type in the field and press Enter.
// Waits for the debounced save to reach the server.
async function renameList(page, name) {
  const saved = page.waitForResponse((r) => r.url().endsWith("/api/list-name") && r.request().method() === "PUT");
  const field = page.locator("#list-name");
  await field.fill(name);
  await field.press("Enter");
  await saved;
}

// Keeps the page from ever getting a live websocket.
async function blockWebSocket(page) {
  await page.routeWebSocket(/\/ws$/, (ws) => ws.close());
}

module.exports = {
  showEntriesOnLoad,
  showEntries,
  preferVerticalReel,
  openImportExport,
  closeImportExport,
  importFile,
  jsonFile,
  csvFile,
  toast,
  entryNamesInTable,
  exportLinkNames,
  renameList,
  blockWebSocket,
};
