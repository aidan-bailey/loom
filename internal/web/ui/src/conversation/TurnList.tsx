import { useState, type ReactElement } from "react";
import type { Item as ItemData, ItemKind, Step, Turn } from "@/api/gen";
import { AGENT_ID, hrefFor } from "@/app/routes";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { cn } from "@/lib/utils";
import { ago } from "@/panels/fleet/SessionRow";
import { Markdown } from "./Markdown";

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

/** useFold is a fold's open state: the reader's choice once made, else auto. */
function useFold(auto: boolean): [boolean, (open: boolean) => void] {
  const [chosen, setChosen] = useState<boolean | undefined>(undefined);
  return [chosen ?? auto, setChosen];
}

function Thinking(props: { item: ItemData; auto: boolean }): ReactElement {
  const [open, setOpen] = useFold(props.auto);
  const text = props.item.text ?? "";
  const first = text.split("\n").find((l) => l.trim() !== "") ?? "";
  return (
    <Collapsible open={open} onOpenChange={setOpen}>
      <CollapsibleTrigger className="min-h-11 w-full text-left text-[12.5px] text-muted-foreground">
        {`▸ thinking: ${first}`}
      </CollapsibleTrigger>
      <CollapsibleContent>
        <div className="text-[12.5px] break-words whitespace-pre-wrap text-muted-foreground">
          {props.item.truncated === true ? text + "…" : text}
        </div>
      </CollapsibleContent>
    </Collapsible>
  );
}

function StepRow(props: { step: Step; live: boolean; sessionId: number }): ReactElement {
  const { step } = props;
  const agent = step.agent ?? "";
  const linked = agent !== "" && AGENT_ID.test(agent);
  const running = props.live && step.state === "pending";
  let glyph: ReactElement | null = null;
  if (running || (linked && !step.finished)) {
    glyph = <span className="text-turn">◐</span>;
  } else if (step.state === "failed") {
    glyph = <span className="text-error">×</span>;
  } else if (step.state === "interrupted") {
    glyph = <span className="text-muted-foreground">×</span>;
  }
  let end = "";
  if (running) end = `running ${ago(step.at)}`;
  else if (step.state === "pending") end = "no result";
  if (linked) end = end === "" ? "›" : `${end} ›`;
  const cells = (
    <>
      <span className="w-4">{glyph}</span>
      <span className="font-mono text-muted-foreground">{step.tool}</span>
      <span className="min-w-0 truncate">{step.summary}</span>
      <span className="text-muted-foreground">{end}</span>
    </>
  );
  const grid = "grid grid-cols-[1rem_auto_1fr_auto] items-baseline gap-2 text-[12.5px]";
  if (linked) {
    return (
      <a
        className={cn(grid, "min-h-11 items-center")}
        href={hrefFor("agent", { id: String(props.sessionId), agent })}
      >
        {cells}
      </a>
    );
  }
  return <div className={grid}>{cells}</div>;
}

function Steps(props: {
  item: ItemData;
  live: boolean;
  auto: boolean;
  sessionId: number;
}): ReactElement {
  const [open, setOpen] = useFold(props.auto);
  const steps = props.item.steps ?? [];
  const total = props.item.total ?? 0;
  const failed = props.item.failed ?? 0;
  const hidden = total - steps.length;
  return (
    <Collapsible open={open} onOpenChange={setOpen}>
      <CollapsibleTrigger className="min-h-11 w-full text-left text-[12.5px] text-muted-foreground">
        {`${total} ${total === 1 ? "step" : "steps"}`}
        {failed > 0 ? <span className="text-error">{` · ${failed} failed`}</span> : null}
      </CollapsibleTrigger>
      <CollapsibleContent>
        {hidden > 0 ? (
          <div className="text-[11px] text-muted-foreground">{`${hidden} earlier steps not shown`}</div>
        ) : null}
        {steps.map((step, i) => (
          <StepRow key={i} step={step} live={props.live} sessionId={props.sessionId} />
        ))}
      </CollapsibleContent>
    </Collapsible>
  );
}

/** Item is one item of the Claude side of a turn, drawn by its kind; an unknown kind is neutral. */
function Item(props: {
  item: ItemData;
  live: boolean;
  sessionId: number;
  remote: string;
  autoOpen: boolean;
}): ReactElement {
  const { item } = props;
  switch (item.kind) {
    case "text":
      return (
        <div className="text-sm">
          <Markdown blocks={item.blocks ?? []} />
          {item.truncated === true ? (
            <p className="text-[11px] text-muted-foreground">
              {"… continues in "}
              {props.remote === "" ? (
                "Claude"
              ) : (
                <a href={props.remote} target="_blank" rel="noopener noreferrer">
                  Claude
                </a>
              )}
            </p>
          ) : null}
        </div>
      );
    case "thinking":
      return <Thinking item={item} auto={props.autoOpen} />;
    case "steps":
      return (
        <Steps item={item} live={props.live} auto={props.autoOpen} sessionId={props.sessionId} />
      );
    case "compacted": {
      const at = clock(item.at);
      return (
        <div className="my-2 text-center text-[11px] text-muted-foreground">
          {at === "" ? "COMPACTED" : `COMPACTED ${at}`}
        </div>
      );
    }
    case "note":
      return item.error === true ? (
        <div className="text-[12.5px] text-error">{item.text}</div>
      ) : (
        <div className="text-[12.5px] text-muted-foreground">{`× ${item.text ?? ""}`}</div>
      );
    default: {
      const text = item.text ?? "";
      return (
        <div className="text-[12.5px] text-muted-foreground">
          <span className="font-mono">{String(item.kind)}</span>
          {text === "" ? null : ` ${text}`}
        </div>
      );
    }
  }
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
  const lastOf = (kind: ItemKind): number => items.map((i) => i.kind).lastIndexOf(kind);
  const lastSteps = lastOf("steps");
  const lastThinking = lastOf("thinking");
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
          autoOpen={props.live && (i === lastSteps || i === lastThinking)}
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
