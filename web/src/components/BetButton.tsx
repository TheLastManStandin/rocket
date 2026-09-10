import { haptic } from "../lib/telegram";
import type { Phase, PlayerBet } from "../lib/types";

interface Props {
  phase: Phase;
  bet: PlayerBet | null;
  /** A stake already paid for and waiting for the next round, in Stars. */
  queued: number | null;
  connected: boolean;
  /** Opens the betting menu, for this round or the next one. */
  onOpen: () => void;
  onCashOut: () => void;
}

/**
 * The one control on the board, and one way of putting a stake down: the menu
 * opens whatever the round is doing, and where the stake lands -- this round
 * or the next -- is the server's business, not something the player picks.
 * Once it is down it stays down.
 */
export function BetButton({ phase, bet, queued, connected, onOpen, onCashOut }: Props) {
  const riding = bet !== null && bet.cashedOutAt === undefined;
  const betsOpen = phase === "betting";
  // A stake on whichever round the menu would have taken one for.
  const staked = betsOpen ? bet !== null : queued !== null;

  const canCashOut = phase === "flying" && riding && connected;
  const canOpen = connected && !canCashOut && !staked;

  const press = () => {
    haptic("tap");
    if (canCashOut) onCashOut();
    else if (canOpen) onOpen();
  };

  return (
    <button
      type="button"
      className={canCashOut ? "action action--cash" : "action"}
      disabled={!canCashOut && !canOpen}
      onClick={press}
    >
      {label({ canCashOut, connected, staked })}
    </button>
  );
}

function label(args: { canCashOut: boolean; connected: boolean; staked: boolean }): string {
  const { canCashOut, connected, staked } = args;

  if (!connected) return "Соединение...";
  if (canCashOut) return "Забрать";
  return staked ? "Ставка принята" : "Сделать ставку";
}
