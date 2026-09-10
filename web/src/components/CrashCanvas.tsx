import { useEffect, useRef } from "react";
import lottie, { type AnimationItem } from "lottie-web";

import { format, secondsToReach } from "../lib/multiplier";
import type { Phase } from "../lib/types";

interface Props {
  phase: Phase;
  /** Hundredths. */
  multiplier: number;
  /** Absolute time the betting window shuts, for the countdown. */
  phaseEndsAt: number | null;
}

interface Spark {
  /** Fractions of whichever box the field is filling, so a resize -- or the
   *  field pulling back into the board -- never respawns it. */
  x: number;
  y: number;
  size: number;
  alpha: number;
  drift: number;
}

/** The reference board's palette, taken off its own stylesheet. */
const ORANGE = "255, 165, 0";
const GREEN = "0, 255, 0";
const CRASH = "255, 48, 100";

/**
 * Where the carrot ends up and how long it takes to get there. On the
 * reference board the clip climbs out of the bottom-left corner over the first
 * few seconds and then hovers in the top right for the rest of the round --
 * the number, not the curve, is what carries a long flight.
 */
const RISE_SECONDS = 3;
/**
 * How far the render loop's flight clock may sit from the multiplier in state
 * before it is reset to it, in seconds. Below this the gap is the quantising
 * and correcting it would stutter; above it something real happened -- a
 * reconnect mid-round, a tab waking up -- and the clock is wrong.
 */
const RE_ANCHOR = 0.3;
/**
 * How long the trail and the clip take to fade up out of the take-off. Keep it
 * short: the climb is the thing worth watching, and a fade that outlasts the
 * fast half of it hides the flight and leaves the clip appearing halfway up.
 */
const FADE_IN = 0.25;
const START_X = 0.109;
const START_Y = 0.9;
const HOVER_X = 0.818;
const HOVER_Y = 0.35;

/** Footprint reserved for the carrot-and-bunny clip riding the curve's tip. */
const CARROT_SIZE = 150;
/** The burst clip needs more room than the carrot: it expands well past it. */
const BURST_SIZE = 240;

/** The retro grid's band: how far above the board it starts, and how deep. */
const GRID_RISE = 112;
/** Seconds the grid takes to bring one row forward. Lower is faster. */
const GRID_PERIOD = 3.5;
/**
 * The shape of one cell. ROW_RATIO is how much closer to the horizon each row
 * sits than the one in front of it, so lowering it spaces the rows out;
 * COLUMN_SPREAD is the gap between columns at the front of the plane, as a
 * fraction of the stage width. Between them they are the cell size.
 */
const ROW_RATIO = 0.62;
const COLUMN_SPREAD = 0.3;
/** Columns drawn either side of the vanishing point. */
const COLUMNS = 4;
/**
 * The last slice of the plane's depth, as a fraction of it, over which rows
 * dissolve into the dark. Rows crowd together without bound as they approach
 * the horizon and left alone they merge into a hard bright line where the
 * plane ends -- this is only wide enough to swallow that line.
 */
const GRID_HAZE = 0.12;
/**
 * How brightly the plane is drawn: the rows, the dots on their crossings, and
 * the columns running back to the vanishing point. Each is the alpha at the
 * brightest point of its fade.
 */
const GRID_ROW_ALPHA = 0.55;
const GRID_DOT_ALPHA = 0.72;
const GRID_COLUMN_ALPHA = 0.42;

/**
 * The sparkle field. One square of box per sparkle for DENSITY, glyph sizes
 * from MIN up to MIN + SPREAD, and the fall in pixels a second between DRIFT
 * and DRIFT + DRIFT_SPREAD -- multiplied by FLYING while a round is in the air
 * and by WAITING while the next one is being set up.
 */
const SPARK_DENSITY = 13000;
const SPARK_MIN_SIZE = 8;
const SPARK_SIZE_SPREAD = 24;
const SPARK_MIN_DRIFT = 9;
const SPARK_DRIFT_SPREAD = 17;
const SPARK_FLYING = 10;
const SPARK_WAITING = 1;

/**
 * crash-anim.json runs 180 frames at 60fps (3s) top to bottom, but its first
 * half is wind-up -- only the back half, the actual detonation, plays. Adjust
 * the frame range here to taste; BURST_LEN (the number's cue to come back on
 * screen) follows it automatically.
 */
const BURST_FRAME_START = 90;
const BURST_FRAME_END = 180;
const BURST_FPS = 60;
const BURST_LEN = (BURST_FRAME_END - BURST_FRAME_START) / BURST_FPS;

/**
 * How long the number takes to pop in once it's allowed back on screen --
 * mirrors the ~0.3s ease-out transition the reference board uses whenever it
 * swaps between states, rather than snapping straight in.
 */
const POP_IN = 0.3;

/** Vertical anchor of the number and the countdown inside the board. */
const NUMBER_Y = 0.452;

interface Box {
  x: number;
  y: number;
  w: number;
  h: number;
}

/**
 * The board and the space around it: starfield and perspective grid over the
 * whole stage, curve inside the board, with the carrot-and-bunny and crash
 * clips as Lottie layers on top (both pulled from the reference board's own
 * assets). Keeping the number on the canvas means the 60fps animation never
 * touches the React tree.
 *
 * The canvas deliberately reaches past the board: on the reference the grid and
 * the sparkles run the full width of the screen and carry on behind the chips
 * and the table, and only the curve is boxed in.
 */
export function CrashCanvas({ phase, multiplier, phaseEndsAt }: Props) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const boardRef = useRef<HTMLDivElement>(null);
  const carrotRef = useRef<HTMLDivElement>(null);
  const burstRef = useRef<HTMLDivElement>(null);

  // The render loop reads the latest values through a ref, so it is set up once
  // and never torn down as props change.
  const latest = useRef({ phase, multiplier, phaseEndsAt });
  latest.current = { phase, multiplier, phaseEndsAt };

  useEffect(() => {
    const canvas = canvasRef.current;
    const boardEl = boardRef.current;
    const carrotEl = carrotRef.current;
    const burstEl = burstRef.current;
    if (!canvas || !boardEl || !carrotEl || !burstEl) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    let sparks: Spark[] = [];
    let width = 0;
    let height = 0;
    let board: Box = { x: 0, y: 0, w: 0, h: 0 };
    let carrotSize = CARROT_SIZE;

    // Where the carrot last sat, in page coordinates. Flying is the only phase
    // that updates it.
    const tip = { x: 0, y: 0 };
    // How far the sparkle field has fallen, in drift-seconds. Accumulating the
    // distance rather than multiplying the clock means the round can change
    // the rate without the whole field jumping to a new offset.
    let sparkTravel = 0;
    let lastFrame = performance.now();
    // A local clock for the flight. The multiplier in state is quantised to
    // whole hundredths, and around take-off one hundredth is four frames
    // apart -- driving the climb off it directly is a clip that jerks from
    // step to step however smoothly it is eased.
    let flightStart: number | null = null;
    let prevPhase: Phase | null = null;
    let burstStart: number | null = null;
    let numberSince: number | null = null;

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
      sparks = makeSparks(width, height);

      // Both the canvas and the board are laid out against the stage, so the
      // board's offsets are already the box to draw the curve into.
      board = {
        x: boardEl.offsetLeft,
        y: boardEl.offsetTop,
        w: boardEl.clientWidth,
        h: boardEl.clientHeight,
      };
      tip.x = board.x + board.w * START_X;
      tip.y = board.y + board.h * START_Y;

      carrotSize = Math.min(CARROT_SIZE, board.w * 0.44);
      const burstSize = Math.min(BURST_SIZE, board.w * 0.7);
      carrotEl.style.width = `${carrotSize}px`;
      carrotEl.style.height = `${carrotSize}px`;
      burstEl.style.width = `${burstSize}px`;
      burstEl.style.height = `${burstSize}px`;
    };

    resize();
    const observer = new ResizeObserver(resize);
    observer.observe(canvas);
    observer.observe(boardEl);

    let frame = 0;
    const start = performance.now();

    const render = () => {
      const now = performance.now();
      const elapsed = (now - start) / 1000;
      // Clamped, so coming back to a backgrounded tab does not fling the
      // sparkle field a minute down the screen in one frame.
      const step = Math.min(0.1, (now - lastFrame) / 1000);
      lastFrame = now;
      const state = latest.current;

      sparkTravel += step * (state.phase === "flying" ? SPARK_FLYING : SPARK_WAITING);

      if (state.phase === "flying") {
        // Anchor on the first frame of a flight, and after that only when the
        // local clock has really come adrift -- a reconnect into the middle of
        // a round, or a tab coming back from sleep. Anything smaller is the
        // quantising, and correcting for that every frame is the stutter.
        const reported = secondsToReach(state.multiplier);
        if (flightStart === null || Math.abs((now - flightStart) / 1000 - reported) > RE_ANCHOR) {
          flightStart = now - reported * 1000;
        }
      } else {
        flightStart = null;
      }
      const flown = flightStart === null ? 0 : (now - flightStart) / 1000;

      if (state.phase === "flying" && prevPhase !== "flying") {
        carrotAnim.play();
        // A fresh flight starts from the corner. Without this the clip would
        // be planted at last round's hover point for the first frame and read
        // as jumping back down to the start.
        tip.x = board.x + board.w * START_X;
        tip.y = board.y + board.h * START_Y;
      }
      if (state.phase !== "flying" && prevPhase === "flying") carrotAnim.stop();

      if (state.phase === "crashed" && prevPhase !== "crashed") {
        burstStart = elapsed;
        burstAnim.playSegments([BURST_FRAME_START, BURST_FRAME_END], true);
      }
      if (state.phase !== "crashed") burstStart = null;
      prevPhase = state.phase;

      const burstAge = burstStart !== null ? elapsed - burstStart : null;
      const burstPlaying = burstAge !== null && burstAge < BURST_LEN;
      burstEl.classList.toggle("crash-burst--active", burstPlaying);

      // The number gets its own quick pop the instant it's allowed back on
      // screen -- at the start of a fresh flight, or once the burst has had
      // its moment -- rather than snapping straight in.
      const showNumber = state.phase !== "betting" && !(state.phase === "crashed" && burstPlaying);
      if (showNumber && numberSince === null) numberSince = elapsed;
      if (!showNumber) numberSince = null;
      const numberAge = numberSince !== null ? elapsed - numberSince : null;

      draw(ctx, width, height, board, state, sparks, sparkTravel, flown, elapsed, tip, carrotSize, numberAge);

      // After the draw, not before it: the curve is what works out where the
      // tip is this frame, and reading it beforehand leaves the clip a frame
      // behind the trail it is supposed to be riding.
      carrotEl.style.visibility = state.phase === "flying" ? "visible" : "hidden";
      if (state.phase === "flying") {
        // The clips live inside the board, the tip is in stage coordinates.
        carrotEl.style.left = `${tip.x - board.x}px`;
        carrotEl.style.top = `${tip.y - board.y}px`;
        carrotEl.style.opacity = String(fadeIn(flown));
      }

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
      <canvas ref={canvasRef} className="stage-canvas" />
      <div ref={boardRef} className="board">
        <div ref={carrotRef} className="crash-lottie" />
        <div ref={burstRef} className="crash-burst" />
      </div>
    </>
  );
}

type Board = { phase: Phase; multiplier: number; phaseEndsAt: number | null };

function makeSparks(width: number, height: number): Spark[] {
  const count = Math.round((width * height) / SPARK_DENSITY);
  return Array.from({ length: count }, () => ({
    x: Math.random(),
    y: Math.random(),
    // Most sparkles are the small ones with the odd big one among them, so the
    // roll is biased rather than spread evenly across the range.
    size: SPARK_MIN_SIZE + Math.round(Math.random() ** 3 * SPARK_SIZE_SPREAD),
    alpha: 0.3 + Math.random() * 0.4,
    drift: SPARK_MIN_DRIFT + Math.random() * SPARK_DRIFT_SPREAD,
  }));
}

function draw(
  ctx: CanvasRenderingContext2D,
  width: number,
  height: number,
  board: Box,
  state: Board,
  sparks: Spark[],
  sparkTravel: number,
  flown: number,
  elapsed: number,
  tip: { x: number; y: number },
  carrotSize: number,
  numberAge: number | null,
) {
  ctx.clearRect(0, 0, width, height);
  ctx.fillStyle = "#000000";
  ctx.fillRect(0, 0, width, height);

  // In flight the field fills the screen and tears past. Between rounds it
  // pulls back into the board and slows to a drift, so the countdown stands in
  // clear space with the sparkles only around it.
  const stage = { x: 0, y: 0, w: width, h: height };
  const field = state.phase === "flying" ? stage : board;
  drawSparks(ctx, field, stage, sparks, sparkTravel);

  // The grid is up for as long as a round is on, burst and all; only the trail
  // goes with the carrot, which is why the reference board reads as empty space
  // again the moment the round settles.
  if (state.phase !== "betting") drawGrid(ctx, width, board, elapsed);
  if (state.phase === "flying") {
    drawCurve(ctx, board, flown, tip, carrotSize, elapsed);
  }

  if (state.phase === "betting") {
    drawCountdown(ctx, board, state.phaseEndsAt);
    return;
  }

  if (numberAge !== null) drawMultiplier(ctx, board, state, numberAge);
}

function drawSparks(
  ctx: CanvasRenderingContext2D,
  field: Box,
  stage: Box,
  sparks: Spark[],
  travel: number,
) {
  ctx.save();
  ctx.textAlign = "center";
  ctx.textBaseline = "middle";

  // The field is scattered for the whole stage, so filling a smaller box means
  // showing proportionally fewer of them -- otherwise pulling back into the
  // board would pack the same crowd into a third of the room. The order is
  // random, so a prefix is an even sample of it.
  const share = (field.w * field.h) / (stage.w * stage.h);
  const shown = Math.round(sparks.length * Math.min(1, share));

  for (let i = 0; i < shown; i++) {
    const spark = sparks[i];
    // Wrap rather than respawn, so the field never visibly thins out.
    const fallen = spark.y * field.h + spark.drift * travel;
    ctx.font = `${spark.size}px system-ui, sans-serif`;
    ctx.fillStyle = `rgba(255, 255, 255, ${spark.alpha})`;
    ctx.fillText(
      "✦",
      field.x + spark.x * field.w,
      field.y + (((fallen % field.h) + field.h) % field.h),
    );
  }

  ctx.restore();
}

/**
 * The retro grid: a plane running back to a vanishing point just inside the top
 * of the board, crawling towards the viewer. It reaches the full width of the
 * screen rather than stopping at the board, which is what makes the board read
 * as a window onto something bigger.
 */
function drawGrid(ctx: CanvasRenderingContext2D, width: number, board: Box, elapsed: number) {
  const horizon = board.y + board.h * 0.08;
  const back = board.y - GRID_RISE + 400;
  const depth = back - horizon;
  if (depth <= 0) return;

  // How far down the band the nearest row sits. Rows are spaced by a constant
  // factor rather than as 1/n: measured off the reference board, that is the
  // spacing that puts a line where it puts one, all the way from the front of
  // the plane to the haze at the horizon.
  const front = depth * 0.74;
  const vanishX = width / 2;
  const spread = width * COLUMN_SPREAD;
  const scroll = (elapsed / GRID_PERIOD) % 1;

  ctx.save();
  ctx.beginPath();
  ctx.rect(0, horizon, width, depth);
  ctx.clip();
  ctx.lineWidth = 1;

  // Columns are straight lines out of the vanishing point, so they can be drawn
  // once, full length, and left to the fade to trail off. They have to die out
  // at both ends: solid to the apex they would converge into a sunburst the
  // reference board does not have, solid to the front they would outshine the
  // rows they are supposed to sit under.
  const columns = ctx.createLinearGradient(0, horizon, 0, horizon + front);
  columns.addColorStop(0, "rgba(128, 128, 128, 0)");
  columns.addColorStop(0.5, `rgba(128, 128, 128, ${GRID_COLUMN_ALPHA})`);
  columns.addColorStop(1, "rgba(128, 128, 128, 0)");
  ctx.strokeStyle = columns;
  for (let j = -COLUMNS; j <= COLUMNS; j++) {
    ctx.beginPath();
    ctx.moveTo(vanishX, horizon);
    ctx.lineTo(vanishX + j * spread, horizon + front);
    ctx.stroke();
  }

  // Rows step forward, not back: subtracting the scroll walks each one towards
  // the viewer, which is the direction the plane is supposed to be coming
  // from. Starting at -1 picks up the row that is halfway off the near edge.
  for (let n = -1; n < 20; n++) {
    const reach = Math.pow(ROW_RATIO, n - scroll);
    if (reach > 1) continue;
    const near = front * reach;
    const y = horizon + near;
    // Dark at both ends: rows rise out of the haze at the horizon and dim
    // again as they run off the near edge, so neither end of the plane is a
    // line you can point at.
    const fade = (1 - reach) * Math.min(1, reach / GRID_HAZE);

    ctx.strokeStyle = `rgba(128, 128, 128, ${GRID_ROW_ALPHA * fade})`;
    ctx.beginPath();
    ctx.moveTo(0, y);
    ctx.lineTo(width, y);
    ctx.stroke();

    // A dot on every crossing, shrinking with the row it sits on.
    const radius = Math.max(0.5, 2.4 * reach);
    ctx.fillStyle = `rgba(255, 255, 255, ${GRID_DOT_ALPHA * fade})`;
    for (let j = -COLUMNS; j <= COLUMNS; j++) {
      const x = vanishX + j * spread * reach;
      ctx.beginPath();
      ctx.arc(x, y, radius, 0, Math.PI * 2);
      ctx.fill();
    }
  }

  ctx.restore();
}

/**
 * The trail: one quadratic sweep out of the bottom-left corner up to whatever
 * the carrot is doing, filled underneath and cut off square below the tip.
 */
function drawCurve(
  ctx: CanvasRenderingContext2D,
  board: Box,
  /** Seconds since take-off, off the render loop's own clock. */
  flown: number,
  tip: { x: number; y: number },
  carrotSize: number,
  elapsed: number,
) {
  const climb = Math.min(1, flown / RISE_SECONDS);
  // Smoothstep: the clip leaves the corner from rest and settles into the
  // hover. An ease-out spent most of the distance in the first half second,
  // which under a fade-in is a clip that simply appears near the top.
  const eased = climb * climb * (3 - 2 * climb);

  // Once it is up there the clip never sits perfectly still: the reference
  // drifts it around by a few pixels for the rest of the round. The drift has
  // to come up with the climb rather than switch on at the top -- a sine
  // starting from wherever its phase happens to be is a jump of several
  // pixels at the exact moment the clip is meant to be settling.
  const hover = climb * climb;
  const tipX =
    board.w * (START_X + (HOVER_X - START_X) * eased) + hover * Math.sin(elapsed * 1.1) * 7;
  const tipY =
    board.h * (START_Y - (START_Y - HOVER_Y) * eased) + hover * Math.sin(elapsed * 0.8) * 5;

  const originX = 0;
  const originY = board.h - 2;
  const controlX = tipX / 2;
  const controlY = board.h;

  ctx.save();
  ctx.globalAlpha = fadeIn(flown);
  ctx.translate(board.x, board.y);

  const stroke = ctx.createLinearGradient(0, originY, tipX, tipY);
  stroke.addColorStop(0, `rgba(${ORANGE}, 0.3)`);
  stroke.addColorStop(1, `rgba(${ORANGE}, 1)`);

  const fill = ctx.createLinearGradient(0, originY, tipX, tipY);
  fill.addColorStop(0, `rgba(${ORANGE}, 0.1)`);
  fill.addColorStop(1, `rgba(${ORANGE}, 0.3)`);

  const under = new Path2D();
  under.moveTo(originX, originY);
  under.quadraticCurveTo(controlX, controlY, tipX, tipY + 10);
  under.lineTo(tipX, originY);
  under.closePath();
  ctx.fillStyle = fill;
  ctx.fill(under);

  const line = new Path2D();
  line.moveTo(originX, originY);
  line.quadraticCurveTo(controlX, controlY, tipX, tipY);
  ctx.strokeStyle = stroke;
  ctx.lineWidth = 3.5;
  ctx.lineCap = "round";
  ctx.shadowColor = `rgba(${ORANGE}, 0.8)`;
  ctx.shadowBlur = 6;
  ctx.stroke(line);

  ctx.restore();

  // Kept inside the board so the clip never half-hangs off an edge.
  tip.x = board.x + clamp(tipX, carrotSize * 0.34, board.w - carrotSize * 0.34);
  tip.y = board.y + clamp(tipY, carrotSize * 0.34, board.h - carrotSize * 0.34);
}

function clamp(value: number, low: number, high: number): number {
  return Math.min(Math.max(value, low), high);
}

/**
 * How far up the trail and the carrot have come out of the take-off. The
 * reference board holds both at nothing for the first instant of a round and
 * cross-fades them in, so a fresh round opens on bare space rather than
 * snapping a curve onto the board.
 */
function fadeIn(flown: number): number {
  return Math.min(1, flown / FADE_IN);
}

/** The seconds left before take-off, alone on the board behind a soft halo. */
function drawCountdown(ctx: CanvasRenderingContext2D, board: Box, endsAt: number | null) {
  const remaining = endsAt === null ? 0 : Math.max(0, endsAt - Date.now());
  const x = board.x + board.w / 2;
  const y = board.y + board.h * NUMBER_Y;

  // Each digit swells as its second begins and settles, so take-off is felt
  // rather than read.
  const intoSecond = 1 - (remaining % 1000) / 1000;
  const size = Math.min(board.w * 0.42, board.h * 0.48) * (1 + 0.06 * (1 - intoSecond));

  ctx.save();

  const halo = ctx.createRadialGradient(x, y, 0, x, y, size * 0.8);
  halo.addColorStop(0, "rgba(255, 255, 255, 0.2)");
  halo.addColorStop(1, "rgba(255, 255, 255, 0)");
  ctx.fillStyle = halo;
  ctx.fillRect(x - size * 0.8, y - size * 0.8, size * 1.6, size * 1.6);

  ctx.textAlign = "center";
  ctx.textBaseline = "middle";
  ctx.font = fontOf(size);
  ctx.fillStyle = "#ffffff";
  ctx.shadowColor = "rgba(255, 255, 255, 0.5)";
  ctx.shadowBlur = 40;
  ctx.fillText(String(Math.max(Math.ceil(remaining / 1000), 0)), x, y);
  ctx.restore();
}

function drawMultiplier(
  ctx: CanvasRenderingContext2D,
  board: Box,
  state: Board,
  age: number,
) {
  const text = `x${format(state.multiplier)}`;
  const crashed = state.phase === "crashed";
  const tone = crashed ? CRASH : GREEN;

  // Ease-out cubic, settling from half size rather than snapping straight in:
  // the reference cross-fades the same element between a small green number
  // parked left of the carrot and a big red one in the middle of the board.
  const t = Math.min(age / POP_IN, 1);
  const eased = 1 - Math.pow(1 - t, 3);

  const maxSize = crashed ? board.w * 0.28 : board.w * 0.14;
  const maxWidth = crashed ? board.w * 0.95 : board.w * 0.5;
  const x = board.x + board.w / 2 - (crashed ? 0 : board.w * 0.233);
  const y = board.y + board.h * NUMBER_Y;

  ctx.save();
  ctx.globalAlpha = eased;
  // Fit by measuring rather than trusting a fixed size: without SF Pro the
  // stack falls back to a noticeably wider face, and a hard-coded size then
  // stretches the number right across the board.
  ctx.font = fontOf(fitFont(ctx, text, maxWidth, maxSize));
  ctx.textAlign = "center";
  ctx.textBaseline = "middle";
  ctx.fillStyle = `rgb(${tone})`;
  ctx.shadowColor = `rgb(${tone})`;
  ctx.shadowBlur = crashed ? 40 : 20;

  const scale = 0.5 + 0.5 * eased;
  ctx.translate(x, y);
  ctx.scale(scale, scale);
  ctx.fillText(text, 0, 0);
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
