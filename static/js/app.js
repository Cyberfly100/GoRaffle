/* Raffle UI logic: theme, mutations, filters, tags, suspense, WS-driven refresh. */
(() => {
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

  /* ---------- Partial refresh ---------- */
  // Fetch-based refresh: independent of any CDN so partials always re-render.
  async function swapPartial(url, selector) {
    // Never tear down the reel mid-spin; the winner reveal re-renders it.
    if (
      selector === "#result" &&
      (reelState.phase === "spinup" ||
        reelState.phase === "cruising" ||
        reelState.phase === "landing" ||
        reelState.phase === "reduced")
    )
      return;
    const target = document.querySelector(selector);
    if (!target) return;
    let resp;
    try {
      resp = await fetch(url);
    } catch {
      return;
    }
    if (!resp.ok) return;
    const html = await resp.text();
    const wrap = document.createElement("template");
    wrap.innerHTML = html;
    const node = wrap.content.firstElementChild;
    if (!node) return;
    target.replaceWith(node);
    if (selector === "#filter-bar") renderFilterZones();
    syncHistoryBtnLabel();
    updateEligibility();
  }

  function refreshPartials() {
    swapPartial("/partials/table", "#entry-table");
    swapPartial("/partials/filter", "#filter-bar");
    swapPartial("/partials/history", "#history");
    swapPartial("/partials/result", "#result");
  }

  function refreshIfNoWS() {
    if (!window.Raffle.wsConnected()) refreshPartials();
  }

  /* ---------- Draw ---------- */
  function selectedTags(zone) {
    return filterState[zone] || [];
  }

  async function doDraw() {
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
      return null;
    }
    const data = await resp.json().catch(() => ({}));
    if (!resp.ok) {
      toast(data.error || "Draw failed.");
      return null;
    }
    window.__pendingWinner = data;
    // When the socket is live, suspense + winner arrive via WS.
    if (!window.Raffle.wsConnected()) fallbackRenderWinner(data);
    return data;
  }

  function fallbackRenderWinner(data) {
    refreshPartials();
    if (data?.winner?.name) {
      toast(`The winner is ${data.winner.name}`);
      // No socket means no reel, but this is still the moment of reveal.
      confettiBurst($("#result .reel") || $("#result"));
    }
  }

  /* ---------- Suspense reel ---------- */
  // Horizontal slot-machine reel. The server sends name frames over WS;
  // the client spins them, then lands on the winner when its event arrives.
  const reelState = { phase: "idle", timers: [], seq: [], cellW: 0, tickRAF: null };
  // Belt speed is a constant px/ms so the reel reads the same regardless of
  // how wide the names make the cells, and so the landing can hand over
  // without a jump in velocity. Phase lengths are tuned against the server's
  // suspenseTotal (draw.go): wind-up + cruise fill it, then the landing runs.
  const REEL_CRUISE_V = 1.45;
  const REEL_LAND_MS = 1800;
  const REEL_SPINUP_MS = 900;

  function reelLater(fn, ms) {
    reelState.timers.push(setTimeout(fn, ms));
  }

  function clearReelTimers() {
    for (const t of reelState.timers) clearTimeout(t);
    reelState.timers = [];
  }

  function measureCellW(names, viewportW) {
    const probe = document.createElement("span");
    probe.style.cssText =
      "position:absolute;visibility:hidden;white-space:nowrap;font-size:1.6rem;font-weight:600;";
    document.body.appendChild(probe);
    let w = 0;
    for (const n of names) {
      probe.textContent = n;
      w = Math.max(w, probe.offsetWidth);
    }
    probe.remove();
    const cap = Math.max(viewportW * 0.6, 128);
    return Math.min(Math.max(w + 32, 128), cap);
  }

  // Deterministic pass: no name repeats within `gap` cells, so the visible
  // window never shows the same contender twice. Built from the server's
  // frame order, so every browser spins identically.
  function spreadSequence(frames, gap) {
    const seq = [];
    for (const n of frames) {
      if (!seq.slice(-gap).includes(n)) seq.push(n);
    }
    if (seq.length === 0 && frames.length > 0) seq.push(frames[0]);
    // The sequence is looped, so its ends meet: trim a tail that would sit
    // next to the head and render as a duplicate at every copy seam.
    while (seq.length > 1 && seq[seq.length - 1] === seq[0]) seq.pop();
    return seq;
  }

  function reelCell(name, cls) {
    const d = document.createElement("div");
    d.className = "reel-cell" + (cls ? " " + cls : "");
    d.textContent = name;
    return d;
  }

  function animateSuspense(names) {
    if (!names || names.length === 0) return;
    const res = $("#result");
    const reel = res && res.querySelector(".reel");
    const track = reel && reel.querySelector(".reel-track");
    const label = res && res.querySelector(".result-label");
    const sub = res && res.querySelector(".result-sub");
    if (!reel || !track) return;
    clearReelTimers();
    if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
      if (label) label.textContent = "Drawing…";
      track.setAttribute("aria-hidden", "true");
      track.innerHTML = "";
      track.appendChild(reelCell("…", "is-idle"));
      reelState.phase = "reduced";
      reelLater(() => {
        if (reelState.phase === "reduced") {
          reelState.phase = "idle";
          refreshPartials();
        }
      }, 6000);
      return;
    }
    const viewportW = reel.clientWidth || 300;
    const cellW = measureCellW(names, viewportW);
    const visibleCells = Math.max(2, Math.ceil(viewportW / cellW) + 1);
    const seq = spreadSequence(names, visibleCells);
    const loopW = seq.length * cellW;
    const spinDist = (REEL_CRUISE_V * REEL_SPINUP_MS) / 2;

    // Keep the name that is already on screen and queue the new ones to its
    // right, so the strip grows out of the current state rather than
    // replacing it with a full row of names on both sides.
    const shown = track.querySelector(".reel-cell");
    const leadName = shown ? shown.textContent : "";
    const leadCls = shown && shown.classList.contains("is-winner") ? "is-winner" : "is-idle";
    // Rotating the cycle keeps the queue from opening with the very name the
    // lead cell is already showing. Adjacency within the cycle is unchanged.
    if (seq.length > 1 && seq[0] === leadName) seq.push(seq.shift());
    reelState.seq = seq;
    reelState.cellW = cellW;

    reel.style.setProperty("--cell-w", cellW + "px");
    if (label) label.textContent = "Drawing…";
    if (sub) sub.textContent = "";
    track.setAttribute("aria-hidden", "true");
    track.classList.remove("cruising", "landing", "spinup");
    track.style.transform = "";
    track.style.transitionDuration = "";
    track.style.animationDelay = "";
    track.innerHTML = "";

    const lead = document.createElement("div");
    lead.className = "reel-lead";
    track.appendChild(lead);
    const leadCell = reelCell(leadName, "is-prefix " + leadCls);
    track.appendChild(leadCell);
    // Centre the existing name where it already sits; the queue starts after it.
    lead.style.width = Math.max(0, (viewportW - leadCell.offsetWidth) / 2) + "px";
    const base = lead.offsetWidth + leadCell.offsetWidth;
    reelState.base = base;

    // The strip travels exactly one sequence per cycle, so it must be at
    // least one sequence WIDER than the viewport — otherwise the tail of
    // each cycle scrolls past the right edge and leaves a hole. The wind-up
    // does NOT wrap, so it has to be covered outright when it runs further
    // than a single loop (short pools).
    const copies = Math.max(
      2,
      Math.ceil((Math.max(loopW, spinDist - base) + viewportW) / loopW)
    );
    for (let c = 0; c < copies; c++) {
      for (const n of seq) track.appendChild(reelCell(n, "fade-in"));
    }
    // Pin the strip to flex-start in the SAME frame it is built. The resting
    // rule centres the track, which would splash the over-wide strip across
    // both sides of the slot for one frame before the wind-up begins.
    track.classList.add("spinup");
    reel.classList.add("is-live");
    // Wind up from rest, then hand over to the constant-speed loop at exactly
    // cruise speed — accelerating over t covers v*t/2.
    const cruiseMs = loopW / REEL_CRUISE_V;
    requestAnimationFrame(() => {
      // The loop runs entirely past the lead cell, where the strip repeats.
      track.style.setProperty("--loop-from", -base + "px");
      track.style.setProperty("--loop-to", -(base + loopW) + "px");
      track.style.setProperty("--cruise-dur", (cruiseMs / 1000).toFixed(3) + "s");
      track.classList.add("spinup");
      track.style.transitionDuration = REEL_SPINUP_MS + "ms";
      void track.offsetWidth;
      track.style.transform = `translateX(${-spinDist}px)`;
      const windupStart = performance.now();
      reelState.phase = "spinup";
      startReelTicks(track, cellW);
      // Hand over when the wind-up truly ends. A bare timer can fire while the
      // transition still has a frame to run, which snaps the belt forward.
      const toCruise = (e) => {
        if (e && e.propertyName !== "transform") return;
        track.removeEventListener("transitionend", toCruise);
        if (reelState.phase !== "spinup") return;
        // Enter the loop at the phase matching where the wind-up ended, via a
        // negative delay, so neither position nor speed jumps. transitionend
        // lands a frame after the transition actually stopped, so advance the
        // phase by that dead time or the belt loses a frame of travel.
        const rel = spinDist >= base ? (spinDist - base) % loopW : 0;
        const relMs = (rel / loopW) * cruiseMs;
        const lost = Math.max(0, performance.now() - (windupStart + REEL_SPINUP_MS));
        track.style.transitionDuration = "0ms";
        track.style.transform = "";
        track.classList.remove("spinup");
        track.style.animationDelay = -(relMs + lost).toFixed(1) + "ms";
        track.classList.add("cruising");
        reelState.phase = "cruising";
      };
      track.addEventListener("transitionend", toCruise);
      reelLater(() => toCruise(), REEL_SPINUP_MS + 150);
    });
    // Safety: if the winner event never arrives, give up and re-render.
    reelLater(() => {
      if (reelState.phase === "cruising" || reelState.phase === "spinup") {
        reelState.phase = "idle";
        refreshPartials();
      }
    }, 6000);
  }

  function landReel(winnerName) {
    const res = $("#result");
    const reel = res && res.querySelector(".reel");
    const track = reel && reel.querySelector(".reel-track");
    if (
      !track ||
      (reelState.phase !== "cruising" &&
        reelState.phase !== "spinup" &&
        reelState.phase !== "reduced")
    ) {
      refreshPartials();
      return;
    }
    clearReelTimers();
    if (reelState.phase === "reduced" || !winnerName) {
      reelState.phase = "idle";
      refreshPartials();
      return;
    }
    const seq = reelState.seq || [];
    if (seq.length === 0) {
      reelState.phase = "idle";
      refreshPartials();
      return;
    }
    const cellW =
      reelState.cellW || parseFloat(getComputedStyle(reel).getPropertyValue("--cell-w")) || 160;
    const viewportW = reel.clientWidth || 300;
    const centerOffset = (viewportW - cellW) / 2;
    const readTraveled = () => {
      const t = getComputedStyle(track).transform;
      return t && t !== "none" ? Math.max(0, -new DOMMatrixReadOnly(t).m41) : 0;
    };

    // Freeze exactly where the belt is *before* touching the DOM, so it can't
    // drift underneath us while the runway is built. Landing already pins
    // justify-content, so measurements below stay valid.
    const traveled = readTraveled();
    track.style.transitionDuration = "0ms";
    track.classList.remove("cruising", "spinup");
    track.classList.add("landing");
    track.style.transform = `translateX(${-traveled}px)`;

    // Hand over from the belt's real speed (it may still be winding up), so
    // the reveal never jumps in velocity.
    const fresh = reelState.v && performance.now() - reelState.v.at < 120;
    const v0 = Math.max(0.15, Math.min(REEL_CRUISE_V, fresh ? reelState.v.value : REEL_CRUISE_V));
    // Coasting to a stop from speed v covers v*t/2. Put the winner exactly
    // that far ahead, so the belt decelerates the whole way instead of
    // sprinting to catch a winner parked at the end of the strip.
    const idealDist = (v0 * REEL_LAND_MS) / 2;
    // Sequence cells sit on a grid starting after the lead cell: grid j is at
    // base + j*cellW. Everything past the right edge is invisible and can be
    // rebuilt (never drop the lead spacer or the lead cell).
    const base = reelState.base || 0;
    const originLeft = track.offsetLeft;
    while (
      track.children.length > 2 &&
      track.lastElementChild.offsetLeft - originLeft >= traveled + viewportW
    ) {
      track.lastElementChild.remove();
    }
    const existing = Math.max(0, track.children.length - 2);
    let grid = Math.max(
      existing,
      Math.round((traveled + centerOffset + idealDist - base) / cellW)
    );
    // Never let the runway's last name duplicate the winner.
    if (seq.length > 1 && seq[((grid - 1) % seq.length + seq.length) % seq.length] === winnerName) {
      grid += 1;
    }
    for (let i = existing; i < grid; i++) {
      track.appendChild(reelCell(seq[i % seq.length]));
    }
    track.appendChild(reelCell(winnerName, "is-winner"));
    const winnerCell = track.lastElementChild;
    const target = winnerCell.offsetLeft - originLeft - centerOffset;
    const dist = Math.max(cellW, target - traveled);
    // The easing starts at 2x its average slope, so this duration makes the
    // landing begin at exactly the belt's current speed and ease to a stop.
    const dur = Math.round(Math.min(2600, Math.max(700, (2 * dist) / v0)));

    void track.offsetWidth;
    track.style.transitionDuration = dur + "ms";
    track.style.transform = `translateX(${-target}px)`;
    reelState.phase = "landing";
    // Pop only the cell we landed on; the re-rendered winner must not re-pop.
    reelLater(() => {
      winnerCell.classList.add("pop");
      confettiBurst(winnerCell);
    }, dur);
    reelLater(() => {
      reelState.phase = "idle";
      refreshPartials();
    }, dur + 420);
  }

  /* ---------- Confetti ---------- */
  // Self-contained so the app keeps working offline (no CDN). Bursts from the
  // winning cell, cleans up its own canvas, and stays out of the layout.
  let confettiCanvas = null;

  function confettiBurst(originEl) {
    // Audio isn't a motion preference, so it fires either way.
    playConfettiSound();
    if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;
    if (!originEl || typeof originEl.getBoundingClientRect !== "function") return;
    if (confettiCanvas) confettiCanvas.remove();
    const w = window.innerWidth;
    const h = window.innerHeight;
    const dpr = Math.min(window.devicePixelRatio || 1, 2);
    const canvas = document.createElement("canvas");
    canvas.className = "confetti-canvas";
    canvas.width = Math.round(w * dpr);
    canvas.height = Math.round(h * dpr);
    canvas.style.width = w + "px";
    canvas.style.height = h + "px";
    const ctx = canvas.getContext("2d");
    if (!ctx) return;
    document.body.appendChild(canvas);
    confettiCanvas = canvas;
    ctx.scale(dpr, dpr);

    const r = originEl.getBoundingClientRect();
    const ox = r.left + r.width / 2;
    const oy = r.top + r.height / 2;
    const colors = ["#e8b923", "#4ade80", "#d6a354", "#7dd3fc", "#f472b6", "#a78bfa"];
    const parts = [];
    for (let i = 0; i < 90; i++) {
      // Fan upward and outward from the winner.
      const angle = -Math.PI / 2 + (Math.random() - 0.5) * Math.PI * 1.15;
      const speed = 4 + Math.random() * 7;
      parts.push({
        x: ox,
        y: oy,
        vx: Math.cos(angle) * speed * (0.7 + Math.random() * 0.9),
        vy: Math.sin(angle) * speed,
        w: 5 + Math.random() * 5,
        h: 8 + Math.random() * 6,
        rot: Math.random() * Math.PI,
        vr: (Math.random() - 0.5) * 0.32,
        color: colors[i % colors.length],
      });
    }

    const maxLife = 1900;
    let start = null;
    function frame(ts) {
      if (canvas !== confettiCanvas) return; // superseded by a newer burst
      if (start === null) start = ts;
      const elapsed = ts - start;
      ctx.clearRect(0, 0, w, h);
      let onScreen = 0;
      for (const p of parts) {
        p.vy += 0.22;
        p.vx *= 0.995;
        p.vy *= 0.995;
        p.x += p.vx;
        p.y += p.vy;
        p.rot += p.vr;
        if (p.y < h + 40) onScreen++;
        ctx.save();
        ctx.globalAlpha = Math.max(0, 1 - elapsed / maxLife);
        ctx.translate(p.x, p.y);
        ctx.rotate(p.rot);
        ctx.fillStyle = p.color;
        ctx.fillRect(-p.w / 2, -p.h / 2, p.w, p.h);
        ctx.restore();
      }
      if (elapsed < maxLife && onScreen > 0) {
        requestAnimationFrame(frame);
      } else {
        canvas.remove();
        if (confettiCanvas === canvas) confettiCanvas = null;
      }
    }
    requestAnimationFrame(frame);
  }

  /* ---------- Wheel tick sound ---------- */
  // Synthesised, not sampled: a filtered noise burst per cell boundary, so the
  // app ships no audio assets and still works offline.
  const audio = { ctx: null, noise: null };

  function soundOn() {
    return localStorage.getItem("raffle-sound") !== "off";
  }

  function audioCtx() {
    if (!audio.ctx) {
      const AC = window.AudioContext || window.webkitAudioContext;
      if (!AC) return null;
      try {
        audio.ctx = new AC();
      } catch {
        return null;
      }
      const len = Math.floor(audio.ctx.sampleRate * 0.05);
      const buf = audio.ctx.createBuffer(1, len, audio.ctx.sampleRate);
      const data = buf.getChannelData(0);
      for (let i = 0; i < len; i++) data[i] = Math.random() * 2 - 1;
      audio.noise = buf;
    }
    // Browsers start the context suspended until a user gesture.
    if (audio.ctx.state === "suspended") audio.ctx.resume().catch(() => {});
    return audio.ctx;
  }

  function playTick(strength) {
    if (!soundOn()) return;
    const ctx = audioCtx();
    if (!ctx || ctx.state !== "running" || !audio.noise) return;
    const t = ctx.currentTime;
    const src = ctx.createBufferSource();
    src.buffer = audio.noise;
    // Vary each strike slightly; a real flapper never hits twice the same.
    src.playbackRate.value = 0.9 + Math.random() * 0.3;
    const bp = ctx.createBiquadFilter();
    bp.type = "bandpass";
    bp.frequency.value = 1800 + Math.random() * 900;
    bp.Q.value = 1.1;
    const gain = ctx.createGain();
    const peak = 0.16 * (strength || 1);
    gain.gain.setValueAtTime(0.0001, t);
    gain.gain.exponentialRampToValueAtTime(peak, t + 0.002);
    gain.gain.exponentialRampToValueAtTime(0.0001, t + 0.035);
    src.connect(bp).connect(gain).connect(ctx.destination);
    src.start(t);
    src.stop(t + 0.05);
  }

  // One tick per cell boundary crossing. Driven by the reel's real position,
  // so the clicks thin out on their own as the belt decelerates.
  function startReelTicks(track, cellW) {
    if (reelState.tickRAF) cancelAnimationFrame(reelState.tickRAF);
    let lastCell = null;
    let lastAt = 0;
    let prevPos = null;
    let prevT = 0;
    const step = () => {
      if (
        reelState.phase !== "spinup" &&
        reelState.phase !== "cruising" &&
        reelState.phase !== "landing"
      ) {
        reelState.tickRAF = null;
        return;
      }
      const t = getComputedStyle(track).transform;
      const pos = t && t !== "none" ? Math.max(0, -new DOMMatrixReadOnly(t).m41) : 0;
      const now = performance.now();
      // Track live speed so the landing can hand over from whatever the belt
      // is actually doing, not from an assumed cruise speed.
      if (prevPos !== null) {
        const dx = pos - prevPos;
        const dt = now - prevT;
        if (dt > 0 && dx >= 0) reelState.v = { value: dx / dt, at: now };
      }
      prevPos = pos;
      prevT = now;
      const cell = Math.floor(pos / cellW);
      if (lastCell === null) {
        lastCell = cell;
      } else if (cell !== lastCell && now - lastAt > 12) {
        lastCell = cell;
        lastAt = now;
        playTick(1);
      }
      reelState.tickRAF = requestAnimationFrame(step);
    };
    reelState.tickRAF = requestAnimationFrame(step);
  }

  // Party popper: a filtered burst with a low thump under it, then glitter
  // blips scattered across the fall. Same mute switch as the wheel ticks.
  function playConfettiSound() {
    if (!soundOn()) return;
    const ctx = audioCtx();
    if (!ctx || ctx.state !== "running" || !audio.noise) return;
    const t0 = ctx.currentTime;

    const pop = ctx.createBufferSource();
    pop.buffer = audio.noise;
    pop.loop = true; // the buffer is short; the envelope shapes the tail
    pop.playbackRate.value = 0.7;
    const popFilter = ctx.createBiquadFilter();
    popFilter.type = "lowpass";
    popFilter.frequency.setValueAtTime(4200, t0);
    popFilter.frequency.exponentialRampToValueAtTime(600, t0 + 0.25);
    const popGain = ctx.createGain();
    popGain.gain.setValueAtTime(0.0001, t0);
    popGain.gain.exponentialRampToValueAtTime(0.25, t0 + 0.008);
    popGain.gain.exponentialRampToValueAtTime(0.0001, t0 + 0.3);
    pop.connect(popFilter).connect(popGain).connect(ctx.destination);
    pop.start(t0);
    pop.stop(t0 + 0.34);

    const thump = ctx.createOscillator();
    thump.type = "sine";
    thump.frequency.setValueAtTime(260, t0);
    thump.frequency.exponentialRampToValueAtTime(70, t0 + 0.18);
    const thumpGain = ctx.createGain();
    thumpGain.gain.setValueAtTime(0.0001, t0);
    thumpGain.gain.exponentialRampToValueAtTime(0.18, t0 + 0.01);
    thumpGain.gain.exponentialRampToValueAtTime(0.0001, t0 + 0.25);
    thump.connect(thumpGain).connect(ctx.destination);
    thump.start(t0);
    thump.stop(t0 + 0.3);

    for (let i = 0; i < 14; i++) {
      const at = t0 + 0.05 + Math.random() * 0.9;
      const osc = ctx.createOscillator();
      osc.type = "triangle";
      const f = 1400 + Math.random() * 2200;
      osc.frequency.setValueAtTime(f, at);
      osc.frequency.exponentialRampToValueAtTime(f * 1.5, at + 0.05);
      const g = ctx.createGain();
      g.gain.setValueAtTime(0.0001, at);
      g.gain.exponentialRampToValueAtTime(0.04 + Math.random() * 0.04, at + 0.005);
      g.gain.exponentialRampToValueAtTime(0.0001, at + 0.09);
      osc.connect(g).connect(ctx.destination);
      osc.start(at);
      osc.stop(at + 0.12);
    }
  }

  function syncSoundBtn() {
    const btn = $("#sound-toggle");
    if (!btn) return;
    const on = soundOn();
    btn.classList.toggle("muted", !on);
    btn.title = on ? "Sound on — click to mute" : "Sound muted — click to unmute";
    btn.setAttribute("aria-pressed", on ? "true" : "false");
  }

  function bindSoundToggle() {
    const btn = $("#sound-toggle");
    if (!btn) return;
    syncSoundBtn();
    btn.addEventListener("click", () => {
      const next = !soundOn();
      localStorage.setItem("raffle-sound", next ? "on" : "off");
      syncSoundBtn();
      // The click is a user gesture: unlock audio and preview the tick.
      if (next) playTick(0.8);
    });
  }

  /* ---------- Row mutations ---------- */
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
    const resp = await fetch("/api/import", { method: "POST", body: form });
    const data = await resp.json().catch(() => ({}));
    if (!resp.ok) {
      toast(data.error || "Import failed.");
      return;
    }
    toast(`Imported: ${data.added} added, ${data.updated} updated.`);
    const input = $("#import-file");
    if (input) input.value = "";
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
    window.Raffle.wsOn("suspense", (data) => animateSuspense(data && data.names));
    window.Raffle.wsOn("winner", (data) => {
      landReel(data && data.winner && data.winner.name);
    });
    window.Raffle.wsOn("undo", () => refreshPartials());
    window.Raffle.wsOn("entries", () => {
      refreshPartials();
    });
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

  /* ---------- Init ---------- */
  function init() {
    bindRowEvents();
    bindTagDialog();
    bindFilter();
    bindWS();
    checkDb();
    setInterval(checkDb, 5000);
    bindIEDialog();
    bindHistoryToggle();
    bindSoundToggle();
    // #new-name lives inside the swapped table partial, so its Enter
    // handling is delegated (element-bound listeners are lost on swap).
    document.addEventListener("keydown", (e) => {
      if (e.key === "Enter" && e.target?.id === "new-name") addEntry();
    });
  }

  window.addEventListener("DOMContentLoaded", init);
})();