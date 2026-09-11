import { useEffect } from "react";
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

/** The burst that goes off with the sheet. */
const CONFETTI_COUNT = 120;
const CONFETTI_SPREAD = 80;
/**
 * How hard the confetti is thrown, in pixels of the first frame's travel. The
 * library's own default carries a burst about two thirds of the way up from
 * the middle of a phone screen; this one is fired off the bottom edge, so it
 * has the whole height to climb and needs the extra to clear it.
 */
const CONFETTI_VELOCITY = 68;

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
  useEffect(() => {
    // Off the bottom edge of the screen, not off the sheet: the confetti is
    // fired up past the sheet from under it, rather than out of its middle.
    confetti({
      particleCount: CONFETTI_COUNT,
      spread: CONFETTI_SPREAD,
      startVelocity: CONFETTI_VELOCITY,
      origin: { x: 0.5, y: 1 },
    });
  }, []);

  return (
    <div className="sheet-backdrop" onClick={onClose}>
      {/* The sheet swallows taps so only the backdrop closes it. */}
      <div className="sheet" onClick={(e) => e.stopPropagation()}>
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
