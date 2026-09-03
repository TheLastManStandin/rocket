// Thin bridge to the Telegram Mini App runtime. Everything is optional: the
// same bundle has to run in a plain browser during development, where
// window.Telegram simply is not there.

interface HapticFeedback {
  impactOccurred(style: "light" | "medium" | "heavy" | "rigid" | "soft"): void;
  notificationOccurred(type: "error" | "success" | "warning"): void;
}

interface TelegramWebApp {
  initData: string;
  ready(): void;
  expand(): void;
  disableVerticalSwipes?(): void;
  setHeaderColor?(color: string): void;
  setBackgroundColor?(color: string): void;
  HapticFeedback?: HapticFeedback;
}

declare global {
  interface Window {
    Telegram?: { WebApp?: TelegramWebApp };
  }
}

export function webApp(): TelegramWebApp | undefined {
  return window.Telegram?.WebApp;
}

/**
 * Puts the Mini App into the state the game wants: full height, no accidental
 * swipe-to-close while tapping "Забрать", and a black chrome to match the board.
 */
export function prepare(): void {
  const app = webApp();
  if (!app) return;

  app.ready();
  app.expand();
  app.disableVerticalSwipes?.();
  app.setHeaderColor?.("#000000");
  app.setBackgroundColor?.("#000000");
}

/** initData is empty outside Telegram; the server only accepts that in dev. */
export function initData(): string {
  return webApp()?.initData ?? "";
}

export function haptic(kind: "tap" | "win" | "lose"): void {
  const feedback = webApp()?.HapticFeedback;
  if (!feedback) return;

  if (kind === "tap") feedback.impactOccurred("medium");
  if (kind === "win") feedback.notificationOccurred("success");
  if (kind === "lose") feedback.notificationOccurred("error");
}
