import type { ReactElement } from "react";

import type { Ask, Session } from "@/api/gen";
import { CommandButton } from "@/app/CommandButton";
import { allowArgs } from "@/commands/asks";

const MONO_BLOCK =
  "overflow-x-auto whitespace-pre-wrap rounded-md bg-background p-2 font-mono text-[12.5px]";

// Asks draws a card per ask the session holds, in order: a question form, a permission card, or a
// neutral card for a kind this build does not know. Every send is a command, so the confirmation,
// the in-flight key, the stale gate and the toasts are the command path's.
export function Asks(props: { session: Session }): ReactElement {
  const asks = props.session.asks ?? [];
  if (asks.length === 0) return <></>;
  return (
    <div className="flex flex-col gap-3">
      {asks.map((ask) => (
        <AskCard key={ask.id} ask={ask} />
      ))}
    </div>
  );
}

function AskCard({ ask }: { ask: Ask }): ReactElement {
  return (
    <div data-ask={ask.id} className="rounded-lg border border-border bg-card p-3">
      {ask.agent !== "" ? (
        <p className="text-[12px] text-muted-foreground">asked by {ask.agent}</p>
      ) : null}
      {ask.kind === "question" ? (
        <UnknownAsk kind="question" />
      ) : ask.kind === "permission" ? (
        <PermissionAsk ask={ask} />
      ) : (
        <UnknownAsk kind={String(ask.kind)} />
      )}
      {ask.answering ? <p className="text-[12px] text-muted-foreground">Sending…</p> : null}
    </div>
  );
}

function PermissionAsk({ ask }: { ask: Ask }): ReactElement {
  return (
    <div className="flex flex-col gap-2">
      <div className="font-mono text-[13px]">{ask.tool}</div>
      {(ask.input ?? []).map((block, i) => (
        <div key={i} className="flex flex-col gap-1">
          <div className="text-[12px] text-muted-foreground">
            {block.label}
            {block.cut ? " (cut)" : ""}
          </div>
          {block.mono ? (
            <pre className={MONO_BLOCK}>{block.text}</pre>
          ) : (
            <p className="text-[14px]">{block.text}</p>
          )}
        </div>
      ))}
      <div className="flex flex-col gap-2 [&>button]:w-full">
        <CommandButton
          id="ask.allow"
          args={allowArgs(ask, 0)}
          label="Allow"
          disabled={ask.answering}
        />
        {(ask.suggestions ?? []).map((g, i) => (
          <CommandButton
            key={i}
            id="ask.allow"
            args={allowArgs(ask, i + 1)}
            label={g.label}
            variant="secondary"
            disabled={ask.answering}
          />
        ))}
        <CommandButton
          id="ask.deny"
          args={{ ask: ask.id }}
          label="Deny…"
          variant="destructive"
          disabled={ask.answering}
        />
      </div>
    </div>
  );
}

function UnknownAsk({ kind }: { kind: string }): ReactElement {
  return (
    <p className="text-[13px] text-muted-foreground">
      <span className="font-mono">{kind}</span> This kind of ask is answered in Claude.
    </p>
  );
}
