import { useEffect, useRef, useState } from "react";

import { BetPanel } from "./components/BetPanel";
import { CrashCanvas } from "./components/CrashCanvas";
import { HistoryStrip } from "./components/HistoryStrip";
import { PlayersList } from "./components/PlayersList";
import { authenticate } from "./lib/api";
import { haptic, prepare } from "./lib/telegram";
import type { AuthResponse } from "./lib/types";
import { useCrashGame } from "./lib/useCrashGame";

export function App() {
  const [account, setAccount] = useState<AuthResponse | null>(null);
  const [failure, setFailure] = useState<string | null>(null);

  useEffect(() => {
    prepare();
    authenticate()
      .then(setAccount)
      .catch((err: Error) => setFailure(err.message));
  }, []);

  const { state, bet, cashOut } = useCrashGame(account?.token ?? null);

  // The balance the socket reports wins once it has said anything; the auth
  // response only seeds the very first paint.
  const balance = state.balance || account?.balance || 0;

  useSettlementHaptics(state.phase, state.bet);

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
      <header className="topbar">
        <span className="balance">
          <svg viewBox="0 0 24 24" className="star" aria-hidden="true">
            <path
              fill="currentColor"
              d="m12 2.6 2.9 5.9 6.5.9-4.7 4.6 1.1 6.4-5.8-3-5.8 3 1.1-6.4L2.6 9.4l6.5-.9z"
            />
          </svg>
          {balance}
        </span>
      </header>

      <div className="board">
        <CrashCanvas
          phase={state.phase}
          multiplier={state.multiplier}
          phaseEndsAt={state.phaseEndsAt}
        />
      </div>

      <HistoryStrip history={state.history} phase={state.phase} current={state.multiplier} />

      <BetPanel
        phase={state.phase}
        multiplier={state.multiplier}
        bet={state.bet}
        balance={balance}
        phaseEndsAt={state.phaseEndsAt}
        bettingWindowMs={state.bettingWindowMs}
        minBet={account.limits?.minBet ?? 10}
        connected={state.connected}
        onBet={bet}
        onCashOut={cashOut}
      />

      {state.error && <p className="notice">{state.error}</p>}

      <PlayersList
        bots={state.bots}
        bet={state.bet}
        multiplier={state.multiplier}
        playerName={account.user.firstName || account.user.username || "Вы"}
      />
    </main>
  );
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
