import { haptic } from "../lib/telegram";
import type { Phase, PlayerBet } from "../lib/types";

interface Props {
  phase: Phase;
  bet: PlayerBet | null;
  connected: boolean;
  /** Opens the betting menu; only ever called while bets are open. */
  onOpen: () => void;
  onCashOut: () => void;
}

/**
 * The one control on the board. It opens the betting menu while bets are open,
 * turns into the cash-out while the player's stake is in the air, and spends
 * the rest of the round saying what the round is doing.
 */
export function BetButton({ phase, bet, connected, onOpen, onCashOut }: Props) {
  const riding = bet !== null && bet.cashedOutAt === undefined;
  const canOpen = phase === "betting" && bet === null && connected;
  const canCashOut = phase === "flying" && riding && connected;

  const press = () => {
    haptic("tap");
    if (canCashOut) onCashOut();
    else if (canOpen) onOpen();
  };

  return (
    <button
      type="button"
      className={canCashOut ? "action action--cash" : "action"}
      disabled={!canOpen && !canCashOut}
      onClick={press}
    >
      {label({ phase, bet, connected, riding })}
    </button>
  );
}

function label(args: {
  phase: Phase;
  bet: PlayerBet | null;
  connected: boolean;
  riding: boolean;
}): string {
  const { phase, bet, connected, riding } = args;

  if (!connected) return "Соединение...";
  if (phase === "betting") return bet ? "Ставка принята" : "Сделать ставку";
  if (phase === "flying") {
    if (riding) return "Забрать";
    return bet ? "Ожидание следующего раунда" : "Ставки приняты";
  }
  return "Ожидание следующего раунда";
}
