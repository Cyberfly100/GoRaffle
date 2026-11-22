// Region refresh: re-renders named page regions from the server and keeps
// what the host had going in each one. Each region declares what it needs;
// nothing else is restored for it.
//
// Interface:
//   const regions = createRegions({ reel });
//   regions.define(name, {
//     url,          string, or () => string | null (null: skip this refresh)
//     target,       selector of the region's element
//     swap,         "outer" (default: the response replaces the element) or
//                   "inner" (the response becomes its content)
//     apply,        (responseText) => void, instead of swapping: the region
//                   updates itself from the response
//     keep: {       what survives the swap:
//       scroll,     selectors of scrollports whose position is kept
//       focus,      true: the focused field, its in-flight edit and cursor
//       fitHeight,  (root) => void: re-fit auto-height fields (after focus
//                   is restored, before scroll is, so offsets clamp right)
//     },
//     waitForIdle,  true: never applied while the Reel is busy; held back
//                   and refreshed when the Reel is idle
//     after,        (element) => void, once the new content is in place
//   })
//   regions.refresh(names)   refresh these regions (unknown names ignored);
//                            resolves once all are applied or skipped
//   regions.refreshAll()
//   regions.names()
//
// The Reel check for "waitForIdle" is made when the response is applied, so
// a refresh fetched just before a Reveal never lands during it.
export function createRegions({ reel }) {
  const decls = new Map();
  // Latest refresh per region: an older response never overwrites a newer.
  const seq = new Map();
  const heldForIdle = new Set();

  reel.onIdle(() => {
    if (!heldForIdle.size) return;
    const names = [...heldForIdle];
    heldForIdle.clear();
    refresh(names);
  });

  function define(name, decl) {
    decls.set(name, decl);
  }

  function held(name, decl) {
    if (!decl.waitForIdle || !reel.busy()) return false;
    heldForIdle.add(name);
    return true;
  }

  async function refreshOne(name) {
    const decl = decls.get(name);
    if (!decl || held(name, decl)) return;
    const url = typeof decl.url === "function" ? decl.url() : decl.url;
    if (!url || !document.querySelector(decl.target)) return;
    const mine = (seq.get(name) || 0) + 1;
    seq.set(name, mine);
    let text;
    try {
      const resp = await fetch(url);
      if (!resp.ok) return;
      text = await resp.text();
    } catch {
      return;
    }
    if (seq.get(name) !== mine || held(name, decl)) return;
    if (decl.apply) {
      decl.apply(text);
      return;
    }
    const target = document.querySelector(decl.target);
    if (!target) return;
    const el = swap(target, text, decl.swap, decl.keep || {});
    if (el && decl.after) decl.after(el);
  }

  function refresh(names) {
    return Promise.all([...names].map(refreshOne));
  }

  return {
    define,
    refresh,
    refreshAll: () => refresh(decls.keys()),
    names: () => [...decls.keys()],
  };
}

function swap(target, html, mode = "outer", keep) {
  const scroll = (keep.scroll || []).map((sel) => {
    const el = target.matches(sel) ? target : target.querySelector(sel);
    return { sel, top: el ? el.scrollTop : 0, left: el ? el.scrollLeft : 0 };
  });
  const focus = keep.focus ? captureFocus(target) : null;

  let el = target;
  if (mode === "inner") {
    target.innerHTML = html;
  } else {
    const wrap = document.createElement("template");
    wrap.innerHTML = html;
    el = wrap.content.firstElementChild;
    if (!el) return null;
    target.replaceWith(el);
  }

  restoreFocus(el, focus);
  if (keep.fitHeight) keep.fitHeight(el);
  for (const { sel, top, left } of scroll) {
    const s = el.matches(sel) ? el : el.querySelector(sel);
    if (!s) continue;
    s.scrollTop = Math.min(top, s.scrollHeight);
    s.scrollLeft = Math.min(left, s.scrollWidth);
  }
  return el;
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
    type: el.type,
    value: el.value,
    selStart: el.selectionStart,
    selEnd: el.selectionEnd,
  };
}

function restoreFocus(root, state) {
  if (!state) return;
  let el = null;
  if (state.id) el = root.querySelector("#" + CSS.escape(state.id));
  else if (state.rowId && state.field)
    el = root.querySelector(`[data-id="${CSS.escape(state.rowId)}"] [data-field="${CSS.escape(state.field)}"]`);
  if (!el || typeof el.focus !== "function") return;
  el.focus();
  // Only text fields carry an edit worth putting back.
  if (state.type === "checkbox" || state.type === "number") return;
  el.value = state.value;
  try {
    el.setSelectionRange(state.selStart, state.selEnd);
  } catch {}
}
