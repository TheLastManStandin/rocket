import type { AuthResponse } from "./types";
import { initData } from "./telegram";

export class AuthError extends Error {}

/**
 * Trades the Telegram initData for a session token. This runs once on start-up
 * with no UI: the player is already signed in by virtue of opening the app
 * inside Telegram.
 */
export async function authenticate(): Promise<AuthResponse> {
  const response = await fetch("/api/auth/telegram", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ initData: initData() }),
  });

  if (!response.ok) {
    const detail = await response.json().catch(() => ({}));
    throw new AuthError(detail.error ?? `authorisation failed (${response.status})`);
  }
  return (await response.json()) as AuthResponse;
}

export function socketURL(token: string): string {
  const scheme = location.protocol === "https:" ? "wss:" : "ws:";
  return `${scheme}//${location.host}/ws?token=${encodeURIComponent(token)}`;
}
