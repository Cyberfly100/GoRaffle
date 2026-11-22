// Page wiring: theme, mutations, filters, tags, partial refresh, and the
// glue between the websocket, the Draw button and the Reel.
import * as ws from "./ws.js";
import { createReel } from "./reel.js";
import { audioCtx, bindSoundToggle, followReel, soundOn } from "./sound.js";

const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];

/* ---------- Toast ---------- */
function toast(msg) {
  let el = $("#toast");
  if (!el) {
    el = document.createElement("div");
    el.id = "toast";
    document.body.appendChild(el);
  }
  el.textContent = msg;
  el.classList.add("show");
  clearTimeout(el._t);
  el._t = setTimeout(() => el.classList.remove("show"), 2600);
}

async function api(method, url, body) {
  const opts = { method, headers: {} };
  if (body !== undefined) {
    opts.headers["Content-Type"] = "application/json";
    opts.body = JSON.stringify(body);
  }
  const resp = await fetch(url, opts);
  let data = {};
  try {
    data = await resp.json();
  } catch {}
  if (!resp.ok) throw new Error(data.error || `Request failed (${resp.status})`);
  return data;
}

/* ---------- Theme ---------- */
const rootEl = document.documentElement;
function applyTheme(theme) {
  if (theme === "auto") rootEl.removeAttribute("data-theme");
  else rootEl.setAttribute("data-theme", theme);
  localStorage.setItem("raffle-theme", theme);
}
(function initTheme() {
  const saved = localStorage.getItem("raffle-theme") || "auto";
  applyTheme(saved);
  $("#theme-toggle")?.addEventListener("click", () => {
    const cur = rootEl.getAttribute("data-theme") || "auto";
    const next = cur === "auto"
      ? (matchMedia("(prefers-color-scheme: dark)").matches ? "light" : "dark")
      : cur === "dark" ? "light" : "auto";
    applyTheme(next);
  });
  matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => {
    if (!rootEl.hasAttribute("data-theme")) rootEl.removeAttribute("data-theme");
  });
})();


/* ---------- Reel ---------- */
const reel = createReel({
  orientation: localStorage.getItem("raffle-reel-mode") === "v" ? "vertical" : "horizontal",
});
followReel(reel);

/* ---------- Partial refresh ---------- */
// Fetch-based refresh: independent of any CDN so partials always re-render.
// Regions that would show the winner (or tear down the Reel) before it has
// landed. While the Reel is busy their refreshes are held back; the Reel's
// idle notification applies them (refreshPartials) once it has landed.
const HELD_DURING_REVEAL = new Set(["#result", "#history", "#entry-table"]);
const heldBack = (selector) => HELD_DURING_REVEAL.has(selector) && reel.busy();

async function swapPartial(url, selector) {
  // Asked again once the response is in: a refresh fetched just before a
  // spin must not land during it.
  if (heldBack(selector)) return;
  if (!document.querySelector(selector)) return;
  let resp;
  try {
    resp = await fetch(url);
  } catch {
    return;
  }
  if (!resp.ok) return;
  const html = await resp.text();
  if (heldBack(selector)) return;
  const target = document.querySelector(selector);
  if (!target) return;
  const wrap = document.createElement("template");
  wrap.innerHTML = html;
  const node = wrap.content.firstElementChild;
  if (!node) return;

  // Replacing the section rebuilds its scrollports (resetting their
  // position) and drops a focused input; capture both so the table
  // neither jumps nor steals the caret mid-edit.
  const scroller = target.querySelector(".rows-scroll");
  const scrollTop = scroller ? scroller.scrollTop : 0;
  const tableEl = target.querySelector(".table");
  const scrollLeft = tableEl ? tableEl.scrollLeft : 0;
  const focus = captureFocus(target);

  target.replaceWith(node);

  // Fit the name fields (and restore any in-flight edit, which also changes
  // their height) BEFORE pinning the scrollports, so the saved offsets are
  // applied and clamped against the final layout, not the collapsed
  // one-line textareas.
  restoreFocus(node, focus);
  syncNameFieldHeights(node);

  const newScroller = node.querySelector(".rows-scroll");
  if (newScroller) newScroller.scrollTop = Math.min(scrollTop, newScroller.scrollHeight);
  const newTable = node.querySelector(".table");
  if (newTable) newTable.scrollLeft = Math.min(scrollLeft, newTable.scrollWidth);

  if (selector === "#filter-bar") renderFilterZones();
  syncHistoryBtnLabel();
  syncEntriesBtnLabel();
  updateEligibility();
  if (selector === "#result") {
    syncDrawBtn();
    reel.dress();
  }
}


// The swap would silently discard an edit that has not blurred yet, so the
// in-flight value is captured and put back after the re-render.
function captureFocus(target) {
  const el = document.activeElement;
  if (!el || !target.contains(el)) return null;
  const row = el.closest("[data-id]");
  return {
    id: el.id || null,
    rowId: row ? row.dataset.id : null,
    field: el.dataset?.field || null,
    value: el.value,
    selStart: el.selectionStart,
    selEnd: el.selectionEnd,
  };
}

function restoreFocus(root, state) {
  if (!state) return;
  let el = null;
  if (state.id) el = root.querySelector("#" + state.id);
  else if (state.rowId && state.field)
    el = root.querySelector(`[data-id="${state.rowId}"] [data-field="${state.field}"]`);
  if (!el || typeof el.focus !== "function") return;
  el.focus();
  if (state.field === "pick_count" || state.field === "excluded") return;
  el.value = state.value;
  try {
    el.setSelectionRange(state.selStart, state.selEnd);
  } catch {}
}

function refreshPartials() {
  swapPartial("/partials/table", "#entry-table");
  swapPartial("/partials/filter", "#filter-bar");
  swapPartial("/partials/history", "#history");
  swapPartial("/partials/result", "#result");
}

function refreshIfNoWS() {
  if (!ws.connected()) refreshPartials();
}


/* ---------- Draw ---------- */
// Draw is disabled from the click until the Reveal starts, then while the
// Reel is busy with anyone's Reveal.
let drawPending = false;

function syncDrawBtn() {
  const btn = $("#draw-btn");
  if (btn) btn.disabled = drawPending || reel.busy();
}

function setDrawPending(pending) {
  drawPending = pending;
  syncDrawBtn();
}

function selectedTags(zone) {
  return filterState[zone] || [];
}

async function doDraw() {
  if (drawPending || reel.busy()) return null;
  setDrawPending(true);
  // Clicking Draw is the user gesture that lets audio play.
  if (soundOn()) audioCtx();
  const must_have = selectedTags("must_have");
  const any_of = selectedTags("any_of");
  let resp;
  try {
    resp = await fetch("/api/draw", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ must_have, any_of }),
    });
  } catch {
    toast("Could not reach the server.");
    setDrawPending(false);
    return null;
  }
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) {
    toast(data.error || "Draw failed.");
    setDrawPending(false);
    return null;
  }
  // The response is the same Reveal the websocket carries: play whichever
  // arrives first, so the Draw works without a websocket too.
  playReveal(data);
  setDrawPending(false);
  return data;
}

/* ---------- Reveal ---------- */
// Each Reveal plays once in this browser, identified by its Pick id, however
// many times it arrives (HTTP response, websocket event, replay on connect).
const playedPicks = new Set();

function playReveal(reveal) {
  const winner = reveal && reveal.winner && reveal.winner.name;
  if (!winner || playedPicks.has(reveal.pick_id)) return;
  playedPicks.add(reveal.pick_id);
  reel.reveal(reveal.suspense_names, winner);
  syncDrawBtn();
}

reel.onIdle(() => {
  setDrawPending(false);
  // Applies the refreshes held back during the Reveal, and shows its Pick.
  refreshPartials();
});

/* ---------- Reel orientation ---------- */
function syncReelToggle() {
  const btn = $("#reel-toggle");
  if (!btn) return;
  const vertical = reel.orientation() === "vertical";
  btn.title = vertical ? "Spinner: vertical — click for horizontal" : "Spinner: horizontal — click for vertical";
  btn.setAttribute("aria-pressed", vertical ? "true" : "false");
}

function bindReelToggle() {
  syncReelToggle();
  $("#reel-toggle")?.addEventListener("click", () => {
    const next = reel.orientation() === "vertical" ? "horizontal" : "vertical";
    // Mid-Reveal the belt's geometry is fixed on one axis.
    if (!reel.setOrientation(next)) {
      toast("Wait for the draw to finish.");
      return;
    }
    localStorage.setItem("raffle-reel-mode", next === "vertical" ? "v" : "h");
    syncReelToggle();
    // The resting track was dressed for the old orientation (or left bare
    // for the new one); re-render it so the drum matches.
    swapPartial("/partials/result", "#result");
  });
}

/* ---------- Row mutations ---------- */
// Textarea name cells size themselves to their content so wrapped names
// show every line instead of clipping.
function fitNameField(el) {
  el.style.height = "auto";
  // +2 covers the borders, which border-box height includes but
  // scrollHeight does not.
  el.style.height = el.scrollHeight + 2 + "px";
}

function syncNameFieldHeights(root) {
  for (const el of (root || document).querySelectorAll("textarea.cell.name")) fitNameField(el);
}

async function patchRow(field, id, value) {
  const body = {};
  if (field === "excluded") body.excluded = !!value;
  else if (field === "pick_count") body.pick_count = Math.max(0, parseInt(value, 10) || 0);
  else if (field === "name") {
    const name = String(value).trim();
    if (!name) return;
    body.name = name;
  }
  try {
    await api("PUT", `/api/entries/${id}`, body);
    refreshIfNoWS();
  } catch (err) {
    toast(err.message);
  }
}

function bindRowEvents() {
  document.addEventListener("change", (e) => {
    const el = e.target;
    const row = el.closest("[data-id]");
    const field = el.dataset?.field;
    if (!row || !field) return;
    // Checkboxes report their checked state, not the value attribute.
    const val = el.type === "checkbox" ? el.checked : el.value;
    patchRow(field, row.dataset.id, val);
  });

  document.addEventListener("input", (e) => {
    if (e.target instanceof HTMLTextAreaElement && e.target.classList.contains("cell")) {
      fitNameField(e.target);
    }
  });

  // Enter commits a rename (blur fires the change handler); Shift+Enter
  // still inserts a literal line break.
  document.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && !e.shiftKey && e.target instanceof HTMLTextAreaElement && e.target.classList.contains("cell")) {
      e.preventDefault();
      e.target.blur();
    }
  });

  document.addEventListener("change", (e) => {
    if (e.target && e.target.id === "import-file") {
      const file = e.target.files && e.target.files[0];
      const nameEl = $("#file-chosen");
      const btn = $("#import-btn");
      if (nameEl) nameEl.textContent = file ? file.name : "No file chosen";
      if (btn) btn.disabled = !file;
    }
  });

  document.addEventListener("click", async (e) => {
    // Controls that live inside swapped partials are bound by delegation
    // so they survive partial refreshes.
    const btnId = e.target.closest("button")?.id;
    if (btnId === "add-entry-btn") {
      addEntry();
      return;
    }
    if (btnId === "import-btn") {
      doImport();
      return;
    }
    if (btnId === "clear-entries-btn") {
      doClearEntries();
      return;
    }
    if (btnId === "reset-btn") {
      doReset();
      return;
    }
    if (btnId === "draw-btn") {
      doDraw();
      return;
    }
    if (btnId === "undo-btn") {
      doUndo();
      return;
    }
    if (btnId === "tag-dialog-close") {
      $("#tag-dialog")?.close();
      return;
    }

    const btn = e.target.closest("[data-action]");
    if (!btn) return;
    const action = btn.dataset.action;
    const row = btn.closest("[data-id]");
    const id = row ? row.dataset.id : null;

    if (action === "delete") {
      if (!id || !confirm("Delete this entry?")) return;
      try {
        await api("DELETE", `/api/entries/${id}`);
        refreshIfNoWS();
      } catch (err) {
        toast(err.message);
      }
    } else if (action === "remove-tag") {
      const bubble = btn.closest(".tag-bubble");
      if (!id || !bubble?.dataset.tagId) return;
      try {
        await api("DELETE", `/api/entries/${id}/tags/${bubble.dataset.tagId}`);
        refreshIfNoWS();
      } catch (err) {
        toast(err.message);
      }
    } else if (action === "open-tags") {
      if (!id) return;
      openTagDialog(id);
    } else if (action === "add-existing-tag") {
      await addTagToEntry(btn.dataset.tag, currentTagEntry());
    }
  });
}

/* ---------- Add entry ---------- */
async function addEntry() {
  const input = $("#new-name");
  const name = input.value.trim();
  if (!name) return;
  try {
    await api("POST", "/api/entries", { name });
    input.value = "";
  } catch (err) {
    toast(err.message);
  }
}

/* ---------- Tags ---------- */
let tagEntry = null;
const currentTagEntry = () => tagEntry;

async function openTagDialog(id) {
  tagEntry = id;
  const dialog = $("#tag-dialog");
  const content = $("#tag-dialog-content");
  if (!dialog || !content) return;
  try {
    const resp = await fetch(`/partials/tag-popover/${id}`);
    content.innerHTML = await resp.text();
    dialog.showModal();
  } catch {
    toast("Could not load tags.");
  }
}

async function addTagToEntry(name, id) {
  if (!name || !id) return;
  try {
    await api("POST", `/api/entries/${id}/tags`, { name });
    $("#tag-dialog")?.close();
    refreshIfNoWS();
  } catch (err) {
    toast(err.message);
  }
}

function bindTagDialog() {
  document.addEventListener("submit", (e) => {
    if (e.target.id !== "tag-new-form") return;
    e.preventDefault();
    const input = e.target.querySelector('input[name="name"]');
    addTagToEntry(input.value.trim(), tagEntry);
  });
}

/* ---------- Filter ---------- */
// Selected tags live client-side only; the zones render as bubbles + a "+"
// that opens the picker. State survives partial refreshes (refiltered on swap).
const filterState = { must_have: [], any_of: [] };
let filterPickerZone = "must_have";

// Palette classes are assigned server-side (template tagClass); reuse the
// rendered chip's class so the FNV hash lives in one place.
function tagClass(name) {
  const chip = $(`#filter-picker [data-tag="${name.replace(/"/g, '\\"')}"]`);
  if (chip) {
    const tc = [...chip.classList].find((c) => c.startsWith("tc-"));
    if (tc) return tc;
  }
  return "tc-0";
}

function renderFilterZones() {
  for (const zone of ["must_have", "any_of"]) {
    const holder = $(`#filter-bar [data-zone="${zone}"]`);
    if (!holder) continue;
    holder.replaceChildren();
    for (const tag of filterState[zone]) {
      const bubble = document.createElement("span");
      bubble.className = "tag-bubble " + tagClass(tag);
      bubble.dataset.tag = tag;
      const x = document.createElement("button");
      x.className = "tag-x";
      x.dataset.action = "remove-filter-tag";
      x.dataset.zone = zone;
      x.dataset.tag = tag;
      x.title = "Remove tag";
      x.textContent = "×";
      bubble.append(tag, x);
      holder.appendChild(bubble);
    }
  }
  const total = filterState.must_have.length + filterState.any_of.length;
  const badge = $("#filter-count");
  if (badge) badge.textContent = total > 0 ? String(total) : "";
  updateEligibility();
}

function openFilterPicker(zone) {
  filterPickerZone = zone;
  const label = $("#filter-picker-zone");
  if (label) label.textContent = zone === "must_have" ? "Must have" : "Any of";
  $("#filter-picker")?.showModal();
}

function pickFilterTag(tag) {
  if (!filterState[filterPickerZone].includes(tag)) {
    filterState[filterPickerZone].push(tag);
  }
  renderFilterZones();
  $("#filter-picker")?.close();
}

function removeFilterTag(zone, tag) {
  filterState[zone] = filterState[zone].filter((t) => t !== tag);
  renderFilterZones();
}

function clearFilter() {
  filterState.must_have = [];
  filterState.any_of = [];
  renderFilterZones();
}

function bindFilter() {
  document.addEventListener("click", (e) => {
    const btn = e.target.closest("[data-action]");
    if (!btn) return;
    const action = btn.dataset.action;
    if (action === "open-filter-picker") openFilterPicker(btn.dataset.zone);
    else if (action === "pick-filter-tag") pickFilterTag(btn.dataset.tag);
    else if (action === "remove-filter-tag") removeFilterTag(btn.dataset.zone, btn.dataset.tag);
  });
  document.addEventListener("click", (e) => {
    if (e.target.closest("#clear-filter")) clearFilter();
    if (e.target.closest("#filter-picker-close")) $("#filter-picker")?.close();
  });
  renderFilterZones();
}

/* ---------- Undo / Reset ---------- */
async function doUndo() {
  try {
    const data = await api("POST", "/api/undo");
    toast(data.info || "Undo complete.");
    refreshIfNoWS();
  } catch (err) {
    toast(err.message);
  }
}

async function doReset() {
  if (!confirm("Reset all scores and clear history?")) return;
  try {
    await api("POST", "/api/reset");
    refreshIfNoWS();
  } catch (err) {
    toast(err.message);
  }
}

/* ---------- Import / Export ---------- */
async function doImport() {
  const file = $("#import-file")?.files?.[0];
  if (!file) {
    toast("Choose a .json or .csv file first.");
    return;
  }
  if (file.size > 10 * 1024 * 1024) {
    toast("File is too large.");
    return;
  }
  const form = new FormData();
  form.append("file", file);
  let resp;
  try {
    resp = await fetch("/api/import", { method: "POST", body: form });
  } catch {
    toast("Could not reach the server.");
    return;
  }
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) {
    toast(data.error || "Import failed.");
    return;
  }
  toast(`Imported: ${data.added} added, ${data.updated} updated.`);
  if (data.list_name) {
    const input = $("#list-name");
    if (input) {
      input.value = data.list_name;
      updateExportLinks(data.list_name);
    }
  }
  const fileInput = $("#import-file");
  if (fileInput) fileInput.value = "";
  const nameEl = $("#file-chosen");
  if (nameEl) nameEl.textContent = "No file chosen";
  const btn = $("#import-btn");
  if (btn) btn.disabled = true;
  refreshIfNoWS();
}

async function doClearEntries() {
  if (!confirm("Empty the ENTIRE entry list? This deletes all state. Consider exporting first.")) return;
  try {
    await api("DELETE", "/api/entries");
    refreshIfNoWS();
  } catch (err) {
    toast(err.message);
  }
}

/* ---------- Eligibility visual ---------- */
function matchesFilter(tags) {
  const has = [...tags];
  if (filterState.must_have.length && !filterState.must_have.every((t) => has.includes(t))) return false;
  if (filterState.any_of.length && !filterState.any_of.some((t) => has.includes(t))) return false;
  return true;
}

function updateEligibility() {
  for (const row of $$("#entry-table-body [data-id]")) {
    const excluded = row.querySelector('input[data-field="excluded"]')?.checked;
    const tags = $$(".tag-bubble[data-tag]", row).map((b) => b.dataset.tag);
    const ineligible = !!excluded || !matchesFilter(tags);
    row.classList.toggle("row-ineligible", ineligible);
    row.title = ineligible ? "Not eligible for the next draw" : "";
  }
}


/* ---------- WS events ---------- */
function bindWS() {
  ws.on("reveal", playReveal);
  ws.on("undo", () => refreshPartials());
  ws.on("entries", () => refreshPartials());
}

/* ---------- DB health ---------- */
async function checkDb() {
  const el = $("#db-status");
  if (!el) return;
  const mark = (ok, title) => {
    el.className = "db-icon " + (ok ? "db-ok" : "db-down");
    el.title = title;
  };
  const ctrl = new AbortController();
  const t = setTimeout(() => ctrl.abort(), 3000);
  try {
    const resp = await fetch("/api/health", { signal: ctrl.signal });
    const data = await resp.json().catch(() => ({}));
    clearTimeout(t);
    mark(data.db === true, data.db === true ? "database reachable" : "database unreachable");
  } catch {
    clearTimeout(t);
    mark(false, "database unreachable");
  }
}

/* ---------- Import / Export dialog ---------- */
function bindIEDialog() {
  const dialog = $("#ie-dialog");
  if (!dialog) return;
  document.addEventListener("click", (e) => {
    if (e.target.closest("#ie-open-btn")) {
      const input = $("#list-name");
      if (input) updateExportLinks(input.value);
      dialog.showModal();
    } else if (e.target.closest("#ie-close-btn")) {
      dialog.close();
    } else if (e.target === dialog) {
      dialog.close(); // backdrop click
    }
  });
}

/* ---------- History show/hide ---------- */
function syncHistoryBtnLabel() {
  const btn = $("#history-toggle-btn");
  if (!btn) return;
  btn.textContent =
    localStorage.getItem("raffle-history-visible") === "1" ? "Hide history" : "Show history";
}

function setHistoryVisible(visible, animate) {
  const grid = $(".grid");
  if (!grid) return;
  if (!animate) grid.classList.add("grid-no-anim");
  grid.classList.toggle("no-history", !visible);
  if (!animate) requestAnimationFrame(() => grid.classList.remove("grid-no-anim"));
  syncHistoryBtnLabel();
}

function bindHistoryToggle() {
  const saved = localStorage.getItem("raffle-history-visible") === "1";
  setHistoryVisible(saved, false);
  // #history-toggle-btn lives in the swapped result partial, so handle it
  // via delegation rather than a direct listener.
  document.addEventListener("click", (e) => {
    if (e.target.closest("#history-toggle-btn")) {
      const next = localStorage.getItem("raffle-history-visible") !== "1";
      localStorage.setItem("raffle-history-visible", next ? "1" : "0");
      setHistoryVisible(next, true);
    }
  });
}

/* ---------- Entries show/hide ---------- */
function syncEntriesBtnLabel() {
  const btn = $("#entries-toggle-btn");
  if (!btn) return;
  btn.textContent =
    localStorage.getItem("raffle-entries-visible") === "1" ? "Hide entries" : "Show entries";
}

function setEntriesVisible(visible, animate) {
  const grid = $(".grid");
  if (!grid) return;
  if (!animate) grid.classList.add("grid-no-anim");
  grid.classList.toggle("no-entries", !visible);
  if (!animate) requestAnimationFrame(() => grid.classList.remove("grid-no-anim"));
  syncEntriesBtnLabel();
}

function bindEntriesToggle() {
  const saved = localStorage.getItem("raffle-entries-visible") === "1";
  setEntriesVisible(saved, false);
  // #entries-toggle-btn lives in the swapped result partial, so handle it
  // via delegation rather than a direct listener.
  document.addEventListener("click", (e) => {
    if (e.target.closest("#entries-toggle-btn")) {
      const next = localStorage.getItem("raffle-entries-visible") !== "1";
      localStorage.setItem("raffle-entries-visible", next ? "1" : "0");
      setEntriesVisible(next, true);
    }
  });
}

/* ---------- List Name ---------- */
// The server renders the current List name into the field and the export
// links, so nothing needs correcting after load.
let listNameDebounce = null;

function updateExportLinks(name) {
  const safeName = name.trim() || "raffle";
  // Found by a stable identity, not by their address, so every rename
  // (not just the first) rewrites them.
  for (const link of $$("a[data-export-format]")) {
    const format = encodeURIComponent(link.dataset.exportFormat);
    link.href = `/api/export?format=${format}&name=${encodeURIComponent(safeName)}`;
  }
}

function saveListName(name) {
  clearTimeout(listNameDebounce);
  listNameDebounce = setTimeout(async () => {
    try {
      await fetch("/api/list-name", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name: name.trim() || "Entries" }),
      });
    } catch {
      // Ignore
    }
  }, 500);
}

function bindListName() {
  const input = $("#list-name");
  if (!input) return;
  input.addEventListener("blur", () => saveListName(input.value));
  input.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      input.blur();
    }
  });
}

function init() {
  bindRowEvents();
  syncNameFieldHeights();
  bindTagDialog();
  bindFilter();
  bindWS();
  checkDb();
  setInterval(checkDb, 5000);
  bindIEDialog();
  bindHistoryToggle();
  bindEntriesToggle();
  bindSoundToggle();
  bindReelToggle();
  reel.dress();
  bindListName();
  // #new-name lives inside the swapped table partial, so its Enter
  // handling is delegated (element-bound listeners are lost on swap).
  document.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && e.target?.id === "new-name") addEntry();
  });
}

// Module scripts run once the document is parsed.
init();
