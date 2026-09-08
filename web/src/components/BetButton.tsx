import type { ReactNode } from "react";

import { haptic } from "../lib/telegram";
import type { Phase, PlayerBet } from "../lib/types";
import { Star } from "./Star";

interface Props {
  phase: Phase;
  bet: PlayerBet | null;
  /** A stake already paid for and waiting for the next round, in Stars. */
  queued: number | null;
  connected: boolean;
  /** Opens the betting menu, for this round or the next one. */
  onOpen: () => void;
  onCancel: () => void;
  onCashOut: () => void;
}

/**
 * The one control on the board. Which of the four things it is doing is
 * decided here rather than by the caller: opening the menu, cashing out,
 * taking a waiting stake back, or saying what the round is doing.
 */
export function BetButton({ phase, bet, queued, connected, onOpen, onCancel, onCashOut }: Props) {
  const riding = bet !== null && bet.cashedOutAt === undefined;
  const betsOpen = phase === "betting";

  const canCashOut = phase === "flying" && riding && connected;
  // While the rocket is up the menu takes bets on the round after it, so the
  // button stays live for the whole cycle rather than only in the window.
  const canOpen = connected && !canCashOut && (betsOpen ? bet === null : queued === null);
  const canCancel = connected && !betsOpen && queued !== null;

  const press = () => {
    haptic("tap");
    if (canCashOut) onCashOut();
    else if (canCancel) onCancel();
    else if (canOpen) onOpen();
  };

  return (
    <button
      type="button"
      className={className(canCashOut, canCancel)}
      disabled={!canCashOut && !canCancel && !canOpen}
      onClick={press}
    >
      {label({ phase, bet, queued, connected, riding, betsOpen })}
    </button>
  );
}

function className(cashing: boolean, cancelling: boolean): string {
  if (cashing) return "action action--cash";
  if (cancelling) return "action action--cancel";
  return "action";
}

function label(args: {
  phase: Phase;
  bet: PlayerBet | null;
  queued: number | null;
  connected: boolean;
  riding: boolean;
  betsOpen: boolean;
}): ReactNode {
  const { phase, bet, queued, connected, riding, betsOpen } = args;

  if (!connected) return "Соединение...";
  if (phase === "flying" && riding) return "Забрать";

  if (betsOpen) return bet ? "Ставка принята" : "Сделать ставку";

  if (queued !== null) {
    return (
      <>
        Отменить
        <span className="action-amount">
          <Star className="star--action" />
          {queued}
        </span>
      </>
    );
  }
  return "Ставка на следующий раунд";
}
