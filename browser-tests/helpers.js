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
  if ((await toggle.innerText()).trim() === "Show entries") await toggle.click();
  await expect(toggle).toHaveText("Hide entries", { useInnerText: true });
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

// The resting Reel as the host sees it: the winner's name and the visible
// cells above/before and below/after it. `winner` is null when no cell won.
async function restingReel(page) {
  return page.locator("#result .reel-track .reel-cell").evaluateAll((cells) => {
    const shown = cells.filter((c) => c.checkVisibility());
    const i = shown.findIndex((c) => c.classList.contains("is-winner"));
    const names = shown.map((c) => c.textContent.trim());
    if (i < 0) return { winner: null, above: [], below: [] };
    return { winner: names[i], above: names.slice(0, i), below: names.slice(i + 1) };
  });
}

// Waits for the Reel to start moving and then come to rest, the way the
// audience sees it, and returns the name centred in the Reel window at the
// moment it stopped. Reads only on-screen geometry.
async function nextLanding(page, { timeout = 15_000 } = {}) {
  return page.evaluate(
    (timeout) =>
      new Promise((resolve, reject) => {
        const deadline = performance.now() + timeout;
        const reel = () => document.querySelector("#result .reel");
        const position = () => {
          const cells = [...document.querySelectorAll("#result .reel-track .reel-cell")];
          if (!cells.length) return null;
          const r = cells[0].getBoundingClientRect();
          return `${cells.length}:${cells[0].textContent}:${r.left.toFixed(1)}:${r.top.toFixed(1)}`;
        };
        const centred = () => {
          const w = reel().getBoundingClientRect();
          const cx = w.left + w.width / 2;
          const cy = w.top + w.height / 2;
          const cell = [...document.querySelectorAll("#result .reel-track .reel-cell")].find((c) => {
            const r = c.getBoundingClientRect();
            return r.left <= cx && cx < r.right && r.top <= cy && cy < r.bottom;
          });
          return cell ? cell.textContent.trim() : null;
        };
        let last = position();
        let moved = false;
        let still = 0;
        const step = () => {
          if (performance.now() > deadline) return reject(new Error(moved ? "Reel never stopped" : "Reel never moved"));
          const now = position();
          if (now !== last) {
            moved = true;
            still = 0;
          } else if (moved && ++still >= 8) {
            return resolve(centred());
          }
          last = now;
          requestAnimationFrame(step);
        };
        requestAnimationFrame(step);
      }),
    timeout
  );
}

// Resolves once the Reel is visibly moving.
async function reelMoving(page) {
  await page.evaluate(
    () =>
      new Promise((resolve) => {
        const pos = () => {
          const r = document.querySelector("#result .reel-track .reel-cell")?.getBoundingClientRect();
          return r ? `${r.left},${r.top}` : undefined;
        };
        let last = pos();
        const step = () => {
          const now = pos();
          if (now !== undefined && last !== undefined && now !== last) return resolve();
          last = now;
          requestAnimationFrame(step);
        };
        requestAnimationFrame(step);
      })
  );
}

// Clicks Draw and returns the drawn winner's name from the server's reply.
async function clickDraw(page) {
  await expect(page.locator("#ws-status")).toHaveClass(/\bconnected\b/);
  const reply = page.waitForResponse((r) => r.url().endsWith("/api/draw"));
  await page.locator("#draw-btn").click();
  return (await (await reply).json()).winner.name;
}

// The host's Pick History, from the API.
async function history(request) {
  return (await request.get("/api/history")).json();
}

// The names in the Pick History card, newest first, as rendered.
async function historyNames(page) {
  return page.locator("#history .history-name").allTextContents();
}

// The Pick counts shown in the Entries table, by Entry name.
async function pickCountsInTable(page) {
  return page.locator("#entry-table [data-id]").evaluateAll((rows) =>
    Object.fromEntries(
      rows.map((r) => [r.querySelector('[data-field="name"]').value, Number(r.querySelector('[data-field="pick_count"]').value)])
    )
  );
}

// True if the Reel's belt does not move for `ms`. A re-rendered result (a
// new belt in place) is not motion; a running Reel is.
async function reelStaysStill(page, ms) {
  return page.evaluate(
    (ms) =>
      new Promise((resolve) => {
        let belt = null;
        let at = null;
        const end = performance.now() + ms;
        const step = () => {
          const now = document.querySelector("#result .reel-track");
          const r = now && now.getBoundingClientRect();
          const pos = r && `${r.left.toFixed(1)}:${r.top.toFixed(1)}`;
          if (now === belt && pos !== at) return resolve(false);
          belt = now;
          at = pos;
          if (performance.now() > end) return resolve(true);
          requestAnimationFrame(step);
        };
        requestAnimationFrame(step);
      }),
    ms
  );
}

// The server replays a Reveal to browsers that connect within its replay
// window: the longest Reveal (raffle.Timing) plus a margin, under 6s.
const REPLAY_WINDOW_MS = 6000;

// Waits until a Draw made at `drawnAt` (Date.now()) can no longer be replayed.
async function waitOutReplayWindow(page, drawnAt) {
  const left = drawnAt + REPLAY_WINDOW_MS + 500 - Date.now();
  if (left > 0) await page.waitForTimeout(left);
}

// Keeps the page from ever getting a live websocket.
async function blockWebSocket(page) {
  await page.routeWebSocket(/\/ws$/, (ws) => ws.close());
}

// Waits until the page receives other browsers' changes live.
async function waitForLiveUpdates(page) {
  await expect(page.locator("#ws-status")).toHaveClass(/\bconnected\b/);
}

// Another browser changes the named Entry (fields as in PUT /api/entries/{id}).
async function otherBrowserChangesEntry(request, name, change) {
  const entries = await (await request.get("/api/entries")).json();
  const entry = entries.find((e) => e.name === name);
  if (!entry) throw new Error(`no Entry named ${name}`);
  const resp = await request.put(`/api/entries/${entry.id}`, { data: change });
  if (!resp.ok()) throw new Error(`change ${name}: ${resp.status()}`);
}

const exactly = (text) => new RegExp(`^${text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`);

// The table row of the Entry the server knows by this name.
function entryRow(page, name) {
  return page
    .locator("#entry-table [data-id]")
    .filter({ has: page.locator('textarea[data-field="name"]', { hasText: exactly(name) }) });
}

function nameField(page, name) {
  return entryRow(page, name).locator('textarea[data-field="name"]');
}

function pickCountField(page, name) {
  return entryRow(page, name).locator('input[data-field="pick_count"]');
}

// Records which regions the page fetches from now on; returns a reader.
function recordRegionFetches(page) {
  const seen = [];
  page.on("request", (req) => {
    const path = new URL(req.url()).pathname;
    if (path.startsWith("/partials/") || path === "/api/list-name") seen.push(path);
  });
  return () => [...seen];
}

const sections = {
  history: { body: "#history", toggle: "#history-toggle-btn" },
  entries: { body: "#entry-table", toggle: "#entries-toggle-btn" },
};

// Asserts a collapsible section is (not) on screen and its toggle says so.
async function expectSection(page, section, visible) {
  const s = sections[section];
  await expect(page.locator(s.toggle)).toHaveText(`${visible ? "Hide" : "Show"} ${section}`, {
    useInnerText: true,
  });
  await expect
    .poll(() =>
      page.locator(s.body).evaluate((el) => {
        const box = el.getBoundingClientRect();
        const onScreen = box.width > 0 && box.height > 0 && box.right > 0 && box.left < innerWidth;
        return onScreen && el.checkVisibility({ checkOpacity: true, checkVisibilityCSS: true });
      })
    )
    .toBe(visible);
}

// Opens the app in a second, independent browser (its own storage). Close it
// with `page.context().close()`.
async function secondBrowser(browser, testInfo) {
  const context = await browser.newContext({ baseURL: testInfo.project.use.baseURL });
  const page = await context.newPage();
  await page.goto("/");
  return page;
}

// Adds a Tag to the Tag filter the way the host does: open the filter, press
// the zone's "+" ("must_have" or "any_of") and pick the Tag.
async function chooseFilterTag(page, zone, tag) {
  const accordion = page.locator("#filter-accordion");
  if (!(await accordion.evaluate((d) => d.open))) await accordion.locator("summary").click();
  await page.locator(`#filter-bar .tag-add[data-zone="${zone}"]`).click();
  await page.locator(`#filter-picker [data-action="pick-filter-tag"][data-tag="${tag}"]`).click();
  await expect(page.locator(`#filter-bar [data-zone="${zone}"] .tag-bubble[data-tag="${tag}"]`)).toBeVisible();
}

// What the table shows for the next Draw: the names highlighted as in the
// Eligible pool and the names marked as in the Pot, each sorted.
async function tableMarking(page) {
  return page.locator("#entry-table [data-id]").evaluateAll((rows) => {
    const name = (r) => r.querySelector('textarea[data-field="name"]').value;
    const names = (keep) => rows.filter(keep).map(name).sort();
    return {
      eligible: names((r) => !r.classList.contains("row-ineligible")),
      pot: names((r) => r.classList.contains("row-pot")),
    };
  });
}

module.exports = {
  chooseFilterTag,
  tableMarking,
  waitForLiveUpdates,
  otherBrowserChangesEntry,
  nameField,
  pickCountField,
  recordRegionFetches,
  expectSection,
  secondBrowser,
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
  restingReel,
  blockWebSocket,
  historyNames,
  pickCountsInTable,
  reelStaysStill,
  waitOutReplayWindow,
  nextLanding,
  reelMoving,
  clickDraw,
  history,
};
