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
  const flying = phase === "flying";

  return (
    <div className="history" role="list">
      {/* Between rounds the head chip says what is happening rather than
          repeating a number the strip already carries. In flight the number
          sits in a fixed-width slot, so the chip stops resizing on every
          hundredth. */}
      <span role="listitem" className="chip chip--live">
        {flying ? <span className="chip-slot">x{format(current)}</span> : "Ожидание раунда"}
      </span>
      {history.map((value, i) => (
        <span role="listitem" key={`${i}-${value}`} className="chip">
          {format(value)}
        </span>
      ))}
    </div>
  );
});
