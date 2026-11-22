// Pure landing planner for the Reel (no DOM). Given the measured geometry of a
// running belt, it decides where the winner goes and how the belt slows to a
// stop on it. Tested with Node's test runner (js-tests/reel-plan.test.js).
//
// The strip is laid out along one axis: a head (lead spacer, carried rows),
// then grid cells of uniform `cellSize` starting at `base` px. Grid cell j
// starts at base + j * cellSize. `travelled` is how far the strip has moved
// back (the belt's current offset), so the window shows
// [travelled, travelled + windowSize].

// Axis descriptors: which CSS/DOM properties carry the belt's one dimension.
// The planner takes one for symmetry with the renderer but never reads it:
// horizontal and vertical are the same plan on a different axis.
export const HORIZONTAL = Object.freeze({
  name: "horizontal",
  translate: "translateX",
  matrix: "m41",
  offset: "offsetLeft",
  size: "offsetWidth",
  cssSize: "width",
  client: "clientWidth",
  cellVar: "--cell-w",
});

export const VERTICAL = Object.freeze({
  name: "vertical",
  translate: "translateY",
  matrix: "m42",
  offset: "offsetTop",
  size: "offsetHeight",
  cssSize: "height",
  client: "clientHeight",
  cellVar: "--cell-h",
});

// planLanding returns the landing plan:
//   keep     - how many already-rendered grid cells stay (the rest, all past
//              the window's far edge, are dropped);
//   runway   - names to add after them, before the winner;
//   tail     - names to add after the winner, so the belt never visibly ends;
//   distance - px the belt travels from `travelled` to the stop;
//   duration - ms the landing lasts. The landing easing starts at twice its
//              average slope, so distance = speed * duration / 2: the belt
//              leaves its current speed without a jump and coasts to a stop.
export function planLanding({ cellSize, windowSize, travelled, speed, landMs, base, strip, cycle, winner }) {
  const len = cycle.length;
  const centreOffset = (windowSize - cellSize) / 2;
  const nameAt = (i) => (i < keep ? strip[i] : cycle[i % len]);

  // Cells that start before the far edge may be on screen: keep them.
  const keep = Math.min(strip.length, Math.max(0, Math.ceil((travelled + windowSize - base) / cellSize)));
  // Coasting to a stop from `speed` over `landMs` covers speed*landMs/2.
  const ideal = (speed * landMs) / 2;
  let index = Math.max(keep, Math.round((travelled + centreOffset + ideal - base) / cellSize));
  // The belt must not seem to stop one cell early on a copy of the name.
  // (The cycle never repeats a name back to back, so this ends within a cycle.)
  for (let n = 0; len > 1 && n <= len && index > 0 && nameAt(index - 1) === winner; n++) index += 1;

  const runway = [];
  for (let i = keep; i < index; i++) runway.push(cycle[i % len]);

  let resume = (index + 1) % len;
  if (len > 1 && cycle[resume] === winner) resume = (resume + 1) % len;
  const tailCount = Math.ceil((windowSize - centreOffset) / cellSize) + 1;
  const tail = [];
  for (let i = 0; i < tailCount; i++) tail.push(cycle[(resume + i) % len]);

  const distance = base + index * cellSize - centreOffset - travelled;
  const duration = (2 * distance) / speed;
  return { keep, runway, tail, distance, duration };
}
