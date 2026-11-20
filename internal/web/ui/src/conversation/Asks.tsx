import type { ReactElement } from "react";

import type { Ask, Session } from "@/api/gen";
import { CommandButton } from "@/app/CommandButton";
import { Badge } from "@/components/ui/badge";
import { allowArgs, answerBody } from "@/commands/asks";
import { MAX_MESSAGE_BYTES, utf8Length } from "@/lib/utf8";
import { useDrafts, type AskDraft, type QuestionDraft } from "@/store/drafts";
import { useLink } from "@/store/fleet";

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
  // off is for the inputs; a button's own off state is the command path's stale gate and `disabled`.
  const { stale } = useLink();
  const off = ask.answering || stale;
  return (
    <div data-ask={ask.id} className="rounded-lg border border-border bg-card p-3">
      {ask.agent !== "" ? (
        <p className="text-[12px] text-muted-foreground">asked by {ask.agent}</p>
      ) : null}
      {ask.kind === "question" ? (
        <QuestionAsk ask={ask} off={off} />
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

const TEXT_INPUT =
  "min-h-11 w-full rounded-lg border border-input bg-background px-3 text-[16px] text-foreground outline-none placeholder:text-faint focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50";

const emptyAnswer = (): QuestionDraft => ({ labels: [], other: "", note: "" });

function emptyDraft(ask: Ask): AskDraft {
  const answers: Record<string, QuestionDraft> = {};
  for (const question of ask.questions ?? []) answers[question.question] = emptyAnswer();
  return { answers };
}

// change replaces one question's entry of the ask's stored draft and writes the whole draft: the
// form holds no state of its own, so the picks survive a tab switch.
function change(ask: Ask, text: string, edit: (answer: QuestionDraft) => QuestionDraft): void {
  const drafts = useDrafts.getState();
  const draft = drafts.asks[ask.id] ?? emptyDraft(ask);
  drafts.setAsk(ask.id, {
    answers: { ...draft.answers, [text]: edit(draft.answers[text] ?? emptyAnswer()) },
  });
}

function QuestionAsk({ ask, off }: { ask: Ask; off: boolean }): ReactElement {
  const stored = useDrafts((s) => s.asks[ask.id]);
  const draft = stored ?? emptyDraft(ask);
  const tooLong = Object.values(draft.answers).some(
    (a) => utf8Length(a.other) > MAX_MESSAGE_BYTES || utf8Length(a.note) > MAX_MESSAGE_BYTES,
  );
  const body = answerBody(ask, stored);
  return (
    <div className="flex flex-col gap-3">
      {(ask.questions ?? []).map((question) => {
        const answer = draft.answers[question.question] ?? emptyAnswer();
        const options = question.options ?? [];
        const previewed = [...answer.labels]
          .reverse()
          .map((label) => options.find((o) => o.label === label)?.preview)
          .find((preview) => preview !== undefined && preview !== "");
        return (
          <fieldset
            key={question.question}
            disabled={off}
            className="flex flex-col gap-2 border-0 p-0"
          >
            <legend className="mb-1 flex items-center gap-2 text-[14px]">
              <Badge variant="neutral">{question.header}</Badge>
              {question.question}
            </legend>
            {options.map((option) => (
              <label key={option.label} className="flex min-h-11 items-start gap-2">
                <input
                  type={question.multi ? "checkbox" : "radio"}
                  name={`${ask.id}\u0000${question.question}`}
                  aria-label={option.label}
                  checked={answer.labels.includes(option.label)}
                  onChange={() => {
                    change(ask, question.question, (a) => {
                      if (!question.multi) return { ...a, labels: [option.label], other: "" };
                      const labels = a.labels.includes(option.label)
                        ? a.labels.filter((l) => l !== option.label)
                        : [...a.labels, option.label];
                      return { ...a, labels };
                    });
                  }}
                  className="mt-1 accent-primary"
                />
                <span className="flex flex-col">
                  <span className="text-[14px]">{option.label}</span>
                  {option.description !== "" ? (
                    <span className="text-[12.5px] text-muted-foreground">
                      {option.description}
                    </span>
                  ) : null}
                </span>
              </label>
            ))}
            {previewed !== undefined ? <pre className={MONO_BLOCK}>{previewed}</pre> : null}
            <input
              type="text"
              placeholder="Other…"
              aria-label={`Other answer to ${question.question}`}
              value={answer.other}
              onChange={(event) => {
                const other = event.target.value;
                change(ask, question.question, (a) => ({
                  ...a,
                  other,
                  labels: !question.multi && other !== "" ? [] : a.labels,
                }));
              }}
              className={TEXT_INPUT}
            />
            <input
              type="text"
              placeholder="Note (optional)"
              aria-label={`Note on ${question.question}`}
              value={answer.note}
              onChange={(event) => {
                const note = event.target.value;
                change(ask, question.question, (a) => ({ ...a, note }));
              }}
              className={TEXT_INPUT}
            />
          </fieldset>
        );
      })}
      {tooLong ? (
        <p role="alert" className="text-[12px] text-error">
          Too long: an answer is at most 64 KiB.
        </p>
      ) : null}
      <div className="flex gap-2">
        <CommandButton
          id="ask.answer"
          args={{ ask: ask.id, body: body ?? {} }}
          label="Answer"
          disabled={ask.answering || body === null}
        />
        <CommandButton
          id="ask.chat"
          args={{ ask: ask.id }}
          label="Chat about this"
          variant="secondary"
          disabled={ask.answering}
        />
      </div>
    </div>
  );
}
