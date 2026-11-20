// The parts the flow board, the story view and the host's flows share. panels/flow/index.tsx
// must not import this file: it would pull these out of the lazy chunks into the entry script.
import { useEffect, useState, type ReactElement } from "react";

import type { Session } from "@/api/gen";

import { usd as figure } from "../fleet/SessionRow";

// planOf is the plan a flow screen reads: the one asked for, else the newest flow's, else none.
export function planOf(session: Session, plan: string): string {
  if (plan !== "") return plan;
  return session.flows?.[0]?.plan ?? "";
}

// usd words a figure as the fleet row does, and nothing for a value that is no number.
export function usd(value: number | null | undefined): string {
  return typeof value === "number" ? figure(value) : "";
}

// median is the middle of the sorted values, the mean of the two middle ones for an even count,
// 0 for none. It sorts a copy.
export function median(values: number[]): number {
  if (values.length === 0) return 0;
  const sorted = [...values].sort((a, b) => a - b);
  const mid = Math.floor(sorted.length / 2);
  if (sorted.length % 2 === 1) return sorted[mid] ?? 0;
  return ((sorted[mid - 1] ?? 0) + (sorted[mid] ?? 0)) / 2;
}

// Bars draws one bar a value, oldest first, then one more for the current one in the accent
// colour, each as an exact share of the tallest (5% at least). It draws only the bars: the
// caller draws a caption and draws nothing under 2 values.
export function Bars({
  values,
  current,
}: {
  values: number[];
  current: number | null;
}): ReactElement {
  const top = Math.max(0.0001, current ?? 0, ...values);
  const height = (value: number): string => `${Math.max(5, (value / top) * 100)}%`;
  return (
    <div aria-hidden="true" className="flex h-11 items-end gap-1">
      {values.map((value, i) => (
        <span
          key={i}
          data-slot="bar"
          className="w-4 bg-muted-foreground/40"
          style={{ height: height(value) }}
        />
      ))}
      {current !== null && (
        <span
          data-slot="bar"
          data-current="true"
          className="w-4 bg-turn"
          style={{ height: height(current) }}
        />
      )}
    </div>
  );
}

// clock reads this phone's time; kept apart from the component, which the purity lint holds to
// render the same for the same props.
function clock(): number {
  return Date.now();
}

// useClock is the time now, read again every ms.
export function useClock(ms: number): number {
  const [now, setNow] = useState(clock);
  useEffect(() => {
    const timer = setInterval(() => {
      setNow(clock());
    }, ms);
    return () => {
      clearInterval(timer);
    };
  }, [ms]);
  return now;
}
