// Page wiring: theme, mutations, filters, tags, partial refresh, and the
// glue between the websocket, the Draw button and the Reel.
import * as ws from "./ws.js";
import { createReel } from "./reel.js";
import { createRegions } from "./regions.js";
import { bindCollapsible } from "./collapsible.js";
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

/* ---------- Regions ---------- */
// Each region of the page that refreshes on its own, with what it keeps.
// Names match the server's Region constants (handler/helpers.go): a
// "changed" event names the stale ones.
const regions = createRegions({ reel });

// The Tag filter lives in this browser only. The table and the filter bar are
// rendered for it, so every refresh of them carries it.
const filterState = { must_have: [], any_of: [] };

function filterQuery() {
  const q = new URLSearchParams();
  for (const zone of ["must_have", "any_of"]) {
    for (const tag of filterState[zone]) q.append(zone, tag);
  }
  const s = q.toString();
  return s ? "?" + s : "";
}

regions.define("table", {
  // The server marks each row as in the Eligible pool and in the Pot.
  url: () => "/partials/table" + filterQuery(),
  target: "#entry-table",
  // Its Pick counts would show the winner before the Reel lands.
  waitForIdle: true,
  // Replacing the section rebuilds its scrollports and drops a focused
  // field, so the table neither jumps nor steals the caret mid-edit.
  keep: { scroll: [".rows-scroll", ".table"], focus: true, fitHeight: syncNameFieldHeights },
});

regions.define("filter", {
  // The server renders the selected Tags' chips.
  url: () => "/partials/filter" + filterQuery(),
  target: "#filter-bar",
});

regions.define("history", {
  url: "/partials/history",
  target: "#history",
  // Would show the winner before the Reel lands.
  waitForIdle: true,
});

regions.define("result", {
  url: "/partials/result",
  target: "#result",
  // Never tear down the Reel mid-Reveal.
  waitForIdle: true,
  after: () => {
    syncDrawBtn();
    reel.dress();
  },
});

regions.define("tag-popover", {
  // Only while the Tag dialog is open for an Entry.
  url: () => (tagEntry ? `/partials/tag-popover/${tagEntry}` : null),
  target: "#tag-dialog-content",
  swap: "inner",
});

regions.define("list-name", {
  url: "/api/list-name",
  target: "#list-name",
  apply: (text) => {
    let name;
    try {
      name = JSON.parse(text).name;
    } catch {
      return;
    }
    if (typeof name !== "string") return;
    updateExportLinks(name);
    // Never overwrite the host's own edit in progress.
    const input = $("#list-name");
    if (input && document.activeElement !== input) input.value = name;
  },
});

function refreshIfNoWS() {
  if (!ws.connected()) regions.refreshAll();
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
  regions.refreshAll();
});

/* ---------- Reel orientation ---------- */
function syncReelToggle() {
  const btn = $("#reel-toggle");
  if (!btn) return;
  const vertical = reel.orientation() === "vertical";
  btn.title = vertical ? "Reel: vertical — click for horizontal" : "Reel: horizontal — click for vertical";
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
    // for the new one); re-render it so the resting Reel matches.
    regions.refresh(["result"]);
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
      if (nameEl) nameEl.textContent = file ? file.name : "No file chosen";
      // Choosing a file is the whole gesture: the warning opens right away,
      // and its Import button is the only way the upload happens.
      if (file) openImportConfirm(file);
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
  const dialog = $("#tag-dialog");
  const content = $("#tag-dialog-content");
  if (!dialog || !content) return;
  tagEntry = id;
  content.replaceChildren();
  await regions.refresh(["tag-popover"]);
  if (!content.firstElementChild) {
    tagEntry = null;
    toast("Could not load tags.");
    return;
  }
  dialog.showModal();
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
  // A closed dialog has no Entry, so its region stops refreshing.
  $("#tag-dialog")?.addEventListener("close", () => {
    tagEntry = null;
  });
  document.addEventListener("submit", (e) => {
    if (e.target.id !== "tag-new-form") return;
    e.preventDefault();
    const input = e.target.querySelector('input[name="name"]');
    addTagToEntry(input.value.trim(), tagEntry);
  });
}

/* ---------- Filter ---------- */
// Changing the Tag filter re-renders the filter bar and the table for it.
let filterPickerZone = "must_have";

function filterChanged() {
  const total = filterState.must_have.length + filterState.any_of.length;
  const badge = $("#filter-count");
  if (badge) badge.textContent = total > 0 ? String(total) : "";
  regions.refresh(["filter", "table"]);
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
  $("#filter-picker")?.close();
  filterChanged();
}

function removeFilterTag(zone, tag) {
  filterState[zone] = filterState[zone].filter((t) => t !== tag);
  filterChanged();
}

function clearFilter() {
  filterState.must_have = [];
  filterState.any_of = [];
  filterChanged();
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
  if (!confirm("Reset: clear all Picks and set every Pick count to zero?")) return;
  try {
    await api("POST", "/api/reset");
    refreshIfNoWS();
  } catch (err) {
    toast(err.message);
  }
}

/* ---------- Import / Export ---------- */
// The Import confirmation: choosing a file opens the warning, and the upload
// only happens on its Import button.
let importPending = null; // the File awaiting confirmation

function openImportConfirm(file) {
  importPending = file;
  const dialog = $("#import-confirm");
  if (!dialog) return;
  const nameEl = $("#import-confirm-file");
  if (nameEl) nameEl.textContent = file.name;
  dialog.showModal();
}

function closeImportConfirm() {
  $("#import-confirm")?.close();
}

// A closed warning leaves no half-stated Import behind: the chosen file is
// forgotten, however the dialog was closed (Cancel, backdrop, Escape).
function resetImportForm() {
  importPending = null;
  const input = $("#import-file");
  if (input) input.value = "";
  const nameEl = $("#file-chosen");
  if (nameEl) nameEl.textContent = "No file chosen";
}

async function doImport(opts = {}) {
  const file = importPending;
  if (!file) {
    toast("Choose a List file first.");
    return;
  }
  if (file.size > 10 * 1024 * 1024) {
    toast("File is too large.");
    closeImportConfirm();
    return;
  }
  const form = new FormData();
  form.append("file", file);
  if (opts.removeUnusedTags) form.append("remove_unused_tags", "1");
  if (opts.skipMissingPicks) form.append("skip_missing_picks", "1");
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
    closeImportConfirm();
    return;
  }
  const entries = `${data.entries} ${data.entries === 1 ? "entry" : "entries"}`;
  const picks = `${data.picks} ${data.picks === 1 ? "pick" : "picks"}`;
  toast(`Imported: ${entries}, ${picks}.`);
  if (data.list_name) {
    const input = $("#list-name");
    if (input) {
      input.value = data.list_name;
      updateExportLinks(data.list_name);
    }
  }
  closeImportConfirm();
  refreshIfNoWS();
}

function bindImportConfirm() {
  const dialog = $("#import-confirm");
  if (!dialog) return;
  document.addEventListener("click", (e) => {
    if (e.target.closest("#import-confirm-btn")) {
      doImport();
      return;
    }
    if (e.target.closest("#import-cancel-btn") || e.target === dialog) {
      closeImportConfirm(); // backdrop click cancels
    }
  });
  dialog.addEventListener("close", resetImportForm);
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

/* ---------- WS events ---------- */
function bindWS() {
  ws.on("reveal", playReveal);
  ws.on("changed", (data) => regions.refresh(data?.regions || regions.names()));
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
  bindImportConfirm();
  bindCollapsible("history");
  bindCollapsible("entries");
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
