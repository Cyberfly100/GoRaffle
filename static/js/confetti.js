// Confetti for the moment of reveal. Self-contained so the app keeps working
// offline (no CDN). Bursts from the given element, cleans up its own canvas,
// and stays out of the layout.
import { playConfettiSound } from "./sound.js";

let confettiCanvas = null;

export function confettiBurst(originEl) {
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
