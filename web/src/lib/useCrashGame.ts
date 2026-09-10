import { useCallback, useEffect, useRef, useState } from "react";

import { socketURL } from "./api";
import { multiplierAt, secondsToReach } from "./multiplier";
import { EVENT, type Bot, type Phase, type PlayerBet, type ServerEvent } from "./types";

export interface CrashState {
  connected: boolean;
  phase: Phase;
  roundId: number;
  /** Hundredths, interpolated between server ticks. */
  multiplier: number;
  /** Absolute time the current phase ends, for the countdown. */
  phaseEndsAt: number | null;
  /**
   * Full length of the betting window, learnt from the server when a round
   * opens. Hard-coding it here would silently desync the countdown bar the
   * moment BETTING_WINDOW changed on the server.
   */
  bettingWindowMs: number;
  bots: Bot[];
  history: number[];
  balance: number;
  bet: PlayerBet | null;
  /**
   * A stake taken while the last round was in the air, in Stars. It is already
   * paid for; the server seats it when the next round opens.
   */
  queued: number | null;
  error: string | null;
}

const initialState: CrashState = {
  connected: false,
  phase: "betting",
  roundId: 0,
  multiplier: 100,
  phaseEndsAt: null,
  bettingWindowMs: 7000,
  bots: [],
  history: [],
  balance: 0,
  bet: null,
  queued: null,
  error: null,
};

const REFUSALS: Record<string, string> = {
  bets_closed: "Ставки на этот раунд уже закрыты",
  already_bet: "Ставка на этот раунд уже сделана",
  already_queued: "Ставка на следующий раунд уже сделана",
  stake_out_of_range: "Такая сумма недоступна",
  not_flying: "Не успели — ракета уже взорвалась",
  no_bet: "Ставка не сделана",
  already_cashed_out: "Выигрыш уже забран",
  insufficient_funds: "Недостаточно средств",
  session_closed: "Соединение потеряно, переподключаемся",
};

const RECONNECT_MIN = 500;
const RECONNECT_MAX = 10_000;

export function useCrashGame(token: string | null) {
  const [state, setState] = useState<CrashState>(initialState);

  const socketRef = useRef<WebSocket | null>(null);
  // Local clock origin for the flight, nudged back into line by every tick.
  const takeoffRef = useRef<number | null>(null);
  const retryRef = useRef(RECONNECT_MIN);
  const closedRef = useRef(false);

  const send = useCallback((payload: object) => {
    const socket = socketRef.current;
    if (socket?.readyState === WebSocket.OPEN) socket.send(JSON.stringify(payload));
  }, []);

  const bet = useCallback((amount: number) => send({ type: "bet", amount }), [send]);
  const cashOut = useCallback(() => send({ type: "cashout" }), [send]);

  useEffect(() => {
    if (!token) return;
    closedRef.current = false;

    let reconnectTimer: number | undefined;

    const connect = () => {
      if (closedRef.current) return;

      const socket = new WebSocket(socketURL(token));
      socketRef.current = socket;

      socket.onopen = () => {
        retryRef.current = RECONNECT_MIN;
        setState((s) => ({ ...s, connected: true, error: null }));
      };

      socket.onmessage = (message) => {
        const event = JSON.parse(message.data as string) as ServerEvent;
        setState((s) => reduce(s, event, takeoffRef));
      };

      socket.onclose = () => {
        setState((s) => ({ ...s, connected: false }));
        if (closedRef.current) return;
        reconnectTimer = window.setTimeout(connect, retryRef.current);
        retryRef.current = Math.min(retryRef.current * 2, RECONNECT_MAX);
      };

      // onerror is always followed by onclose, which owns the retry.
      socket.onerror = () => socket.close();
    };

    connect();

    return () => {
      closedRef.current = true;
      window.clearTimeout(reconnectTimer);
      socketRef.current?.close();
      socketRef.current = null;
    };
  }, [token]);

  // The server ticks ten times a second; this fills in the frames between so the
  // curve climbs smoothly instead of stepping.
  useEffect(() => {
    if (state.phase !== "flying") return;

    let frame = 0;
    const draw = () => {
      const takeoff = takeoffRef.current;
      if (takeoff !== null) {
        const flying = (performance.now() - takeoff) / 1000;
        setState((s) => (s.phase === "flying" ? { ...s, multiplier: multiplierAt(flying) } : s));
      }
      frame = requestAnimationFrame(draw);
    };
    frame = requestAnimationFrame(draw);
    return () => cancelAnimationFrame(frame);
  }, [state.phase]);

  return { state, bet, cashOut };
}

function reduce(
  s: CrashState,
  e: ServerEvent,
  takeoffRef: { current: number | null },
): CrashState {
  switch (e.type) {
    case EVENT.state: {
      // A snapshot arrives on connect and on every reconnect, so it has to be
      // able to drop the client into the middle of a flight.
      if (e.phase === "flying" && e.multiplier) {
        takeoffRef.current = performance.now() - secondsToReach(e.multiplier) * 1000;
      }
      return {
        ...s,
        phase: e.phase ?? s.phase,
        roundId: e.roundId ?? s.roundId,
        multiplier: e.multiplier ?? 100,
        phaseEndsAt: e.endsInMs ? Date.now() + e.endsInMs : null,
        bots: e.bots ?? [],
        history: e.history ?? [],
        bet: e.amount
          ? { amount: e.amount, cashedOutAt: e.payout ? e.multiplier : undefined, payout: e.payout }
          : null,
        queued: e.queuedAmount ?? null,
        error: null,
      };
    }

    case EVENT.roundOpened:
      takeoffRef.current = null;
      return {
        ...s,
        phase: "betting",
        roundId: e.roundId ?? s.roundId,
        multiplier: 100,
        phaseEndsAt: e.endsInMs ? Date.now() + e.endsInMs : null,
        // Only round_opened carries the whole window; a snapshot's endsInMs is
        // whatever is left of it.
        bettingWindowMs: e.endsInMs ?? s.bettingWindowMs,
        bots: e.bots ?? [],
        // A stake that was waiting arrives seated: the server sends bet_placed
        // straight after this, with no balance on it, because the money went
        // when the stake was taken.
        bet: null,
        queued: null,
        error: null,
      };

    case EVENT.tookOff:
      takeoffRef.current = performance.now();
      return { ...s, phase: "flying", multiplier: 100, phaseEndsAt: null };

    case EVENT.tick: {
      // Re-anchor the local clock so drift never accumulates.
      if (e.multiplier) {
        takeoffRef.current = performance.now() - secondsToReach(e.multiplier) * 1000;
      }
      // The tick also carries the curve itself, and it has to land in state.
      // The frame loop below is what normally advances the multiplier, and it
      // is not running while the Mini App is in the background -- an auto
      // cash-out reading a curve frozen at x1.00 would sit there and let the
      // round burst. Never step backwards: between ticks the frame loop is
      // ahead, and rewinding it would stutter the curve.
      const multiplier = Math.max(s.multiplier, e.multiplier ?? 0);
      return { ...s, phase: "flying", multiplier };
    }

    case EVENT.botJoined:
      // The table fills over the countdown, a few at a time.
      return { ...s, bots: [...s.bots, ...(e.bots ?? [])] };

    case EVENT.botCashedOut:
      return {
        ...s,
        bots: s.bots.map((b) =>
          b.id === e.botId ? { ...b, cashedOutAt: e.multiplier, payout: e.payout } : b,
        ),
      };

    case EVENT.crashed:
      takeoffRef.current = null;
      return {
        ...s,
        phase: "crashed",
        multiplier: e.multiplier ?? s.multiplier,
        history: e.history ?? s.history,
      };

    case EVENT.betPlaced:
      return {
        ...s,
        bet: { amount: e.amount ?? 0 },
        queued: null,
        balance: e.balance ?? s.balance,
        error: null,
      };

    case EVENT.betQueued:
      return {
        ...s,
        queued: e.amount ?? 0,
        balance: e.balance ?? s.balance,
        error: null,
      };

    case EVENT.cashedOut:
      return {
        ...s,
        bet: s.bet
          ? { ...s.bet, cashedOutAt: e.multiplier, payout: e.payout }
          : s.bet,
        balance: e.balance ?? s.balance,
      };

    case EVENT.balance:
      return { ...s, balance: e.balance ?? s.balance };

    case EVENT.error:
      return { ...s, error: REFUSALS[e.code ?? ""] ?? e.message ?? "Что-то пошло не так" };

    default:
      return s;
  }
}
