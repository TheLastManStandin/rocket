// Wire types, mirroring internal/game.Event on the server.
//
// Every multiplier crosses the wire as hundredths (250 means x2.50). The server
// settles money on these integers, so the client keeps them intact and only
// divides when it is about to draw.

export type Phase = "betting" | "flying" | "crashed";

export interface Bot {
  id: string;
  name: string;
  hue: number;
  initial: string;
  bet: number;
  /** Present only once the curve has actually passed the bot's exit. */
  cashedOutAt?: number;
  payout?: number;
}

export interface ServerEvent {
  type: string;
  roundId?: number;
  phase?: Phase;
  endsInMs?: number;
  multiplier?: number;
  bots?: Bot[];
  botId?: string;
  amount?: number;
  payout?: number;
  balance?: number;
  history?: number[];
  code?: string;
  message?: string;
}

export const EVENT = {
  state: "state",
  roundOpened: "round_opened",
  tookOff: "took_off",
  tick: "tick",
  botJoined: "bot_joined",
  botCashedOut: "bot_cashed_out",
  crashed: "crashed",
  betPlaced: "bet_placed",
  cashedOut: "cashed_out",
  balance: "balance",
  error: "error",
} as const;

export interface AuthResponse {
  token: string;
  balance: number;
  limits: {
    minBet: number;
    maxBet: number;
  };
  /** Amounts the top-up sheet may offer. Empty when the server sells no Stars. */
  starPackages?: number[];
  user: {
    id: number;
    username: string;
    firstName: string;
    photoUrl: string;
  };
}

/** The player's own stake on the round in view. */
export interface PlayerBet {
  amount: number;
  cashedOutAt?: number;
  payout?: number;
}
