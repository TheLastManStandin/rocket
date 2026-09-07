import { useState } from "react";

import { createInvoice } from "../lib/api";
import { haptic, openInvoice } from "../lib/telegram";
import { Star } from "./Star";

interface Props {
  token: string;
  packages: number[];
  onClose: () => void;
}

type Stage =
  | { kind: "idle" }
  | { kind: "waiting"; stars: number }
  | { kind: "paid"; stars: number }
  | { kind: "failed"; message: string };

/**
 * The top-up menu behind the balance chip.
 *
 * The balance itself is never set from here: the payment settles between the
 * player and Telegram, the server hears about it as an update, and the new
 * balance arrives down the socket like any other. This only starts the payment
 * and reports how it went.
 */
export function TopUpSheet({ token, packages, onClose }: Props) {
  const [stage, setStage] = useState<Stage>({ kind: "idle" });
  const busy = stage.kind === "waiting";

  const buy = async (stars: number) => {
    if (busy) return;
    haptic("tap");
    setStage({ kind: "waiting", stars });

    try {
      const link = await createInvoice(token, stars);
      const status = await openInvoice(link);

      if (status === "paid") {
        setStage({ kind: "paid", stars });
        haptic("win");
        return;
      }
      if (status === "unsupported") {
        setStage({
          kind: "failed",
          message: "Пополнение работает только внутри Telegram.",
        });
        return;
      }
      if (status === "failed") {
        setStage({ kind: "failed", message: "Платёж не прошёл." });
        return;
      }
      // "cancelled" and "pending" both leave the player where they started.
      setStage({ kind: "idle" });
    } catch (err) {
      setStage({ kind: "failed", message: (err as Error).message });
    }
  };

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
          <h2 className="sheet-title">Пополнить баланс</h2>
        </div>

        <div className="sheet-body sheet-body--topup">
          <p className="sheet-hint">1 звезда — 1 к балансу</p>

          <div className="packs">
            {packages.map((stars) => (
              <button
                key={stars}
                className="pack"
                disabled={busy}
                onClick={() => void buy(stars)}
              >
                {stars}
                <Star className="star--quick" />
              </button>
            ))}
          </div>

          {stage.kind === "waiting" && (
            <p className="sheet-note">Открываю счёт на {stage.stars}...</p>
          )}
          {stage.kind === "paid" && (
            <p className="sheet-note sheet-note--good">
              Оплачено: {stage.stars}. Баланс обновится через секунду.
            </p>
          )}
          {stage.kind === "failed" && (
            <p className="sheet-note sheet-note--bad">{stage.message}</p>
          )}
        </div>
      </div>
    </div>
  );
}
