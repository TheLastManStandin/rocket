import { memo } from "react";

import { format } from "../lib/multiplier";
import type { Bot, PlayerBet } from "../lib/types";

interface Props {
  bots: Bot[];
  bet: PlayerBet | null;
  playerName: string;
  /** Live curve, in hundredths, for valuing stakes still in the air. */
  multiplier: number;
}

/**
 * The table under the curve. Rows are inert by design: the original opens a
 * profile on tap, this one deliberately does nothing.
 */
export const PlayersList = memo(function PlayersList({
  bots,
  bet,
  playerName,
  multiplier,
}: Props) {
  const totalBets = bots.length + (bet ? 1 : 0);
  const totalStake = bots.reduce((sum, b) => sum + b.bet, 0) + (bet?.amount ?? 0);

  return (
    <section className="players">
      <ul className="players-list">
        {bet && (
          <Row
            key="me"
            name={playerName}
            initial={initialOf(playerName)}
            hue={210}
            stake={bet.amount}
            cashedOutAt={bet.cashedOutAt}
            payout={bet.payout}
            multiplier={multiplier}
            mine
          />
        )}
        {bots.map((bot) => (
          <Row
            key={bot.id}
            name={bot.name}
            initial={bot.initial}
            hue={bot.hue}
            stake={bot.bet}
            cashedOutAt={bot.cashedOutAt}
            payout={bot.payout}
            multiplier={multiplier}
          />
        ))}
      </ul>

      <footer className="players-total">
        Всего {totalBets} {plural(totalBets, "ставка", "ставки", "ставок")}, {totalStake}
      </footer>
    </section>
  );
});

interface RowProps {
  name: string;
  initial: string;
  hue: number;
  stake: number;
  cashedOutAt?: number;
  payout?: number;
  multiplier: number;
  mine?: boolean;
}

function Row({ name, initial, hue, stake, cashedOutAt, payout, multiplier, mine }: RowProps) {
  const won = cashedOutAt !== undefined;
  // Still in the air: show what the stake is worth at the current curve, which
  // is the number the original ticks up beside every rider.
  const showing = won ? (payout ?? 0) : Math.floor((stake * multiplier) / 100);

  return (
    <li className={mine ? "player-row player-row--mine" : "player-row"}>
      <span className="player-avatar" style={{ background: `hsl(${hue} 55% 42%)` }}>
        {initial}
      </span>

      <span className="player-identity">
        <span className="player-name">{name}</span>
        <span className="player-stake">
          <Star />
          {stake}
        </span>
      </span>

      <span className={won ? "player-payout player-payout--won" : "player-payout"}>
        <Star />
        {showing}
        {won && <em className="player-at">x{format(cashedOutAt)}</em>}
      </span>
    </li>
  );
}

function Star() {
  return (
    <svg viewBox="0 0 24 24" className="star" aria-hidden="true">
      <path
        fill="currentColor"
        d="m12 2.6 2.9 5.9 6.5.9-4.7 4.6 1.1 6.4-5.8-3-5.8 3 1.1-6.4L2.6 9.4l6.5-.9z"
      />
    </svg>
  );
}

function initialOf(name: string): string {
  const match = name.match(/[\p{L}\p{N}]/u);
  return match ? match[0].toUpperCase() : "?";
}

function plural(n: number, one: string, few: string, many: string): string {
  const mod100 = n % 100;
  if (mod100 >= 11 && mod100 <= 14) return many;
  const mod10 = n % 10;
  if (mod10 === 1) return one;
  if (mod10 >= 2 && mod10 <= 4) return few;
  return many;
}
