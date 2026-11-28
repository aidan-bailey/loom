// The flow board (a chunk of its own): every flow of the shown workspaces in its stage's column,
// drawn from the fleet store, so it follows the stream and sends no read of its own. On desktop a
// card links to the board with its own `session` and `plan`, which the shell's preview pane
// (desktop/FlowPreview.tsx) shows beside it; on the phone it opens the flow.
import { useEffect, useMemo, useState, type ReactElement } from "react";

import type { PanelProps } from "@/app/panels";
import { hrefFor } from "@/app/routes";
import { useLayout } from "@/app/viewport";
import { boardOf, cardLine, FINISHED, type BoardCard, type BoardColumn } from "@/fleet/board";
import { cn } from "@/lib/utils";
import { useFleet, useLink } from "@/store/fleet";

import { ago, flowTone, usd } from "../fleet/SessionRow";

const LEGEND = "text-[11px] tracking-wide text-muted-foreground uppercase";
const BORDER = { attention: "border-attention", error: "border-error", docs: "border-border" };

// Where a card leads: the board's preview of it on desktop, its flow on the phone.
export function cardHref(card: BoardCard, phone: boolean): string {
  const plan = card.flow.plan;
  return phone
    ? hrefFor("flow", { id: String(card.session) }, { plan })
    : hrefFor("flowBoard", {}, { session: String(card.session), plan });
}

interface CardProps {
  card: BoardCard;
  phone: boolean;
  previewed: boolean;
}

function Card({ card, phone, previewed }: CardProps): ReactElement {
  const flow = card.flow;
  const tone = flowTone(flow);
  const line = cardLine(flow);
  const facts = [typeof flow.spent === "number" ? usd(flow.spent) : "", ago(flow.updated)];
  return (
    <a
      href={cardHref(card, phone)}
      data-tone={tone}
      aria-current={previewed ? "true" : undefined}
      className={cn(
        "flex min-h-[44px] flex-col gap-0.5 rounded-lg border bg-background p-2",
        BORDER[tone],
        previewed && "ring-2 ring-ring",
      )}
    >
      <span className="block truncate font-semibold">{card.title}</span>
      {card.repo !== "" && (
        <span className="block truncate text-xs text-muted-foreground">{card.repo}</span>
      )}
      {flow.part !== "" && (
        <span className="block truncate text-xs text-muted-foreground">{flow.part}</span>
      )}
      {line !== "" && <span className="block truncate text-sm">{line}</span>}
      {flow.asks !== "" && (
        <span className="block truncate text-xs text-attention">{flow.asks}</span>
      )}
      {flow.unreadable !== "" && <span className="block text-xs text-error">unreadable</span>}
      <span className="block truncate text-xs text-muted-foreground">
        {facts.filter((f) => f !== "").join(" · ")}
      </span>
    </a>
  );
}

interface ColumnProps {
  column: BoardColumn;
  phone: boolean;
  // previewed says whether a card is the one the desktop's preview shows.
  previewed: (card: BoardCard) => boolean;
}

function Column({ column, phone, previewed }: ColumnProps): ReactElement {
  return (
    <div
      role="group"
      aria-label={column.name}
      className={cn(
        "flex min-w-0 flex-col gap-2 rounded-xl border border-border bg-card p-2",
        column.name === FINISHED && "opacity-60",
      )}
    >
      <p className={LEGEND}>
        {column.name} · {column.cards.length}
      </p>
      {column.cards.map((card) => (
        <Card
          key={`${card.session}:${card.flow.plan}`}
          card={card}
          phone={phone}
          previewed={previewed(card)}
        />
      ))}
    </div>
  );
}

export default function FlowBoardBody(props: PanelProps): ReactElement {
  const snapshot = useFleet((s) => s.snapshot);
  const { stale } = useLink();
  const phone = useLayout() === "phone";
  const [, setTick] = useState(0);

  // Ages move with the clock, not only with the stream.
  useEffect(() => {
    const t = setInterval(() => setTick((n) => n + 1), 30_000);
    return () => clearInterval(t);
  }, []);

  const columns = useMemo(() => (snapshot === null ? null : boardOf(snapshot)), [snapshot]);
  if (columns === null) {
    return (
      <p role="status" className="px-4 py-6 text-center text-muted-foreground">
        Loading…
      </p>
    );
  }
  if (columns.every((c) => c.cards.length === 0)) {
    return (
      <p className="px-4 py-6 text-center text-sm text-faint">
        No session&apos;s worktree holds a flow.
      </p>
    );
  }
  // The phone stacks the columns and leaves the empty ones out; a desktop keeps the board's shape.
  const drawn = phone ? columns.filter((c) => c.cards.length > 0) : columns;
  const { session = "", plan = "" } = props.search;
  const previewed = (card: BoardCard): boolean =>
    !phone && session !== "" && String(card.session) === session && card.flow.plan === plan;
  return (
    <section
      aria-label="Flow board"
      className={cn(
        "px-4 pb-6",
        phone
          ? "flex flex-col gap-4"
          : "grid auto-cols-[minmax(9rem,1fr)] grid-flow-col items-start gap-3 overflow-x-auto",
        stale && "opacity-50",
      )}
    >
      {drawn.map((column) => (
        <Column key={column.name} column={column} phone={phone} previewed={previewed} />
      ))}
    </section>
  );
}
