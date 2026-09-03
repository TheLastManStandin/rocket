// The client redraws at screen rate while the server only ticks ten times a
// second, so it reproduces the server's curve locally and lets each tick
// correct the drift. The server stays the sole authority on where a round
// bursts -- this only fills in the frames between its updates.

/** Must match growthLambda in internal/game/multiplier.go. */
export const GROWTH_LAMBDA = Math.LN2 / 5;

/**
 * The curve as a continuous value, in hundredths, with no rounding.
 *
 * Drawing must use this rather than multiplierAt: quantising to whole
 * hundredths makes consecutive samples land on the same pixel row and the line
 * comes out as a staircase.
 */
export function curveAt(seconds: number): number {
  if (seconds <= 0) return 100;
  return Math.exp(GROWTH_LAMBDA * seconds) * 100;
}

/** Multiplier in hundredths after `seconds` of flight, as the server counts it. */
export function multiplierAt(seconds: number): number {
  if (seconds <= 0) return 100;
  return Math.floor(Math.exp(GROWTH_LAMBDA * seconds) * 100);
}

/** Seconds of flight the curve needs to reach `hundredths`. */
export function secondsToReach(hundredths: number): number {
  if (hundredths <= 100) return 0;
  return Math.log(hundredths / 100) / GROWTH_LAMBDA;
}

export function format(hundredths: number): string {
  return (hundredths / 100).toFixed(2);
}
