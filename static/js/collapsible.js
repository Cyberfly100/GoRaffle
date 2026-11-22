// Collapsible: show/hide of a page section with a remembered preference.
// History and Entries both use it.
//
// Interface:
//   bindCollapsible(name)   wire every [data-collapsible-toggle="<name>"]
//                           button (now and after any region refresh) to
//                           flip the section and remember the choice
//
// The state lives on <html> as the class "no-<name>" (collapsed), so it is
// outside every refreshed region and survives every swap. The preference is
// localStorage "raffle-<name>-visible" ("1" = shown; anything else, hidden).
// The tiny inline script in layout.html applies it before the first paint
// using the same convention; the button labels follow the class in CSS.
const root = document.documentElement;

export const storageKey = (name) => `raffle-${name}-visible`;

function setVisible(name, visible) {
  root.classList.toggle(`no-${name}`, !visible);
  try {
    localStorage.setItem(storageKey(name), visible ? "1" : "0");
  } catch {}
}

export function bindCollapsible(name) {
  // The toggles live in refreshed regions, so bind by delegation.
  document.addEventListener("click", (e) => {
    if (!e.target.closest(`[data-collapsible-toggle="${name}"]`)) return;
    setVisible(name, root.classList.contains(`no-${name}`));
  });
}
