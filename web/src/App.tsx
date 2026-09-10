import { useEffect, useRef, useState } from "react";

import { BetButton } from "./components/BetButton";
import { BetSheet } from "./components/BetSheet";
import { CrashCanvas } from "./components/CrashCanvas";
import { HistoryStrip } from "./components/HistoryStrip";
import { PlayersList } from "./components/PlayersList";
import { Star, StarDefs } from "./components/Star";
import { TopUpSheet } from "./components/TopUpSheet";
import { WinSheet } from "./components/WinSheet";
import { authenticate } from "./lib/api";
import { haptic, prepare } from "./lib/telegram";
import type { AuthResponse } from "./lib/types";
import { useCrashGame, type CrashState } from "./lib/useCrashGame";

export function App() {
  const [account, setAccount] = useState<AuthResponse | null>(null);
  const [failure, setFailure] = useState<string | null>(null);
  const [toppingUp, setToppingUp] = useState(false);
  const [betting, setBetting] = useState(false);
  // Hundredths, or null while the player has auto cash-out switched off. It
  // outlives the sheet so reopening it offers back the last target set.
  const [autoCashOut, setAutoCashOut] = useState<number | null>(null);

  useEffect(() => {
    prepare();
    authenticate()
      .then(setAccount)
      .catch((err: Error) => setFailure(err.message));
  }, []);

  const { state, bet, cashOut } = useCrashGame(account?.token ?? null, account?.playerId ?? 0);

  // The balance the socket reports wins once it has said anything; the auth
  // response only seeds the very first paint.
  const balance = state.balance || account?.balance || 0;

  useSettlementHaptics(state.phase, state.bet);
  useAutoCashOut(state, autoCashOut, cashOut);
  const [win, dismissWin] = useWinSheet(state);

  // The menu stays valid whatever the round is doing -- outside the window it
  // takes bets on the next one -- so it closes on the stake landing, not on
  // the phase turning over.
  useEffect(() => {
    if (state.bet || state.queued !== null) setBetting(false);
  }, [state.bet, state.queued]);

  if (failure) {
    return (
      <main className="screen screen--message">
        <p className="message">Не удалось войти: {failure}</p>
        <p className="message message--dim">Откройте приложение через Telegram.</p>
      </main>
    );
  }

  if (!account) {
    return (
      <main className="screen screen--message">
        <p className="message message--dim">Загрузка...</p>
      </main>
    );
  }

  return (
    <main className="screen">
      <StarDefs />

      <header className="topbar">
        <button
          className="balance"
          onClick={() => {
            haptic("tap");
            setToppingUp(true);
          }}
        >
          <span className="balance-value">{balance}</span>
          <Star className="star--balance" />
        </button>
      </header>

      <div className="stage">
        <CrashCanvas
          phase={state.phase}
          multiplier={state.multiplier}
          phaseEndsAt={state.phaseEndsAt}
          mine={state.bet !== null}
        />

        <HistoryStrip history={state.history} phase={state.phase} current={state.multiplier} />

        <BetButton
          phase={state.phase}
          bet={state.bet}
          queued={state.queued}
          connected={state.connected}
          onOpen={() => setBetting(true)}
          onCashOut={cashOut}
        />

        <PlayersList bots={state.bots} players={state.players} multiplier={state.multiplier} />
      </div>

      {state.error && <p className="notice">{state.error}</p>}

      {betting && (
        <BetSheet
          balance={balance}
          minBet={account.limits?.minBet ?? 10}
          maxBet={account.limits?.maxBet ?? 100000}
          autoCashOut={autoCashOut}
          onAutoCashOut={setAutoCashOut}
          onSubmit={(amount) => {
            bet(amount);
            setBetting(false);
          }}
          onClose={() => setBetting(false)}
        />
      )}

      {toppingUp && (
        <TopUpSheet
          token={account.token}
          packages={account.starPackages ?? []}
          onClose={() => setToppingUp(false)}
        />
      )}

      {win && (
        <WinSheet payout={win.payout} cashedOutAt={win.cashedOutAt} onClose={dismissWin} />
      )}
    </main>
  );
}

/**
 * Holds the cash-out that is waiting to be shown, from the moment the server
 * confirms it until the player closes the sheet or the next round opens.
 *
 * It reads the settlement rather than the tap: the button only asks, and a
 * request that arrives after the round has burst wins nothing. Once per round,
 * so a reconnect that replays the snapshot does not pop the sheet a second
 * time.
 */
function useWinSheet(state: CrashState): [Win | null, () => void] {
  const [win, setWin] = useState<Win | null>(null);
  const shownFor = useRef<number | null>(null);

  useEffect(() => {
    const bet = state.bet;
    if (!bet || bet.cashedOutAt === undefined) return;
    if (shownFor.current === state.roundId) return;

    shownFor.current = state.roundId;
    setWin({ payout: bet.payout ?? 0, cashedOutAt: bet.cashedOutAt });
  }, [state.bet, state.roundId]);

  // A sheet left open is closed by the next round rather than sitting over a
  // board that has already moved on.
  useEffect(() => setWin(null), [state.roundId]);

  return [win, () => setWin(null)];
}

interface Win {
  payout: number;
  cashedOutAt: number;
}

/** One buzz when the round settles, in whichever direction. */
function useSettlementHaptics(phase: string, bet: { cashedOutAt?: number } | null) {
  const previous = useRef(phase);

  useEffect(() => {
    if (previous.current !== "crashed" && phase === "crashed" && bet) {
      haptic(bet.cashedOutAt !== undefined ? "win" : "lose");
    }
    previous.current = phase;
  }, [phase, bet]);
}

/**
 * Takes the money out the moment the curve reaches the target the player set in
 * the betting menu.
 *
 * The request is the same one the button sends, so the server still decides
 * where the exit lands -- this only decides when to ask. It asks once per
 * round: a refused request must not turn into a retry loop against a round
 * that has already burst.
 */
function useAutoCashOut(state: CrashState, target: number | null, cashOut: () => void) {
  const askedFor = useRef<number | null>(null);

  useEffect(() => {
    if (target === null) return;
    if (state.phase !== "flying") return;
    if (!state.bet || state.bet.cashedOutAt !== undefined) return;
    if (state.multiplier < target) return;
    if (askedFor.current === state.roundId) return;

    askedFor.current = state.roundId;
    cashOut();
  }, [state, target, cashOut]);
}
