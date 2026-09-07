import { memo } from "react";

import type { Bot, PlayerBet } from "../lib/types";
import { Star } from "./Star";

interface Props {
  bots: Bot[];
  bet: PlayerBet | null;
  playerName: string;
  playerPhoto: string;
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
  playerPhoto,
  multiplier,
}: Props) {
  const totalBets = bots.length + (bet ? 1 : 0);
  const totalStake = bots.reduce((sum, b) => sum + b.bet, 0) + (bet?.amount ?? 0);

  return (
    <div className="table">
      <ul className="players">
        {bet && (
          <Row
            key="me"
            name={playerName}
            photo={playerPhoto}
            initial={initialOf(playerName)}
            hue={210}
            stake={bet.amount}
            cashedOutAt={bet.cashedOutAt}
            payout={bet.payout}
            multiplier={multiplier}
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

      <p className="players-total">
        {totalBets === 0
          ? "Ставок еще нет, 0"
          : `Всего ${totalBets} ${plural(totalBets, "ставка", "ставки", "ставок")}, ${totalStake}`}
        <Star className="star--total" />
      </p>
    </div>
  );
});

interface RowProps {
  name: string;
  photo?: string;
  initial: string;
  hue: number;
  stake: number;
  cashedOutAt?: number;
  payout?: number;
  multiplier: number;
}

function Row({ name, photo, initial, hue, stake, cashedOutAt, payout, multiplier }: RowProps) {
  const won = cashedOutAt !== undefined;
  // Still in the air: show what the stake is worth at the current curve, which
  // is the number the original ticks up beside every rider.
  const showing = won ? (payout ?? 0) : Math.floor((stake * multiplier) / 100);

  return (
    <li className="player-row">
      <div className="player-who">
        {photo ? (
          <img className="player-avatar" src={photo} alt="" />
        ) : (
          <span className="player-avatar" style={{ background: `hsl(${hue} 62% 58%)` }}>
            {initial}
          </span>
        )}

        <span className="player-identity">
          <span className="player-name">{name}</span>
          <span className="player-stake">
            <Star className="star--stake" />
            {stake}
          </span>
        </span>
      </div>

      <span className="player-payout">
        <Star className="star--payout" />
        <span className={won ? "player-value player-value--won" : "player-value"}>{showing}</span>
      </span>
    </li>
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
