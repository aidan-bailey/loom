import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Session } from "@/api/gen";
import { api } from "@/api/request";
import { commandEnabled, runCommand } from "@/app/commands";
import { askConfirm } from "@/app/ConfirmSheet";
import { useDrafts } from "@/store/drafts";

import { sessionActions, type SessionActions } from "./session";
import {
  askedSheet,
  command,
  context,
  makeSession,
  noticesOk,
  resetFleet,
  seed,
  spyOnAdd,
} from "./testkit";

vi.mock("@/app/ConfirmSheet", async (original) => ({
  ...(await original<typeof import("@/app/ConfirmSheet")>()),
  askConfirm: vi.fn(),
}));

let add: ReturnType<typeof spyOnAdd>;

beforeEach(() => {
  add = spyOnAdd();
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.mocked(askConfirm).mockReset();
  resetFleet();
  useDrafts.getState().clear();
});

const OFFERED: (keyof SessionActions)[] = [
  "pause",
  "resume",
  "push",
  "merge",
  "send",
  "diff",
  "kill",
];

const STATES: [Session["status"], Session["group"], (keyof SessionActions)[]][] = [
  ["Ready", "your_turn", ["pause", "push", "merge", "send", "diff", "kill"]],
  ["Running", "working", ["pause", "push", "merge", "send", "diff", "kill"]],
  ["Prompting", "needs_you", ["pause", "push", "merge", "send", "diff", "kill"]],
  ["Loading", "working", []],
  ["Deleting", "working", []],
  ["Loading", "paused", []],
  ["Paused", "paused", ["resume", "push", "merge", "kill"]],
  ["Recoverable", "paused", ["kill"]],
  ["Ready", "paused", ["kill"]],
];

describe("sessionActions", () => {
  it.each(STATES)("a %s session in group %s offers %j", (status, group, offered) => {
    const actions = sessionActions(makeSession({ status, group }));
    expect(OFFERED.filter((key) => actions[key])).toEqual(offered);
  });
});

const COMMANDS: [string, keyof SessionActions][] = [
  ["session.send", "send"],
  ["session.pause", "pause"],
  ["session.resume", "resume"],
  ["session.push", "push"],
  ["session.merge", "merge"],
  ["session.kill", "kill"],
];

describe("the session commands' availability", () => {
  it.each(COMMANDS)("%s is enabled exactly where sessionActions offers %s", (id, key) => {
    for (const [status, group] of STATES) {
      const session = makeSession({ status, group });
      expect(commandEnabled(command(id), context(session))).toBe(sessionActions(session)[key]);
    }
  });

  it.each(COMMANDS)("%s is off with no session and while the snapshot is stale", (id) => {
    const open =
      id === "session.resume" ? makeSession({ status: "Paused", group: "paused" }) : makeSession();
    expect(commandEnabled(command(id), context(open))).toBe(true);
    expect(commandEnabled(command(id), context(undefined))).toBe(false);
    expect(commandEnabled(command(id), context(open, { stale: true, link: "offline" }))).toBe(
      false,
    );
  });
});

describe("session.send", () => {
  it("sends the trimmed text and clears the session's draft, and no other session's", async () => {
    const send = vi.spyOn(api, "send").mockResolvedValue(noticesOk());
    useDrafts.getState().setReply(1, "  hello  ");
    useDrafts.getState().setReply(2, "keep me");

    await runCommand(command("session.send"), context(makeSession()), { text: "  hello  " });

    expect(send).toHaveBeenCalledOnce();
    expect(send).toHaveBeenCalledWith(1, { text: "hello" });
    expect(useDrafts.getState().replies).toStrictEqual({ 2: "keep me" });
  });

  it("keeps what was typed while the send was in flight", async () => {
    vi.spyOn(api, "send").mockImplementation(() => {
      useDrafts.getState().setReply(1, "hello, and one more thing");
      return Promise.resolve(noticesOk());
    });
    useDrafts.getState().setReply(1, "hello");

    await runCommand(command("session.send"), context(makeSession()), { text: "hello" });

    expect(useDrafts.getState().replies).toEqual({ 1: "hello, and one more thing" });
  });

  it("keeps the draft on a 202 and says the outcome will follow", async () => {
    vi.spyOn(api, "send").mockResolvedValue({ kind: "pending" });
    useDrafts.getState().setReply(1, "hello");

    await runCommand(command("session.send"), context(makeSession()), { text: "hello" });

    expect(useDrafts.getState().replies).toEqual({ 1: "hello" });
    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({
        description: "Send is still running. Its outcome will follow.",
        type: "info",
      }),
    );
  });

  it("keeps the draft and toasts the model's text when the send is refused", async () => {
    vi.spyOn(api, "send").mockResolvedValue({
      kind: "refused",
      status: 409,
      message: "session is busy",
    });
    useDrafts.getState().setReply(1, "hello");

    await runCommand(command("session.send"), context(makeSession()), { text: "hello" });

    expect(useDrafts.getState().replies).toEqual({ 1: "hello" });
    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({ description: "session is busy", type: "error" }),
    );
  });

  it("sends nothing from a session that cannot take a reply", async () => {
    const send = vi.spyOn(api, "send").mockResolvedValue(noticesOk());
    const paused = makeSession({ status: "Paused", group: "paused" });

    await runCommand(command("session.send"), context(paused), { text: "hello" });

    expect(send).not.toHaveBeenCalled();
  });
});

describe("session.pause and session.resume", () => {
  it("pauses the session with no sheet", async () => {
    const pause = vi.spyOn(api, "pause").mockResolvedValue(noticesOk());

    await runCommand(command("session.pause"), context(makeSession()), undefined);

    expect(askConfirm).not.toHaveBeenCalled();
    expect(pause).toHaveBeenCalledOnce();
    expect(pause).toHaveBeenCalledWith(1);
  });

  it("resumes a Paused session with no sheet", async () => {
    const resume = vi.spyOn(api, "resume").mockResolvedValue(noticesOk());
    const paused = makeSession({ status: "Paused", group: "paused" });

    await runCommand(command("session.resume"), context(paused), undefined);

    expect(askConfirm).not.toHaveBeenCalled();
    expect(resume).toHaveBeenCalledOnce();
    expect(resume).toHaveBeenCalledWith(1);
  });

  it("toasts a refusal in the model's words", async () => {
    vi.spyOn(api, "pause").mockResolvedValue({
      kind: "refused",
      status: 409,
      message: "session is not running",
    });

    await runCommand(command("session.pause"), context(makeSession()), undefined);

    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({ description: "session is not running", type: "error" }),
    );
  });

  it("does not resume a session that is not Paused", async () => {
    const resume = vi.spyOn(api, "resume").mockResolvedValue(noticesOk());
    const recoverable = makeSession({ status: "Recoverable", group: "paused" });

    await runCommand(command("session.resume"), context(recoverable), undefined);

    expect(resume).not.toHaveBeenCalled();
  });
});

describe("session.push", () => {
  it("asks first, in the classic words, and pushes on a yes", async () => {
    const session = makeSession({ branch: "feat/login" });
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue("");
    const push = vi.spyOn(api, "push").mockResolvedValue(noticesOk());

    await runCommand(command("session.push"), context(session), undefined);

    expect(askedSheet()).toEqual({
      title: "Push this branch?",
      body: "This commits the session's uncommitted changes first, then pushes feat/login.",
      action: "Commit and push",
      destructive: false,
    });
    expect(push).toHaveBeenCalledOnce();
    expect(push).toHaveBeenCalledWith(1);
  });

  it("pushes nothing when the sheet is cancelled", async () => {
    const session = makeSession();
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue(null);
    const push = vi.spyOn(api, "push").mockResolvedValue(noticesOk());

    await runCommand(command("session.push"), context(session), undefined);

    expect(push).not.toHaveBeenCalled();
  });
});

describe("session.kill", () => {
  const tree = {
    tmux: "loom_login",
    worktree: "/work/login",
    branch: "feat/login",
  };

  it("asks first, naming the tmux session, the worktree and the branch", async () => {
    const session = makeSession(tree);
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue(null);

    await runCommand(command("session.kill"), context(session), undefined);

    expect(askedSheet()).toEqual({
      title: "Kill this session?",
      body: [
        "It ends tmux session loom_login.",
        "It force-removes worktree /work/login, discarding its uncommitted changes. Push first to keep them.",
        "It deletes branch feat/login unless the session checked out an existing branch.",
      ].join("\n\n"),
      action: "Kill",
      destructive: true,
    });
  });

  it("speaks of the session's own tmux session and worktree when it has none recorded", async () => {
    const session = makeSession({ branch: "feat/login", tmux: undefined, worktree: undefined });
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue(null);

    await runCommand(command("session.kill"), context(session), undefined);

    expect(askedSheet().body).toBe(
      [
        "It ends the session's tmux session.",
        "It force-removes the session's worktree, discarding its uncommitted changes. Push first to keep them.",
        "It deletes branch feat/login unless the session checked out an existing branch.",
      ].join("\n\n"),
    );
  });

  it("adds the stash paragraph for a Paused session only", async () => {
    const paused = makeSession({ ...tree, status: "Paused", group: "paused" });
    seed([paused]);
    vi.mocked(askConfirm).mockResolvedValue(null);
    await runCommand(command("session.kill"), context(paused), undefined);
    const stash =
      "If an older loom paused it, its worktree is gone and its changes may be in a stash, which Kill drops.";
    expect(askedSheet().body).toContain(stash);

    vi.mocked(askConfirm).mockClear();
    const ready = makeSession(tree);
    seed([ready]);
    await runCommand(command("session.kill"), context(ready), undefined);
    expect(askedSheet().body).not.toContain("stash");
  });

  it("kills on a yes and opens the fleet when the daemon answers 200", async () => {
    const session = makeSession(tree);
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue("");
    const kill = vi.spyOn(api, "kill").mockResolvedValue(noticesOk());
    const assign = vi.spyOn(window.location, "assign").mockImplementation(() => undefined);

    await runCommand(command("session.kill"), context(session), undefined);

    expect(kill).toHaveBeenCalledOnce();
    expect(kill).toHaveBeenCalledWith(1);
    expect(assign).toHaveBeenCalledOnce();
    expect(assign).toHaveBeenCalledWith("#/");
  });

  it("kills nothing, and stays on the session, when the sheet is cancelled", async () => {
    const session = makeSession(tree);
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue(null);
    const kill = vi.spyOn(api, "kill").mockResolvedValue(noticesOk());
    const assign = vi.spyOn(window.location, "assign").mockImplementation(() => undefined);

    await runCommand(command("session.kill"), context(session), undefined);

    expect(kill).not.toHaveBeenCalled();
    expect(assign).not.toHaveBeenCalled();
  });

  it("stays on the session when the daemon answers 202, and says the outcome will follow", async () => {
    const session = makeSession(tree);
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue("");
    vi.spyOn(api, "kill").mockResolvedValue({ kind: "pending" });
    const assign = vi.spyOn(window.location, "assign").mockImplementation(() => undefined);

    await runCommand(command("session.kill"), context(session), undefined);

    expect(assign).not.toHaveBeenCalled();
    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({
        description: "Kill is still running. Its outcome will follow.",
        type: "info",
      }),
    );
  });

  it("stays on the session and toasts the model's text when the kill is refused", async () => {
    const session = makeSession(tree);
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue("");
    vi.spyOn(api, "kill").mockResolvedValue({
      kind: "refused",
      status: 409,
      message: "session is deleting",
    });
    const assign = vi.spyOn(window.location, "assign").mockImplementation(() => undefined);

    await runCommand(command("session.kill"), context(session), undefined);

    expect(assign).not.toHaveBeenCalled();
    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({ description: "session is deleting", type: "error" }),
    );
  });
});

describe("session.merge", () => {
  const self = makeSession({ id: 1, workspace: 1, branch: "feat/login" });
  const fixLogin = makeSession({ id: 2, workspace: 1, title: "Fix login", branch: "fix-login" });
  const elsewhere = makeSession({ id: 3, workspace: 2, title: "Elsewhere", branch: "elsewhere" });
  const docs = makeSession({ id: 4, workspace: 1, title: "Docs", branch: "docs" });

  it("offers the other sessions of its workspace, by title and branch, valued by id", async () => {
    seed([self, fixLogin, elsewhere, docs]);
    vi.mocked(askConfirm).mockResolvedValue(null);

    await runCommand(command("session.merge"), context(self), undefined);

    expect(askedSheet()).toEqual({
      title: "Merge a branch in",
      body: "Merge which session's branch into feat/login?",
      action: "Merge",
      destructive: false,
      field: {
        kind: "choice",
        label: "Session to merge in",
        options: [
          { value: "2", label: "Fix login (fix-login)" },
          { value: "4", label: "Docs (docs)" },
        ],
      },
    });
  });

  it("merges the session the sheet chose into this one", async () => {
    seed([self, fixLogin, elsewhere, docs]);
    vi.mocked(askConfirm).mockResolvedValue("4");
    const merge = vi.spyOn(api, "merge").mockResolvedValue(noticesOk());

    await runCommand(command("session.merge"), context(self), undefined);

    expect(merge).toHaveBeenCalledOnce();
    expect(merge).toHaveBeenCalledWith(1, { source: 4 });
  });

  it("merges nothing when the sheet is cancelled", async () => {
    seed([self, fixLogin, elsewhere, docs]);
    vi.mocked(askConfirm).mockResolvedValue(null);
    const merge = vi.spyOn(api, "merge").mockResolvedValue(noticesOk());

    await runCommand(command("session.merge"), context(self), undefined);

    expect(merge).not.toHaveBeenCalled();
  });

  it("shows no sheet and an error toast when no other session shares the workspace", async () => {
    seed([self, elsewhere]);
    const merge = vi.spyOn(api, "merge").mockResolvedValue(noticesOk());

    await runCommand(command("session.merge"), context(self), undefined);

    expect(askConfirm).not.toHaveBeenCalled();
    expect(merge).not.toHaveBeenCalled();
    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({
        description: "No other session in this workspace to merge in.",
        type: "error",
      }),
    );
  });
});
