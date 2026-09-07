import { useEffect, useRef, useState } from "react";

import { haptic } from "../lib/telegram";
import { Star } from "./Star";

interface Props {
  balance: number;
  minBet: number;
  maxBet: number;
  /** Auto cash-out in hundredths, or null when the player has it switched off. */
  autoCashOut: number | null;
  onAutoCashOut: (target: number | null) => void;
  onSubmit: (amount: number) => void;
  onClose: () => void;
}

/** The presets the reference board offers above the keypad. */
const QUICK = [50, 500, 1000, 5000];

/** Bounds and step of the auto cash-out spinner, in hundredths. */
const AUTO_MIN = 110;
const AUTO_MAX = 10000;
const AUTO_STEP = 10;
const AUTO_DEFAULT = 200;

/**
 * The betting menu: a sheet that rises over the board, takes a sum in Stars and
 * an optional auto cash-out, and hands the stake back to the caller.
 *
 * It never touches the balance itself. The stake is only spent once the server
 * has taken the bet, so a sheet dismissed halfway costs nothing.
 */
export function BetSheet({
  balance,
  minBet,
  maxBet,
  autoCashOut,
  onAutoCashOut,
  onSubmit,
  onClose,
}: Props) {
  const [text, setText] = useState("");
  const [auto, setAuto] = useState(autoCashOut ?? AUTO_DEFAULT);
  const [autoOn, setAutoOn] = useState(autoCashOut !== null);
  const [autoText, setAutoText] = useState(formatAuto(autoCashOut ?? AUTO_DEFAULT));

  const amount = Number(text) || 0;
  const short = amount > balance;
  const tooSmall = amount < minBet;
  const canSubmit = !short && !tooSmall && amount <= maxBet;

  // The sheet owns the auto cash-out while it is open; the caller only hears
  // the final answer, when the bet goes in.
  const setAutoValue = (next: number) => {
    const clamped = Math.min(AUTO_MAX, Math.max(AUTO_MIN, next));
    setAuto(clamped);
    setAutoText(formatAuto(clamped));
  };

  const submit = () => {
    if (!canSubmit) return;
    haptic("tap");
    onAutoCashOut(autoOn ? auto : null);
    onSubmit(amount);
  };

  return (
    <div className="sheet-backdrop" onClick={onClose}>
      {/* The sheet swallows taps so only the backdrop closes it. */}
      <div className="sheet" onClick={(e) => e.stopPropagation()}>
        <div className="sheet-grip-row">
          <span className="sheet-grip" />
        </div>

        <div className="sheet-head">
          <button className="sheet-close" onClick={onClose} aria-label="Закрыть">
            <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true">
              <path
                fill="#fff"
                d="M12.293 2.293a1 1 0 1 1 1.414 1.414L9.414 8l4.293 4.293a1 1 0 0 1-1.414 1.414L8 9.414l-4.293 4.293a1 1 0 1 1-1.414-1.414L6.586 8 2.293 3.707a1 1 0 0 1 1.414-1.414L8 6.586z"
              />
            </svg>
          </button>
          <h2 className="sheet-title">Введите сумму</h2>
        </div>

        <div className="sheet-body">
          <div className="segment">
            <span className="segment-on">Звёзды</span>
          </div>

          <div className="amount-zone">
            <div className="amount-stack">
              <div className="amount-row">
                <AmountInput text={text} onText={setText} maxBet={maxBet} />
                <Star className="star--amount" />
              </div>

              <div className="quick-row">
                {QUICK.map((value) => (
                  <button
                    type="button"
                    key={value}
                    className={value === amount ? "quick quick--on" : "quick"}
                    onClick={() => {
                      haptic("tap");
                      setText(String(value));
                    }}
                  >
                    {value}
                    <Star className="star--quick" />
                  </button>
                ))}
              </div>
            </div>
          </div>

          <div className="auto-row">
            <label className="auto-toggle">
              <input
                type="checkbox"
                className="auto-box"
                checked={autoOn}
                onChange={(e) => setAutoOn(e.target.checked)}
              />
              <span>Авто вывод</span>
            </label>

            <div className={autoOn ? "stepper" : "stepper stepper--off"}>
              <button
                type="button"
                className="stepper-step"
                disabled={!autoOn}
                aria-label="Уменьшить авто-вывод"
                onClick={() => setAutoValue(auto - AUTO_STEP)}
              >
                –
              </button>
              <label className="stepper-value">
                <span className="stepper-x">x</span>
                <input
                  inputMode="decimal"
                  enterKeyHint="done"
                  disabled={!autoOn}
                  aria-label="Авто вывод"
                  value={autoText}
                  onChange={(e) => setAutoText(e.target.value.replace(/[^\d.,]/g, ""))}
                  onBlur={() => setAutoValue(parseAuto(autoText, auto))}
                />
              </label>
              <button
                type="button"
                className="stepper-step"
                disabled={!autoOn}
                aria-label="Увеличить авто-вывод"
                onClick={() => setAutoValue(auto + AUTO_STEP)}
              >
                +
              </button>
            </div>
          </div>

          <button type="button" className="sheet-cta" disabled={!canSubmit} onClick={submit}>
            {short ? "Недостаточно звёзд" : tooSmall ? `Минимальная сумма — ${minBet}` : "Сделать ставку"}
          </button>
        </div>
      </div>
    </div>
  );
}

/** The sum itself: a bare field that grows with the number typed into it. */
function AmountInput({
  text,
  onText,
  maxBet,
}: {
  text: string;
  onText: (next: string) => void;
  maxBet: number;
}) {
  const ref = useRef<HTMLInputElement>(null);

  // The sheet is opened to be typed into, so it starts focused -- but only on
  // mount, or every re-render would drag the caret back to the end.
  useEffect(() => ref.current?.focus(), []);

  return (
    <input
      ref={ref}
      inputMode="numeric"
      pattern="[0-9]*"
      autoComplete="off"
      placeholder="0"
      className={text ? "amount-input" : "amount-input amount-input--empty"}
      style={{ width: `${Math.max(1, text.length)}ch` }}
      value={text}
      onChange={(e) => {
        const digits = e.target.value.replace(/\D/g, "").replace(/^0+(?=\d)/, "");
        if (Number(digits) > maxBet) return;
        onText(digits);
      }}
    />
  );
}

function formatAuto(hundredths: number): string {
  return (hundredths / 100).toFixed(1);
}

/** Reads the spinner's field back, falling back to the last good value. */
function parseAuto(text: string, fallback: number): number {
  const value = Number(text.replace(",", "."));
  if (!Number.isFinite(value) || value <= 0) return fallback;
  return Math.round(value * 100);
}
