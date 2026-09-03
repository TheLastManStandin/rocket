import { useEffect, useState } from "react";

import { format } from "../lib/multiplier";
import { haptic } from "../lib/telegram";
import type { Phase, PlayerBet } from "../lib/types";

interface Props {
  phase: Phase;
  multiplier: number;
  bet: PlayerBet | null;
  balance: number;
  phaseEndsAt: number | null;
  bettingWindowMs: number;
  minBet: number;
  connected: boolean;
  onBet: (amount: number) => void;
  onCashOut: () => void;
}

const QUICK = [100, 250, 500, 1000];

export function BetPanel({
  phase,
  multiplier,
  bet,
  balance,
  phaseEndsAt,
  bettingWindowMs,
  minBet,
  connected,
  onBet,
  onCashOut,
}: Props) {
  const [amount, setAmount] = useState(QUICK[0]);
  const remaining = useCountdown(phaseEndsAt);

  const riding = bet !== null && bet.cashedOutAt === undefined;
  const canBet = phase === "betting" && bet === null && connected && amount <= balance;
  const canCashOut = phase === "flying" && riding && connected;

  const press = () => {
    haptic("tap");
    if (canCashOut) onCashOut();
    else if (canBet) onBet(amount);
  };

  return (
    <section className="bet-panel">
      {phase === "betting" && (
        <div className="countdown" aria-hidden="true">
          <span
            className="countdown-fill"
            style={{ width: `${Math.min(100, (remaining / bettingWindowMs) * 100)}%` }}
          />
        </div>
      )}

      <div className="stake-row">
        <button
          type="button"
          className="stake-step"
          onClick={() => setAmount((a) => Math.max(minBet, a - 100))}
          disabled={!canBet}
          aria-label="Уменьшить ставку"
        >
          −
        </button>

        <span className="stake-value">{amount}</span>

        <button
          type="button"
          className="stake-step"
          onClick={() => setAmount((a) => a + 100)}
          disabled={!canBet}
          aria-label="Увеличить ставку"
        >
          +
        </button>
      </div>

      <div className="quick-row">
        {QUICK.map((value) => (
          <button
            type="button"
            key={value}
            className={value === amount ? "quick quick--on" : "quick"}
            onClick={() => setAmount(value)}
            disabled={phase !== "betting" || bet !== null}
          >
            {value}
          </button>
        ))}
        <button
          type="button"
          className="quick"
          onClick={() => setAmount(Math.max(minBet, balance))}
          disabled={phase !== "betting" || bet !== null}
        >
          Всё
        </button>
      </div>

      <button
        type="button"
        className={buttonClass(phase, bet, canBet || canCashOut)}
        onClick={press}
        disabled={!canBet && !canCashOut}
      >
        {label({ phase, multiplier, bet, balance, amount, connected })}
      </button>
    </section>
  );
}

function buttonClass(phase: Phase, bet: PlayerBet | null, live: boolean): string {
  const base = "action";
  if (!live) return `${base} action--idle`;
  if (phase === "flying" && bet) return `${base} action--cash`;
  return `${base} action--bet`;
}

function label(args: {
  phase: Phase;
  multiplier: number;
  bet: PlayerBet | null;
  balance: number;
  amount: number;
  connected: boolean;
}): string {
  const { phase, multiplier, bet, balance, amount, connected } = args;

  if (!connected) return "Соединение...";

  if (phase === "crashed") {
    if (bet?.cashedOutAt !== undefined) return `Забрано x${format(bet.cashedOutAt)}`;
    return bet ? "Улетела" : "Раунд окончен";
  }

  if (phase === "flying") {
    if (bet?.cashedOutAt !== undefined) return `Забрано x${format(bet.cashedOutAt)}`;
    if (bet) return `Забрать ${Math.floor((bet.amount * multiplier) / 100)}`;
    return "Ставки закрыты";
  }

  if (bet) return "Ставка принята";
  if (amount > balance) return "Недостаточно средств";
  return "Сделать ставку";
}

/** Milliseconds left in the current phase, ticked ten times a second. */
function useCountdown(endsAt: number | null): number {
  const [remaining, setRemaining] = useState(0);

  useEffect(() => {
    if (endsAt === null) {
      setRemaining(0);
      return;
    }
    const update = () => setRemaining(Math.max(0, endsAt - Date.now()));
    update();
    const timer = window.setInterval(update, 100);
    return () => window.clearInterval(timer);
  }, [endsAt]);

  return remaining;
}
