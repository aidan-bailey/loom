// The session's commands: send, pause, resume, push, merge and kill, with the classic app's sheets
// and words (session.mjs) and its availability, `sessionActions`. A `when` here reads the
// session's status and group and nothing else (spec Part 2 decision 2); the daemon's admit stays
// the authority and a refusal comes back as a toast.
import type { Session } from "@/api/gen";
import { api } from "@/api/request";
import { defineCommand, type CommandContext } from "@/app/commands";
import type { ChoiceOption } from "@/app/ConfirmSheet";
import { hrefFor } from "@/app/routes";
import { showMessage, showOutcome } from "@/app/toasts";
import { allSessions } from "@/fleet/groups";
import { useDrafts } from "@/store/drafts";
import { useFleet } from "@/store/fleet";

// SessionActions says which of a session's actions are on offer: the classic `actionsFor`. `diff`
// is the changes tab: ReadDiff refuses a busy session and one in the paused group.
export interface SessionActions {
  pause: boolean;
  resume: boolean;
  push: boolean;
  merge: boolean;
  send: boolean;
  diff: boolean;
  kill: boolean;
}

// sessionActions follows the session's status and group. Busy is Loading or Deleting; paused is
// the group, which holds the Recoverable and unstarted Ready sessions that status alone cannot
// tell from a started Ready. Pause keeps the worktree, so push and merge work on a Paused
// session, but a Recoverable or unstarted one may have no worktree to work on.
export function sessionActions(session: Session): SessionActions {
  const busy = session.status === "Loading" || session.status === "Deleting";
  const paused = session.group === "paused";
  const tree = !paused || session.status === "Paused";
  return {
    pause: !busy && !paused,
    resume: session.status === "Paused",
    push: !busy && tree,
    merge: !busy && tree,
    send: !busy && !paused,
    diff: !busy && !paused,
    kill: !busy,
  };
}

// requireSession is the session a command acts on: every command's `when` needs one, so none is
// a bug, which runCommand shows as an error toast.
export function requireSession(ctx: CommandContext): Session {
  if (ctx.session === undefined) throw new Error("loom: this action needs a session on screen");
  return ctx.session;
}

// offers is a session command's `when`: a session is on screen and `sessionActions` offers the action.
function offers(action: keyof SessionActions): (ctx: CommandContext) => boolean {
  return (ctx) => ctx.session !== undefined && sessionActions(ctx.session)[action];
}

defineCommand<{ text: string }>({
  id: "session.send",
  title: "Send",
  when: offers("send"),
  run: async (ctx, args) => {
    const session = requireSession(ctx);
    const text = args.text.trim();
    const outcome = await api.send(session.id, { text });
    showOutcome(outcome, "Send");
    if (outcome.kind !== "ok") return;
    // What was typed while the send was in flight is a new draft: only the text sent is dropped.
    const drafts = useDrafts.getState();
    if ((drafts.replies[session.id] ?? "").trim() === text) drafts.setReply(session.id, "");
  },
});

defineCommand({
  id: "session.pause",
  title: "Pause",
  palette: true,
  when: offers("pause"),
  run: async (ctx) => {
    showOutcome(await api.pause(requireSession(ctx).id), "Pause");
  },
});

defineCommand({
  id: "session.resume",
  title: "Resume",
  palette: true,
  when: offers("resume"),
  run: async (ctx) => {
    showOutcome(await api.resume(requireSession(ctx).id), "Resume");
  },
});

defineCommand({
  id: "session.push",
  title: "Push",
  palette: true,
  when: offers("push"),
  confirm: (ctx) => ({
    title: "Push this branch?",
    body: `This commits the session's uncommitted changes first, then pushes ${requireSession(ctx).branch}.`,
    action: "Commit and push",
    destructive: false,
  }),
  run: async (ctx) => {
    showOutcome(await api.push(requireSession(ctx).id), "Push");
  },
});

// mergeSources are the other sessions of the session's workspace, whose branch can be merged into
// this one, in the snapshot's order.
function mergeSources(session: Session): ChoiceOption[] {
  const snapshot = useFleet.getState().snapshot;
  if (snapshot === null) return [];
  return allSessions(snapshot)
    .filter((other) => other.workspace === session.workspace && other.id !== session.id)
    .map((other) => ({ value: String(other.id), label: `${other.title} (${other.branch})` }));
}

defineCommand({
  id: "session.merge",
  title: "Merge",
  palette: true,
  when: offers("merge"),
  // No sheet when there is nothing to pick: run then says so.
  confirm: (ctx) => {
    const session = requireSession(ctx);
    const options = mergeSources(session);
    if (options.length === 0) return null;
    return {
      title: "Merge a branch in",
      body: `Merge which session's branch into ${session.branch}?`,
      action: "Merge",
      destructive: false,
      field: { kind: "choice", label: "Session to merge in", options },
    };
  },
  run: async (ctx, args, input) => {
    const session = requireSession(ctx);
    if (input === "") {
      showMessage("No other session in this workspace to merge in.", true);
      return;
    }
    showOutcome(await api.merge(session.id, { source: Number(input) }), "Merge");
  },
});

// killBody is Kill's sheet: what it ends, removes and deletes, as the classic app words it.
function killBody(session: Session): string {
  const tmux = session.tmux
    ? `It ends tmux session ${session.tmux}.`
    : "It ends the session's tmux session.";
  const tree = session.worktree
    ? `It force-removes worktree ${session.worktree}`
    : "It force-removes the session's worktree";
  const paragraphs = [
    tmux,
    `${tree}, discarding its uncommitted changes. Push first to keep them.`,
  ];
  if (session.status === "Paused") {
    paragraphs.push(
      "If an older loom paused it, its worktree is gone and its changes may be in a stash, which Kill drops.",
    );
  }
  paragraphs.push(
    `It deletes branch ${session.branch} unless the session checked out an existing branch.`,
  );
  return paragraphs.join("\n\n");
}

defineCommand({
  id: "session.kill",
  title: "Kill",
  palette: true,
  when: offers("kill"),
  confirm: (ctx) => ({
    title: "Kill this session?",
    body: killBody(requireSession(ctx)),
    action: "Kill",
    destructive: true,
  }),
  run: async (ctx) => {
    const outcome = await api.kill(requireSession(ctx).id);
    showOutcome(outcome, "Kill");
    // The session is gone only when the daemon answered 200; a 202 or a refusal leaves it here.
    if (outcome.kind === "ok") window.location.assign(hrefFor("fleet"));
  },
});
