import { memo } from "react";

import { format } from "../lib/multiplier";
import type { Phase } from "../lib/types";

interface Props {
  history: number[];
  phase: Phase;
  current: number;
}

/** Recent burst points, newest first, with the live round pinned at the head. */
export const HistoryStrip = memo(function HistoryStrip({ history, phase, current }: Props) {
  return (
    <div className="history" role="list">
      {/* Between rounds the head chip says what is happening rather than
          repeating a number the strip already carries. */}
      <span role="listitem" className="chip chip--live">
        {phase === "flying" ? `x${format(current)}` : "Ожидание раунда"}
      </span>
      {history.map((value, i) => (
        <span
          role="listitem"
          key={`${i}-${value}`}
          className={value >= 200 ? "chip chip--high" : "chip"}
        >
          {format(value)}
        </span>
      ))}
    </div>
  );
});
