import { useState, type ReactElement } from "react";
import type { Item as ItemData, Turn } from "@/api/gen";
import { cn } from "@/lib/utils";

const FOLD_LINES = 12;

/** turnLabel is a turn's label by who it came from. */
export function turnLabel(turn: Turn): string {
  const name = turn.name.toUpperCase();
  switch (turn.from) {
    case "you":
      return "YOU";
    case "plugin":
      return name === "" ? "PLUGIN" : name;
    case "peer":
      return name === "" ? "FROM ANOTHER SESSION" : `FROM ${name}`;
    case "task":
      return "BACKGROUND TASK";
    default:
      return String(turn.from).toUpperCase();
  }
}

/** clock is a time as 24-hour hh:mm in the device's zone, "" for none or one that does not parse. */
export function clock(iso: string): string {
  if (iso === "") return "";
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "";
  const d = new Date(t);
  const two = (n: number): string => String(n).padStart(2, "0");
  return `${two(d.getHours())}:${two(d.getMinutes())}`;
}

function Prompt({ turn }: { turn: Turn }): ReactElement {
  const [open, setOpen] = useState(false);
  const lines = turn.text.split("\n");
  const folded = lines.length > FOLD_LINES && !open;
  const shown = folded ? lines.slice(0, FOLD_LINES).join("\n") : turn.text;
  const at = clock(turn.at);
  return (
    <fieldset className="rounded-lg border border-border px-3 pb-2">
      <legend className="px-1 text-[11px] text-faint">
        {turnLabel(turn) + (at === "" ? "" : ` · ${at}`)}
      </legend>
      <div className={cn("text-sm break-words whitespace-pre-wrap", turn.command && "font-mono")}>
        {turn.cut && !folded ? shown + "…" : shown}
      </div>
      {folded ? (
        <button
          type="button"
          className="min-h-11 text-[12.5px] text-muted-foreground"
          onClick={() => setOpen(true)}
        >
          {`▸ ${lines.length - FOLD_LINES} more lines`}
        </button>
      ) : null}
    </fieldset>
  );
}

/**
 * Item is one item of the Claude side of a turn. This is a temporary form: every item draws as the
 * neutral unknown-kind line, and Task 5.4 replaces it with the kinds.
 */
function Item(props: {
  item: ItemData;
  live: boolean;
  sessionId: number;
  remote: string;
}): ReactElement {
  const { item } = props;
  return (
    <div className="text-muted-foreground">
      <span className="font-mono">{item.kind}</span>
      {item.text !== undefined && item.text !== "" ? ` ${item.text}` : null}
    </div>
  );
}

function ClaudeSide(props: {
  turn: Turn;
  live: boolean;
  sessionId: number;
  remote: string;
}): ReactElement | null {
  const items = props.turn.items ?? [];
  if (items.length === 0) return null;
  const said = items.some((i) => i.kind !== "compacted");
  const at = clock(items[items.length - 1]?.at ?? "");
  const label = props.live ? "CLAUDE · ◐ WORKING" : at === "" ? "CLAUDE" : `CLAUDE · ${at}`;
  return (
    <div className="mt-2">
      {said ? <div className="text-[11px] text-faint">{label}</div> : null}
      {items.map((item, i) => (
        <Item
          key={i}
          item={item}
          live={props.live}
          sessionId={props.sessionId}
          remote={props.remote}
        />
      ))}
    </div>
  );
}

/**
 * TurnList is a conversation's turns, oldest first, as the classic app draws them: each turn's
 * prompt in a box and Claude's items below it. live makes only the last turn live.
 */
export function TurnList(props: {
  turns: Turn[];
  sessionId: number;
  live: boolean;
  remote: string;
}): ReactElement {
  return (
    <div>
      {props.turns.map((turn, i) => (
        <article key={turn.id} className="mb-4">
          {turn.partial ? (
            <p className="text-[11px] text-muted-foreground">Its start is earlier.</p>
          ) : (
            <Prompt turn={turn} />
          )}
          <ClaudeSide
            turn={turn}
            live={props.live && i === props.turns.length - 1}
            sessionId={props.sessionId}
            remote={props.remote}
          />
        </article>
      ))}
    </div>
  );
}
