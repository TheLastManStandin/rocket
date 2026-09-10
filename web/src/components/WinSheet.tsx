import { useEffect, useRef } from "react";
import confetti from "canvas-confetti";

import { format } from "../lib/multiplier";
import { Star } from "./Star";

interface Props {
  /** Stars the round paid out. The balance already carries them. */
  payout: number;
  /** Where the exit landed, in hundredths. */
  cashedOutAt: number;
  onClose: () => void;
}

/** The burst that goes off with the sheet, matched to the reference board's. */
const CONFETTI_COUNT = 120;
const CONFETTI_SPREAD = 80;
/**
 * How far down the sheet the burst starts, as a fraction of its height. The
 * reference fires from a quarter of the way in, which on a sheet this tall
 * puts the origin off the bottom of the screen -- so the confetti comes up
 * from below the edge and falls back through the sheet.
 */
const CONFETTI_DEPTH = 0.25;

/**
 * What a cash-out looks like: the same sheet the stake was placed from, with
 * the Stars it paid out and a burst of confetti over it.
 *
 * It reports the round rather than settling it. The payout was banked by the
 * server the moment the cash-out went in, and the balance arrived down the
 * socket on its own; closing this sheet -- or never opening it -- costs
 * nothing.
 */
export function WinSheet({ payout, cashedOutAt, onClose }: Props) {
  const sheetRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const sheet = sheetRef.current;
    if (!sheet) return;

    // offsetHeight, not the bounding box: the sheet is mid-rise on this frame
    // and its box is still off the bottom of the screen. The height is what
    // the transform does not touch, and the sheet is anchored to the bottom,
    // so this is where it is about to come to rest.
    const top = window.innerHeight - sheet.offsetHeight;
    const y = (top + sheet.offsetHeight * CONFETTI_DEPTH) / window.innerHeight;

    confetti({
      particleCount: CONFETTI_COUNT,
      spread: CONFETTI_SPREAD,
      origin: { x: 0.5, y: Math.min(1, Math.max(0, y)) },
    });
  }, []);

  return (
    <div className="sheet-backdrop" onClick={onClose}>
      {/* The sheet swallows taps so only the backdrop closes it. */}
      <div className="sheet" ref={sheetRef} onClick={(e) => e.stopPropagation()}>
        <div className="sheet-grip-row">
          <span className="sheet-grip" />
        </div>

        <div className="sheet-head">
          <button className="sheet-close" onClick={onClose} aria-label="Закрыть">
            <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true">
              <path
                fill="#fff"
                d="M12.293 2.293a1 1 0 1 1 1.414 1.414L9.414 8l4.293 4.293a1 1 0 0 1-1.414 1.414L8 9.414l-4.293 4.293a1 1 0 1 1-1.414-1.414L6.586 8 2.293 3.707a1 1 0 0 1 1.414-1.414L8 6.586z"
              />
            </svg>
          </button>
          <h2 className="sheet-title">Выигрыш</h2>
        </div>

        <div className="sheet-body sheet-body--win">
          <div className="win-prize">
            <Star className="star--win" />
            <span className="win-amount">{payout}</span>
          </div>

          <p className="sheet-hint">Забрано на x{format(cashedOutAt)}</p>

          <button type="button" className="sheet-cta" onClick={onClose}>
            Отлично
          </button>
        </div>
      </div>
    </div>
  );
}
