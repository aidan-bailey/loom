// The ask commands: answer a question, chat about it, allow or deny a permission, each keyed by
// its ask's id so two asks never hold each other (spec Part 2 decision 7), with the classic
// app's sheets and words (asks.mjs). `allowArgs` and `answerBody` are the pure halves the
// permission card and the question form call: what an Allow sends, and what Answer sends and when
// it is on.
import type { AnswerBody, Ask, QuestionAnswerBody } from "@/api/gen";
import { api } from "@/api/request";
import { defineCommand, type CommandContext } from "@/app/commands";
import { showOutcome } from "@/app/toasts";
import { MAX_MESSAGE_BYTES, utf8Length } from "@/lib/utf8";
import { useDrafts, type AskDraft } from "@/store/drafts";

import { requireSession } from "./session";

// AllowArgs is what `ask.allow` takes: the ask, which suggestion (0 for the plain Allow, else
// 1-based), its label for the sheet, and whether Claude would save it as a rule.
export interface AllowArgs {
  ask: string;
  suggestion: number;
  label: string;
  persists: boolean;
}

// allowArgs is the args of the permission card's Allow button for `suggestion`: 0 is the plain
// Allow, which saves nothing; n >= 1 is the ask's n-th suggestion, which persists only when that
// suggestion says so. A suggestion the ask does not hold is an error, never another's rule.
export function allowArgs(ask: Ask, suggestion: number): AllowArgs {
  if (suggestion === 0) return { ask: ask.id, suggestion: 0, label: "Allow", persists: false };
  const chosen = ask.suggestions?.[suggestion - 1];
  if (chosen === undefined) throw new Error(`loom: ask ${ask.id} has no suggestion ${suggestion}`);
  return { ask: ask.id, suggestion, label: chosen.label, persists: chosen.persists === true };
}

// answerBody is the body the question form's Answer sends, or null while Answer is off: until
// every question has a picked label or an Other text, and while an Other text or a note that
// would be sent passes the daemon's limit in UTF-8 bytes. Other and note are trimmed and left out
// when empty, and a single-select question carries one answer, its last picked label or else its
// Other text, never both. The limit is checked on that sent text, as the daemon checks it: not on
// whitespace trimmed away, and not on an Other text a single-select question's label drops.
export function answerBody(ask: Ask, draft: AskDraft | undefined): AnswerBody | null {
  const questions = ask.questions ?? [];
  if (questions.length === 0 || draft === undefined) return null;
  const answers: Record<string, QuestionAnswerBody> = {};
  for (const question of questions) {
    const typed = draft.answers[question.question];
    if (typed === undefined) return null;
    let other = typed.other.trim();
    if (typed.labels.length === 0 && other === "") return null;
    let labels = typed.labels;
    if (!question.multi && labels.length > 0) {
      labels = labels.slice(-1);
      other = "";
    }
    const note = typed.note.trim();
    if (utf8Length(other) > MAX_MESSAGE_BYTES || utf8Length(note) > MAX_MESSAGE_BYTES) return null;
    const answer: QuestionAnswerBody = { labels };
    if (other !== "") answer.other = other;
    if (note !== "") answer.note = note;
    answers[question.question] = answer;
  }
  return { answers };
}

// answer sends body as the answer to the ask and reports whether the daemon answered 200: a 202
// leaves the ask on screen until the stream says it was answered, so nothing is dropped for it.
async function answer(ctx: CommandContext, ask: string, body: AnswerBody, what: string) {
  const outcome = await api.answer(requireSession(ctx).id, ask, body);
  showOutcome(outcome, what);
  return outcome.kind === "ok";
}

defineCommand<{ ask: string; body: AnswerBody }>({
  id: "ask.answer",
  title: "Answer",
  key: (args) => args.ask,
  when: (ctx) => ctx.session !== undefined,
  run: async (ctx, args) => {
    if (await answer(ctx, args.ask, args.body, "Answer")) useDrafts.getState().dropAsk(args.ask);
  },
});

const TOO_LONG = "Too long: a message is at most 64 KiB.";

defineCommand<{ ask: string }>({
  id: "ask.chat",
  title: "Chat about this",
  key: (args) => args.ask,
  when: (ctx) => ctx.session !== undefined,
  confirm: () => ({
    title: "Chat about this",
    body: "Claude puts the questions aside and asks what you want to clarify.",
    action: "Send",
    destructive: false,
    field: {
      kind: "text",
      label: "What to clarify",
      placeholder: "What to clarify (optional)",
      max: MAX_MESSAGE_BYTES,
      tooLong: TOO_LONG,
    },
  }),
  run: async (ctx, args, input) => {
    const body: AnswerBody = { chat: true, chat_text: input.trim() };
    if (await answer(ctx, args.ask, body, "Chat")) useDrafts.getState().dropAsk(args.ask);
  },
});

defineCommand<AllowArgs>({
  id: "ask.allow",
  title: "Allow",
  key: (args) => args.ask,
  when: (ctx) => ctx.session !== undefined,
  // Only a rule Claude would save asks first: the plain Allow and a one-time suggestion go at once.
  confirm: (ctx, args) =>
    args.persists
      ? {
          title: "Save this rule?",
          body: `${args.label}.\n\nClaude writes it to a settings file, so it lasts past this session.`,
          action: "Allow and save",
          destructive: false,
        }
      : null,
  run: async (ctx, args) => {
    const body: AnswerBody =
      args.suggestion > 0 ? { allow: true, suggestion: args.suggestion } : { allow: true };
    await answer(ctx, args.ask, body, "Allow");
  },
});

defineCommand<{ ask: string }>({
  id: "ask.deny",
  title: "Deny…",
  key: (args) => args.ask,
  when: (ctx) => ctx.session !== undefined,
  confirm: () => ({
    title: "Deny this?",
    body: 'Claude stops this turn, as "No" does in the terminal.',
    action: "Deny",
    destructive: true,
    field: {
      kind: "text",
      label: "What Claude should do instead",
      placeholder: "What Claude should do instead (optional)",
      max: MAX_MESSAGE_BYTES,
      tooLong: TOO_LONG,
    },
  }),
  run: async (ctx, args, input) => {
    await answer(ctx, args.ask, { allow: false, message: input.trim() }, "Deny");
  },
});
