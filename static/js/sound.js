// Synthesised sound effects: the Reel tick and the confetti pop. The app ships
// no audio assets and still works offline. Sound only reads the Reel (through
// followReel); muting never changes how the Reel moves.
const audio = { ctx: null, noise: null };

export function soundOn() {
  return localStorage.getItem("raffle-sound") !== "off";
}

// Creates (or resumes) the audio context. Browsers only allow that from a
// user gesture, so call it from one (the Draw click, the sound toggle).
export function audioCtx() {
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

// A filtered noise burst: one flapper strike.
export function playTick(strength) {
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

// One tick per cell that crosses the window, driven by the Reel's published
// motion, so the clicks thin out on their own as the belt slows down.
export function followReel(reel) {
  let lastCell = null;
  let lastAt = 0;
  reel.onSpeed(({ cells }) => {
    if (cells === null || lastCell === null) {
      lastCell = cells;
      return;
    }
    const now = performance.now();
    if (cells !== lastCell && now - lastAt > 12) {
      lastCell = cells;
      lastAt = now;
      playTick(1);
    }
  });
}

// Party popper: a filtered burst with a low thump under it, then glitter
// blips scattered across the fall. Same mute switch as the Reel ticks.
export function playConfettiSound() {
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

function syncSoundBtn(btn) {
  const on = soundOn();
  btn.classList.toggle("muted", !on);
  btn.title = on ? "Sound on — click to mute" : "Sound muted — click to unmute";
  btn.setAttribute("aria-pressed", on ? "true" : "false");
}

export function bindSoundToggle() {
  const btn = document.querySelector("#sound-toggle");
  if (!btn) return;
  syncSoundBtn(btn);
  btn.addEventListener("click", () => {
    const next = !soundOn();
    localStorage.setItem("raffle-sound", next ? "on" : "off");
    syncSoundBtn(btn);
    // The click is a user gesture: unlock audio and preview the tick.
    if (next) playTick(0.8);
  });
}
