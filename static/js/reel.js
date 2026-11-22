// The Reel: the slot-machine strip that names scroll across during the
// Suspense and that stops on the winner. This module is the only place that
// knows whether a Reveal is in progress, and the only code that touches the
// Reel's DOM, classes and CSS variables.
//
// Interface:
//   const reel = createReel({ orientation: "horizontal" | "vertical" });
//   reel.reveal(names, winnerName)
//                            play a Reveal: a Suspense over `names`, then the
//                            landing on the winner (queued behind a running
//                            Reveal). The Reel alone decides how long it takes.
//   reel.busy()              true from a Suspense until the Reel is idle again
//   reel.onSpeed(fn)         fn({ speed, cells }) on every frame the belt moves
//                            (speed in px/ms; cells = cells passed, for ticks),
//                            and fn({ speed: 0, cells: null }) when it stops
//   reel.onIdle(fn)          fn() when the Reel returns to idle with nothing
//                            queued (refresh the page regions, enable Draw)
//   reel.setOrientation(o)   switch layout; refused (false) while busy
//   reel.orientation()
//   reel.dress()             re-dress the resting Reel after #result was
//                            re-rendered (the vertical Reel's neighbour rows)
//
// Phases: idle -> windup -> cruising -> landing -> idle, or idle -> reduced
// -> idle with reduced motion. A Reveal that arrives while busy is queued and
// played after the current landing finishes.
//
// A Reveal's length is defined once, on the server (raffle.Timing in
// internal/raffle/timing.go), and rendered into the page as the
// #reveal-timing JSON; the server's replay window is derived from the same
// values. The Reel reads them here and keeps no copy of its own.
import { planLanding, cruiseSpeed, HORIZONTAL, VERTICAL } from "./reel-plan.js";
import { confettiBurst } from "./confetti.js";

// The belt cruises at the Reel speed (names per second, see cruiseSpeed),
// constant for the whole cruise so the landing can hand over without a jump
// in speed. Wind-up + cruise fill the Suspense, then the landing runs.
const WINDUP_MS = 900;
const timing = readTiming();
// How long the belt runs (wind-up included) before it starts landing.
export const SUSPENSE_MS = timing.suspense_ms;
// The nominal landing, and the bound on the measured one.
export const LANDING_MS = timing.landing_ms;
const LANDING_MAX_MS = timing.landing_max_ms;
// The landed winner stays on the belt this long before the Reel is idle.
const SETTLE_MS = timing.settle_ms;
const MIN_LANDING_SPEED = 0.15;

function readTiming() {
  const el = document.getElementById("reveal-timing");
  if (!el) throw new Error("reel: the page carries no #reveal-timing");
  return JSON.parse(el.textContent);
}

const reducedMotion = () => window.matchMedia("(prefers-reduced-motion: reduce)").matches;

export function createReel({ orientation = "horizontal" } = {}) {
  let phase = "idle";
  let current = null; // { names, winner } being revealed
  const queue = []; // pending { names, winner }
  let timers = [];
  let raf = null;
  let speed = { value: 0, at: 0 };
  // Geometry of the running belt.
  let belt = null; // { seq, cellSize, base, headCount }
  // The names flanking the last landed winner, kept for the resting Reel.
  let restNeighbours = null;
  let lastCycle = null;
  let listNames = null;
  const speedListeners = [];
  const idleListeners = [];

  document.body.classList.toggle("reel-vertical", orientation === "vertical");

  const axis = () => (orientation === "vertical" ? VERTICAL : HORIZONTAL);
  const parts = () => {
    const res = document.querySelector("#result");
    const reel = res && res.querySelector(".reel");
    return {
      reel,
      track: reel && reel.querySelector(".reel-track"),
      label: res && res.querySelector(".result-label"),
      sub: res && res.querySelector(".result-sub"),
    };
  };

  function later(fn, ms) {
    timers.push(setTimeout(fn, ms));
  }
  function clearTimers() {
    for (const t of timers) clearTimeout(t);
    timers = [];
  }

  function busy() {
    return phase !== "idle";
  }

  /* ---------- Suspense ---------- */
  function reveal(names, winner) {
    if (!winner) return;
    const item = { names: names && names.length ? names : [winner], winner };
    if (busy()) {
      queue.push(item);
      return;
    }
    play(item);
  }

  function play(item) {
    current = item;
    const { reel, track, label, sub } = parts();
    if (!reel || !track) {
      // No Reel to run: reveal the winner without motion.
      phase = "reduced";
      later(landCurrent, 0);
      return;
    }
    clearTimers();
    if (label) label.textContent = "Drawing…";
    if (sub) sub.textContent = "";
    if (reducedMotion()) {
      track.setAttribute("aria-hidden", "true");
      track.innerHTML = "";
      track.appendChild(cell("…", "is-idle"));
      phase = "reduced";
    } else {
      startSpin(reel, track, item.names);
    }
    later(landCurrent, phase === "reduced" ? 0 : SUSPENSE_MS);
  }

  function startSpin(reel, track, names) {
    const ax = axis();
    const viewport = reel[ax.client] || 300;
    const cellSize = measureCell(names, viewport, orientation === "vertical");
    const visibleCells = Math.max(2, Math.ceil(viewport / cellSize) + 1);
    const seq = spreadSequence(names, visibleCells);
    const loopLen = seq.length * cellSize;
    const cruise = cruiseSpeed(cellSize);
    const windupDist = (cruise * WINDUP_MS) / 2;

    // Keep the name that is already on screen (the resting winner, not a
    // dressed Reel row) plus the rows flanking it, so the Suspense's first frame
    // is identical to the resting one and the belt grows out of it.
    const shown = track.querySelector(".reel-cell.is-winner") || track.querySelector(".reel-cell");
    const leadName = shown ? shown.textContent : "";
    const leadCls = shown && shown.classList.contains("is-winner") ? "is-winner" : "is-idle";
    const carryBefore = [];
    const carryAfter = [];
    if (shown) {
      let pe = shown.previousElementSibling;
      while (pe && pe.classList.contains("reel-cell") && carryBefore.length < 3) {
        carryBefore.unshift(pe.textContent);
        pe = pe.previousElementSibling;
      }
      let ne = shown.nextElementSibling;
      while (ne && ne.classList.contains("reel-cell") && carryAfter.length < 3) {
        carryAfter.push(ne.textContent);
        ne = ne.nextElementSibling;
      }
    }
    // Rotating the cycle keeps the queue from opening with a name that
    // already sits at the seam (the lead, or the last carried row).
    const lastCarried = carryAfter.length ? carryAfter[carryAfter.length - 1] : null;
    for (let g = 0; seq.length > 1 && g < seq.length; g++) {
      if (seq[0] !== leadName && seq[0] !== lastCarried) break;
      seq.push(seq.shift());
    }

    reel.style.setProperty(ax.cellVar, cellSize + "px");
    track.setAttribute("aria-hidden", "true");
    track.classList.remove("cruising", "landing", "spinup");
    track.style.transform = "";
    track.style.transitionDuration = "";
    track.style.animationDelay = "";
    track.innerHTML = "";

    const lead = document.createElement("div");
    lead.className = "reel-lead";
    track.appendChild(lead);
    const leadCell = cell(leadName, "is-prefix " + leadCls);
    const beforeCells = carryBefore.map((n) => cell(n));
    const afterCells = carryAfter.map((n) => cell(n));
    track.append(...beforeCells, leadCell, ...afterCells);
    // Pin the strip to flex-start in the SAME frame it is built. The resting
    // rule centres the track, which would splash the over-wide strip across
    // both sides of the slot for one frame before the wind-up begins.
    track.classList.add("spinup");
    // Only as many carried rows stay ahead of the lead as fit between the
    // window's near edge and it: the outer ones were clipped at rest.
    let beforeSize = 0;
    for (const c of beforeCells) beforeSize += c[ax.size];
    const halfWindow = (viewport - leadCell[ax.size]) / 2;
    while (beforeCells.length && beforeSize > halfWindow) {
      beforeSize -= beforeCells[0][ax.size];
      beforeCells.shift().remove();
    }
    // Centre the existing name where it already sits.
    lead.style[ax.cssSize] = Math.max(0, halfWindow - beforeSize) + "px";
    // The head (spacer, carried rows, lead cell) is never trimmed; the grid
    // of sequence cells starts right after it.
    const headCount = 2 + beforeCells.length + afterCells.length;
    const headEnd = track.children[headCount - 1].getBoundingClientRect()[ax.rectEnd];
    const base = headEnd - track.getBoundingClientRect()[ax.rectStart];
    belt = { seq, cellSize, base, headCount, cruise };
    lastCycle = seq;

    // The strip travels one sequence per cycle, so it must be at least one
    // sequence longer than the window, and cover the wind-up outright when
    // that runs further than a single loop (short Pots).
    const copies = Math.max(2, Math.ceil((Math.max(loopLen, windupDist - base) + viewport) / loopLen));
    for (let c = 0; c < copies; c++) {
      for (const n of seq) track.appendChild(cell(n, "fade-in"));
    }
    reel.classList.add("is-live");
    phase = "windup";
    // Wind up from rest, then hand over to the constant-speed loop at exactly
    // cruise speed: accelerating over t covers v*t/2.
    const cruiseMs = loopLen / cruise;
    requestAnimationFrame(() => {
      if (phase !== "windup") return;
      track.style.setProperty("--loop-from", -base + "px");
      track.style.setProperty("--loop-to", -(base + loopLen) + "px");
      track.style.setProperty("--cruise-dur", (cruiseMs / 1000).toFixed(3) + "s");
      track.style.transitionDuration = WINDUP_MS + "ms";
      void track.offsetWidth;
      track.style.transform = `${ax.translate}(${-windupDist}px)`;
      const windupStart = performance.now();
      followMotion(track, cellSize);
      // Hand over when the wind-up truly ends. A bare timer can fire while
      // the transition still has a frame to run, which snaps the belt.
      const toCruise = (e) => {
        if (e && e.propertyName !== "transform") return;
        track.removeEventListener("transitionend", toCruise);
        if (phase !== "windup") return;
        // Enter the loop at the phase matching where the wind-up ended, via
        // a negative delay, so neither position nor speed jumps. Advance by
        // the frame transitionend arrives late, or the belt loses travel.
        const rel = windupDist >= base ? (windupDist - base) % loopLen : 0;
        const relMs = (rel / loopLen) * cruiseMs;
        const lost = Math.max(0, performance.now() - (windupStart + WINDUP_MS));
        track.style.transitionDuration = "0ms";
        track.style.transform = "";
        track.classList.remove("spinup");
        track.style.animationDelay = -(relMs + lost).toFixed(1) + "ms";
        track.classList.add("cruising");
        phase = "cruising";
      };
      track.addEventListener("transitionend", toCruise);
      later(() => toCruise(), WINDUP_MS + 150);
    });
  }

  /* ---------- Landing ---------- */
  function landCurrent() {
    clearTimers();
    const winner = current.winner;
    const { reel, track } = parts();
    if (phase === "reduced" || !belt || !reel || !track) {
      showResting(winner);
      finish();
      return;
    }
    const ax = axis();
    const travelled = readTravelled(track, ax);
    // Freeze exactly where the belt is before touching the DOM, so it can't
    // drift underneath while the runway is built.
    track.style.transitionDuration = "0ms";
    track.classList.remove("cruising", "spinup");
    track.classList.add("landing");
    track.style.transform = `${ax.translate}(${-travelled}px)`;

    // Hand over from the belt's real speed (it may still be winding up).
    const fresh = performance.now() - speed.at < 120;
    const { seq, cellSize, base, headCount, cruise } = belt;
    const v0 = Math.max(MIN_LANDING_SPEED, Math.min(cruise, fresh ? speed.value : cruise));
    const strip = [...track.children].slice(headCount).map((c) => c.textContent);
    const plan = planLanding({
      axis: ax,
      cellSize,
      windowSize: reel[ax.client] || 300,
      travelled,
      speed: v0,
      landMs: LANDING_MS,
      base,
      strip,
      cycle: seq,
      winner,
    });
    while (track.children.length > headCount + plan.keep) track.lastElementChild.remove();
    for (const n of plan.runway) track.appendChild(cell(n));
    const winnerCell = cell(winner, "is-winner");
    track.appendChild(winnerCell);
    for (const n of plan.tail) track.appendChild(cell(n));
    restNeighbours = neighbours(winnerCell);

    // Bounded, so every Reveal ends within the server's replay window.
    const dur = Math.round(Math.min(plan.duration, LANDING_MAX_MS));
    void track.offsetWidth;
    track.style.transitionDuration = dur + "ms";
    track.style.transform = `${ax.translate}(${-(travelled + plan.distance)}px)`;
    phase = "landing";
    // Pop only the cell we landed on; the re-rendered winner must not re-pop.
    later(() => {
      winnerCell.classList.add("pop");
      confettiBurst(winnerCell);
    }, dur);
    later(finish, dur + SETTLE_MS);
  }

  // The current Reveal is over: play the next queued one, or go idle.
  function finish() {
    clearTimers();
    phase = "idle";
    current = null;
    belt = null;
    if (queue.length) {
      play(queue.shift());
      return;
    }
    emitIdle();
  }

  function emitIdle() {
    for (const fn of idleListeners) fn();
  }

  /* ---------- Resting Reel ---------- */
  // The winner on a still Reel, with no motion.
  function showResting(name) {
    const { reel, track, label } = parts();
    if (!reel || !track) return;
    track.classList.remove("cruising", "landing", "spinup");
    track.style.transform = "";
    track.style.transitionDuration = "";
    track.style.animationDelay = "";
    track.removeAttribute("aria-hidden");
    track.replaceChildren(cell(name, "is-winner"));
    reel.classList.remove("is-live");
    if (label) label.textContent = "The winner is";
    restNeighbours = null;
    dressResting();
    confettiBurst(track.firstElementChild);
  }

  // The vertical window is tall enough to show rows around the resting
  // winner; a lone cell would end the Reel. Dimmed neighbours are added
  // symmetrically, from the landing, the last cycle, or the List.
  function dress() {
    if (!busy()) dressResting();
  }

  async function dressResting() {
    if (orientation !== "vertical") return;
    const { track } = parts();
    if (!track || track.children.length !== 1) return;
    const winner = track.firstElementChild;
    if (!winner.classList.contains("is-winner")) return;
    let above = [];
    let below = [];
    if (restNeighbours) {
      above = restNeighbours.before.filter((n) => n !== winner.textContent);
      below = restNeighbours.after.filter((n) => n !== winner.textContent);
    }
    if (!above.length || !below.length) {
      let names = lastCycle && lastCycle.length ? lastCycle : listNames;
      if (!names) {
        try {
          const resp = await fetch("/api/entries");
          names = (await resp.json()).map((e) => e.name);
          listNames = names;
        } catch {
          return;
        }
        // The page may have moved on while we fetched.
        if (!track.isConnected || orientation !== "vertical") return;
        if (track.children.length !== 1) return;
      }
      const pool = names.filter((n) => n !== winner.textContent);
      if (pool.length === 0) return;
      above = [0, 1, 2].map((i) => pool[i % pool.length]);
      below = [0, 1, 2].map((i) => pool[(i + 1) % pool.length]);
    }
    const dim = (name) => {
      const c = cell(name);
      c.setAttribute("aria-hidden", "true");
      return c;
    };
    winner.replaceWith(...above.map(dim), winner, ...below.map(dim));
  }

  /* ---------- Motion ---------- */
  // Samples the belt's real position every frame while it moves, so the
  // landing hands over from the true speed, and publishes it.
  function followMotion(track, cellSize) {
    if (raf) cancelAnimationFrame(raf);
    const ax = axis();
    let prevPos = null;
    let prevT = 0;
    const step = () => {
      if (phase !== "windup" && phase !== "cruising" && phase !== "landing") {
        raf = null;
        speed = { value: 0, at: performance.now() };
        publish({ speed: 0, cells: null });
        return;
      }
      const pos = readTravelled(track, ax);
      const now = performance.now();
      if (prevPos !== null) {
        const dx = pos - prevPos;
        const dt = now - prevT;
        if (dt > 0 && dx >= 0) speed = { value: dx / dt, at: now };
      }
      prevPos = pos;
      prevT = now;
      publish({ speed: speed.value, cells: Math.floor(pos / cellSize) });
      raf = requestAnimationFrame(step);
    };
    raf = requestAnimationFrame(step);
  }

  function publish(s) {
    for (const fn of speedListeners) fn(s);
  }

  return {
    reveal,
    busy,
    onSpeed: (fn) => speedListeners.push(fn),
    onIdle: (fn) => idleListeners.push(fn),
    setOrientation(next) {
      if (busy()) return false;
      orientation = next;
      document.body.classList.toggle("reel-vertical", next === "vertical");
      restNeighbours = null;
      return true;
    },
    orientation: () => orientation,
    dress,
  };
}

/* ---------- Helpers ---------- */
function cell(name, cls) {
  const d = document.createElement("div");
  d.className = "reel-cell" + (cls ? " " + cls : "");
  d.textContent = name;
  return d;
}

function neighbours(winnerCell) {
  const nb = { before: [], after: [] };
  let pe = winnerCell.previousElementSibling;
  while (pe && nb.before.length < 3) {
    if (pe.classList.contains("reel-cell")) nb.before.unshift(pe.textContent);
    pe = pe.previousElementSibling;
  }
  let ne = winnerCell.nextElementSibling;
  while (ne && nb.after.length < 3) {
    if (ne.classList.contains("reel-cell")) nb.after.push(ne.textContent);
    ne = ne.nextElementSibling;
  }
  return nb;
}

function readTravelled(track, ax) {
  const t = getComputedStyle(track).transform;
  return t && t !== "none" ? Math.max(0, -new DOMMatrixReadOnly(t)[ax.matrix]) : 0;
}

function measureCell(names, viewport, vertical) {
  const probe = document.createElement("span");
  probe.style.cssText =
    "position:absolute;visibility:hidden;white-space:nowrap;font-size:1.6rem;font-weight:600;" +
    (vertical ? "line-height:3rem;" : "");
  document.body.appendChild(probe);
  if (vertical) {
    // Vertical cells all share one height (the cell line-height).
    const h = Math.max(probe.offsetHeight || 0, 48);
    probe.remove();
    return h;
  }
  let w = 0;
  for (const n of names) {
    probe.textContent = n;
    w = Math.max(w, probe.offsetWidth);
  }
  probe.remove();
  const cap = Math.max(viewport * 0.6, 128);
  return Math.min(Math.max(w + 32, 128), cap);
}

// Deterministic pass: no name repeats within `gap` cells, so the window never
// shows the same contender twice. Built from the server's name order, so every
// browser runs the Reel identically.
function spreadSequence(names, gap) {
  const seq = [];
  for (const n of names) {
    if (!seq.slice(-gap).includes(n)) seq.push(n);
  }
  if (seq.length === 0 && names.length > 0) seq.push(names[0]);
  // The sequence is looped, so its ends meet: trim a tail that would sit next
  // to the head and render as a duplicate at every copy seam.
  while (seq.length > 1 && seq[seq.length - 1] === seq[0]) seq.pop();
  return seq;
}
