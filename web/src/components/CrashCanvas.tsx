import { useEffect, useRef } from "react";
import lottie, { type AnimationItem } from "lottie-web";

import { curveAt, format, secondsToReach } from "../lib/multiplier";
import type { Phase } from "../lib/types";

interface Props {
  phase: Phase;
  /** Hundredths. */
  multiplier: number;
  /** Absolute time the betting window shuts, for the countdown. */
  phaseEndsAt: number | null;
}

interface Star {
  x: number;
  y: number;
  r: number;
  alpha: number;
  drift: number;
}

/** Where the leading edge of the curve sits across the plot. */
const TIP_AT = 0.82;

const GOLD = "240, 160, 32";
const GREEN = "0, 255, 0";
/** The burst colour on the original board is a hot pink, not a dark red. */
const CRASH = "255, 45, 85";

/** Footprint reserved for the carrot-and-bunny clip riding the curve's tip. */
const CARROT_SIZE = 96;
/** The burst clip needs more room than the carrot: it expands well past it. */
const BURST_SIZE = 220;

/**
 * The crash clip (crash-anim.json) runs a fixed 3s. It's left to play out in
 * full, and the multiplier only appears once it's done -- the server's
 * crashed-phase pause is set longer than this so there is time left to show it.
 */
const BURST_LEN = 3;

/**
 * The whole board: starfield, perspective grid and curve on a canvas, with the
 * carrot-and-bunny and crash clips as Lottie layers on top of it (both pulled
 * from the reference board's own assets). Keeping the number on the canvas
 * means the 60fps animation never touches the React tree.
 */
export function CrashCanvas({ phase, multiplier, phaseEndsAt }: Props) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const carrotRef = useRef<HTMLDivElement>(null);
  const burstRef = useRef<HTMLDivElement>(null);

  // The render loop reads the latest values through a ref, so it is set up once
  // and never torn down as props change.
  const latest = useRef({ phase, multiplier, phaseEndsAt });
  latest.current = { phase, multiplier, phaseEndsAt };

  useEffect(() => {
    const canvas = canvasRef.current;
    const carrotEl = carrotRef.current;
    const burstEl = burstRef.current;
    if (!canvas || !carrotEl || !burstEl) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    let stars: Star[] = [];
    let width = 0;
    let height = 0;
    let carrotSize = CARROT_SIZE;

    // Where the carrot last sat. Flying is the only phase that updates it.
    const tip = { x: 0, y: 0 };
    let prevPhase: Phase | null = null;
    let burstStart: number | null = null;

    const carrotAnim: AnimationItem = lottie.loadAnimation({
      container: carrotEl,
      renderer: "svg",
      loop: true,
      autoplay: false,
      path: "/lottie/bunny-anim.json",
    });
    const burstAnim: AnimationItem = lottie.loadAnimation({
      container: burstEl,
      renderer: "svg",
      loop: false,
      autoplay: false,
      path: "/lottie/crash-anim.json",
    });

    const resize = () => {
      const dpr = window.devicePixelRatio || 1;
      width = canvas.clientWidth;
      height = canvas.clientHeight;
      canvas.width = Math.round(width * dpr);
      canvas.height = Math.round(height * dpr);
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      stars = makeStars(width, height);
      tip.x = width / 2;
      tip.y = height * 0.5;

      carrotSize = Math.min(CARROT_SIZE, width * 0.26);
      const burstSize = Math.min(BURST_SIZE, width * 0.6);
      carrotEl.style.width = `${carrotSize}px`;
      carrotEl.style.height = `${carrotSize}px`;
      burstEl.style.width = `${burstSize}px`;
      burstEl.style.height = `${burstSize}px`;
      // Dead centre of the board, not the curve's tip -- the burst reads as
      // the round's outcome, not as a continuation of where it happened.
      burstEl.style.left = `${width / 2}px`;
      burstEl.style.top = `${height / 2}px`;
    };

    resize();
    const observer = new ResizeObserver(resize);
    observer.observe(canvas);

    let frame = 0;
    const start = performance.now();

    const render = () => {
      const elapsed = (performance.now() - start) / 1000;
      const board = latest.current;

      if (board.phase === "flying" && prevPhase !== "flying") carrotAnim.play();
      if (board.phase !== "flying" && prevPhase === "flying") carrotAnim.stop();
      carrotEl.style.visibility = board.phase === "flying" ? "visible" : "hidden";
      if (board.phase === "flying") {
        carrotEl.style.left = `${tip.x}px`;
        carrotEl.style.top = `${tip.y}px`;
      }

      if (board.phase === "crashed" && prevPhase !== "crashed") {
        burstStart = elapsed;
        burstAnim.goToAndPlay(0, true);
      }
      if (board.phase !== "crashed") burstStart = null;
      prevPhase = board.phase;

      const burstAge = burstStart !== null ? elapsed - burstStart : null;
      burstEl.style.visibility = burstAge !== null && burstAge < BURST_LEN ? "visible" : "hidden";

      draw(ctx, width, height, board, stars, elapsed, tip, carrotSize, burstAge);
      frame = requestAnimationFrame(render);
    };
    frame = requestAnimationFrame(render);

    return () => {
      cancelAnimationFrame(frame);
      observer.disconnect();
      carrotAnim.destroy();
      burstAnim.destroy();
    };
  }, []);

  return (
    <>
      <canvas ref={canvasRef} className="crash-canvas" />
      <div ref={carrotRef} className="crash-lottie" />
      <div ref={burstRef} className="crash-lottie" />
    </>
  );
}

type Board = { phase: Phase; multiplier: number; phaseEndsAt: number | null };

function makeStars(width: number, height: number): Star[] {
  const count = Math.round((width * height) / 2600);
  return Array.from({ length: count }, () => ({
    x: Math.random() * width,
    y: Math.random() * height,
    r: Math.random() * 1.2 + 0.3,
    alpha: Math.random() * 0.6 + 0.2,
    drift: Math.random() * 6 + 2,
  }));
}

function draw(
  ctx: CanvasRenderingContext2D,
  width: number,
  height: number,
  board: Board,
  stars: Star[],
  elapsed: number,
  tip: { x: number; y: number },
  carrotSize: number,
  burstAge: number | null,
) {
  ctx.clearRect(0, 0, width, height);
  ctx.fillStyle = "#000000";
  ctx.fillRect(0, 0, width, height);

  drawStars(ctx, height, stars, elapsed);

  // After the burst the original clears the board back to bare space and leaves
  // only the number standing, so the grid and the curve go with it.
  if (board.phase === "flying") {
    drawGrid(ctx, width, height, elapsed);
    drawCurve(ctx, width, height, board.multiplier, tip, carrotSize);
  }

  if (board.phase === "betting") {
    drawCountdown(ctx, width, height, board.phaseEndsAt);
    return;
  }

  // The burst clip gets the crashed phase to itself; the coefficient only
  // takes over once it has fully played out.
  const holdingForBurst = board.phase === "crashed" && burstAge !== null && burstAge < BURST_LEN;
  if (!holdingForBurst) drawMultiplier(ctx, width, height, board, 1);
}

function drawStars(
  ctx: CanvasRenderingContext2D,
  height: number,
  stars: Star[],
  elapsed: number,
) {
  for (const star of stars) {
    // Wrap rather than respawn, so the field never visibly thins out.
    const y = (star.y + elapsed * star.drift) % height;
    const twinkle = 0.75 + 0.25 * Math.sin(elapsed * 2 + star.x);

    ctx.beginPath();
    ctx.arc(star.x, y, star.r, 0, Math.PI * 2);
    ctx.fillStyle = `rgba(255, 255, 255, ${star.alpha * twinkle})`;
    ctx.fill();
  }
}

/** A ceiling grid running back to a vanishing point, fading as it comes down. */
function drawGrid(ctx: CanvasRenderingContext2D, width: number, height: number, elapsed: number) {
  const horizon = height * 0.46;
  const vanishX = width / 2;
  const vanishY = -height * 0.15;

  ctx.save();
  ctx.beginPath();
  ctx.rect(0, 0, width, horizon);
  ctx.clip();
  ctx.lineWidth = 1;

  for (let i = -8; i <= 8; i++) {
    const x = vanishX + (i * width) / 5;
    const fade = 0.2 * (1 - Math.abs(i) / 10);
    ctx.strokeStyle = `rgba(255, 255, 255, ${Math.max(fade, 0.03)})`;
    ctx.beginPath();
    ctx.moveTo(vanishX, vanishY);
    ctx.lineTo(x, horizon);
    ctx.stroke();
  }

  // Rows crawl forward so the grid reads as motion rather than wallpaper.
  const scroll = (elapsed * 0.7) % 1;
  for (let row = 0; row < 14; row++) {
    const t = (row + scroll) / 14;
    const y = vanishY + (horizon - vanishY) * t * t;
    if (y < 0 || y > horizon) continue;
    ctx.strokeStyle = `rgba(255, 255, 255, ${0.2 * t})`;
    ctx.beginPath();
    ctx.moveTo(0, y);
    ctx.lineTo(width, y);
    ctx.stroke();
  }

  ctx.restore();
}

function drawCurve(
  ctx: CanvasRenderingContext2D,
  width: number,
  height: number,
  multiplier: number,
  tip: { x: number; y: number },
  carrotSize: number,
) {
  const padBottom = height * 0.1;
  const plotW = width * 0.92;
  const plotH = height - padBottom - height * 0.14;

  const flown = secondsToReach(multiplier);
  // The tip holds a fixed fraction of the width and the time window scrolls
  // under it, so the curve fills the frame from the first second.
  const spanSeconds = Math.max(flown / TIP_AT, 2.2);
  const spanTop = Math.max(multiplier, 180);

  // Sample the unrounded curve: multiplierAt quantises to whole hundredths, so
  // consecutive samples land on the same pixel row and the line comes out as a
  // staircase.
  const pointAt = (t: number): [number, number] => [
    (t / spanSeconds) * plotW,
    height - padBottom - ((curveAt(t) - 100) / (spanTop - 100)) * plotH,
  ];

  const samples = 160;
  const path = new Path2D();
  path.moveTo(...pointAt(0));
  for (let i = 1; i <= samples; i++) {
    path.lineTo(...pointAt((flown * i) / samples));
  }

  const [tipX, tipY] = pointAt(flown);

  const filled = new Path2D(path);
  filled.lineTo(tipX, height - padBottom);
  filled.lineTo(0, height - padBottom);
  filled.closePath();

  const gradient = ctx.createLinearGradient(0, tipY, 0, height - padBottom);
  gradient.addColorStop(0, `rgba(${GOLD}, 0.34)`);
  gradient.addColorStop(1, `rgba(${GOLD}, 0)`);
  ctx.fillStyle = gradient;
  ctx.fill(filled);

  ctx.strokeStyle = `rgb(${GOLD})`;
  ctx.lineWidth = 3;
  ctx.lineJoin = "round";
  ctx.lineCap = "round";
  ctx.shadowColor = `rgba(${GOLD}, 0.7)`;
  ctx.shadowBlur = 12;
  ctx.stroke(path);
  ctx.shadowBlur = 0;

  // Kept inside the board so the clip never half-hangs off an edge.
  tip.x = clamp(tipX, carrotSize * 0.5, width - carrotSize * 0.5);
  tip.y = clamp(tipY, carrotSize * 0.5, height - carrotSize * 0.5);
}

function clamp(value: number, low: number, high: number): number {
  return Math.min(Math.max(value, low), high);
}

/** Big numbers counting the betting window down. */
function drawCountdown(
  ctx: CanvasRenderingContext2D,
  width: number,
  height: number,
  endsAt: number | null,
) {
  const remaining = endsAt === null ? 0 : Math.max(0, endsAt - Date.now());

  ctx.save();
  ctx.textAlign = "center";
  ctx.textBaseline = "middle";

  ctx.font = fontOf(Math.min(width * 0.055, 22));
  ctx.fillStyle = "rgba(255, 255, 255, 0.5)";
  ctx.fillText("Ожидание раунда", width / 2, height * 0.24);

  // Each digit swells as its second begins and settles, so take-off is felt
  // rather than read.
  const intoSecond = 1 - (remaining % 1000) / 1000;
  const size = Math.min(width * 0.42, height * 0.46) * (1 + 0.06 * (1 - intoSecond));

  ctx.font = fontOf(size);
  ctx.fillStyle = "rgba(255, 255, 255, 0.92)";
  ctx.shadowColor = "rgba(255, 255, 255, 0.35)";
  ctx.shadowBlur = 24;
  ctx.fillText(String(Math.max(Math.ceil(remaining / 1000), 0)), width / 2, height * 0.55);
  ctx.restore();
}

function drawMultiplier(
  ctx: CanvasRenderingContext2D,
  width: number,
  height: number,
  board: Board,
  alpha: number,
) {
  if (alpha <= 0) return;

  const text = `x${format(board.multiplier)}`;
  const tone = board.phase === "crashed" ? CRASH : GREEN;
  // While it's flying the carrot owns the right side of the board, so the
  // number sits small in the clear space on the left rather than spanning the
  // centre on top of it. Once crashed there's nothing to share the board
  // with, so it gets the big centred reveal back.
  const compact = board.phase === "flying";

  ctx.save();
  ctx.globalAlpha = alpha;
  // Fit by measuring rather than trusting a fixed size: without SF Pro the
  // stack falls back to a noticeably wider face, and a hard-coded 96px then
  // stretches the number right across the board.
  const maxWidth = compact ? width * 0.4 : width * 0.52;
  const maxSize = compact
    ? Math.min(width * 0.15, height * 0.18)
    : Math.min(width * 0.24, height * 0.3);
  ctx.font = fontOf(fitFont(ctx, text, maxWidth, maxSize));
  ctx.textAlign = compact ? "left" : "center";
  ctx.textBaseline = "middle";
  ctx.fillStyle = `rgb(${tone})`;
  ctx.shadowColor = `rgb(${tone})`;
  ctx.shadowBlur = board.phase === "crashed" ? 34 : 20;
  ctx.fillText(text, compact ? width * 0.07 : width / 2, height * 0.38);
  ctx.restore();
}

function fontOf(size: number): string {
  return `600 ${Math.round(size)}px -apple-system, "SF Pro Display", Inter, system-ui, sans-serif`;
}

/** Largest size at or below maxSize that keeps text inside maxWidth. */
function fitFont(
  ctx: CanvasRenderingContext2D,
  text: string,
  maxWidth: number,
  maxSize: number,
): number {
  ctx.font = fontOf(maxSize);
  const measured = ctx.measureText(text).width;
  return measured <= maxWidth ? maxSize : (maxSize * maxWidth) / measured;
}
