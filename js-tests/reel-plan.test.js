// Pure tests for the Reel's landing planner. Run: node --test 'js-tests/*.test.js'
import { test } from "node:test";
import assert from "node:assert/strict";
import { planLanding, beltSpeed, HORIZONTAL, VERTICAL } from "../static/js/reel-plan.js";

const CYCLE = ["Ada", "Grace", "Linus", "Margaret", "Ken"];

// A belt after some cruising: `rendered` grid cells already drawn from the
// cycle, starting `base` px into the strip.
function geometry(over = {}) {
  const g = {
    axis: HORIZONTAL,
    cellSize: 160,
    windowSize: 448,
    travelled: 2400,
    speed: 4.35,
    landMs: 1800,
    base: 220,
    cycle: CYCLE,
    winner: "Linus",
    ...over,
  };
  if (!g.strip) {
    const rendered = over.rendered ?? 40;
    g.strip = Array.from({ length: rendered }, (_, i) => g.cycle[i % g.cycle.length]);
  }
  delete g.rendered;
  return g;
}

// The strip as it reads after the plan is applied.
function landedStrip(g, plan) {
  return [...g.strip.slice(0, plan.keep), ...plan.runway, g.winner, ...plan.tail];
}

// Which grid cell holds the centre of the window once the belt has stopped,
// and how far that cell's own centre sits from the window's centre.
function centred(g, plan) {
  const stop = g.travelled + plan.distance;
  const centre = stop + g.windowSize / 2 - g.base;
  const index = Math.floor(centre / g.cellSize);
  const miss = Math.abs(index * g.cellSize + g.cellSize / 2 - centre);
  return { index, miss };
}

function* combinations() {
  for (const cellSize of [48, 128, 160, 268.8])
    for (const windowSize of [144, 300, 448, 900])
      for (const travelled of [0, 37, 1957, 2400.5, 9000])
        for (const speed of [0.15, 1.2, 4.35])
          for (const winner of ["Ada", "Linus", "Ken"])
            yield { cellSize, windowSize, travelled, speed, winner };
}

test("the winner is centred when the belt stops", () => {
  for (const c of combinations()) {
    const g = geometry({ ...c, rendered: Math.ceil((c.travelled + 3 * c.windowSize) / c.cellSize) + 5 });
    const plan = planLanding(g);
    const strip = landedStrip(g, plan);
    const { index, miss } = centred(g, plan);
    assert.ok(miss < 1e-6, `off-centre by ${miss}px for ${JSON.stringify(c)}`);
    assert.equal(strip[index], g.winner, JSON.stringify(c));
    assert.equal(index, plan.keep + plan.runway.length, `centred cell is not the placed winner for ${JSON.stringify(c)}`);
  }
});

test("an earlier copy of the winner's name is never where the belt stops, nor right beside it", () => {
  for (const c of combinations()) {
    for (const strip of [
      // A strip that is the winner's name all the way through.
      Array.from({ length: 80 }, () => c.winner),
      // A strip with the winner's name every other cell.
      Array.from({ length: 80 }, (_, i) => (i % 2 ? c.winner : "Grace")),
    ]) {
      const g = geometry({ ...c, strip });
      const plan = planLanding(g);
      const landed = landedStrip(g, plan);
      const { index } = centred(g, plan);
      assert.equal(index, plan.keep + plan.runway.length, `stopped on an earlier copy for ${JSON.stringify(c)}`);
      assert.notEqual(landed[index - 1], c.winner, `copy just before the winner for ${JSON.stringify(c)}`);
      assert.notEqual(landed[index + 1], c.winner, `copy just after the winner for ${JSON.stringify(c)}`);
    }
  }
});

test("the landing coasts down from the current speed: distance = speed x duration / 2, close to the planned duration", () => {
  for (const c of combinations()) {
    const g = geometry({ ...c, rendered: Math.ceil((c.travelled + 3 * c.windowSize) / c.cellSize) + 5 });
    const plan = planLanding(g);
    assert.ok(plan.distance > 0, JSON.stringify(c));
    // The easing starts at twice its average slope: that slope must be the
    // belt's current speed, or the belt visibly jumps.
    const startSpeed = (2 * plan.distance) / plan.duration;
    assert.ok(Math.abs(startSpeed - c.speed) < 1e-9, `starts at ${startSpeed} not ${c.speed} for ${JSON.stringify(c)}`);
    // Coasting from the belt's speed over landMs covers speed * landMs / 2;
    // the winner sits on the nearest grid cell to that (at most one more to
    // dodge a copy).
    const ideal = (c.speed * g.landMs) / 2;
    if (ideal >= c.windowSize + c.cellSize) {
      assert.ok(Math.abs(plan.distance - ideal) <= 1.5 * c.cellSize, `distance ${plan.distance} vs ${ideal} for ${JSON.stringify(c)}`);
    }
  }
});

test("horizontal and vertical give the same plan for the same geometry", () => {
  for (const c of combinations()) {
    const g = geometry({ ...c, rendered: 30 });
    assert.deepEqual(planLanding({ ...g, axis: VERTICAL }), planLanding({ ...g, axis: HORIZONTAL }));
  }
});

// Reel speed: 16 names pass the window per second in either orientation,
// but the belt never moves faster than 4.35 px/ms (wide names stay readable).
test("a vertical Reel (48 px cells) cruises at 16 names per second", () => {
  assert.ok(Math.abs(beltSpeed(48) - 0.768) < 1e-9);
});

test("a horizontal Reel with short names (128 px cells) cruises at 16 names per second", () => {
  assert.ok(Math.abs(beltSpeed(128) - 2.048) < 1e-9);
});

test("a horizontal Reel with very long names is capped at 4.35 px/ms", () => {
  assert.equal(beltSpeed(400), 4.35);
});
