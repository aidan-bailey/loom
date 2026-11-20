import { QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, within } from "@testing-library/react";
import type { ReactElement, ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { File, Outcome, Patch, Session, Snapshot } from "@/api/gen";
import { queryClient } from "@/api/queries";
import { api } from "@/api/request";
import "@/commands";
import { deferred } from "@/commands/testkit";
import { fixturePatch, fixtureSnapshot } from "@/fixtures";
import { useDrafts } from "@/store/drafts";
import { useFleet } from "@/store/fleet";

import { Changes } from "./Changes";

const NOW = Date.parse("2026-11-18T12:00:00Z");

const spyOnDiff = () => vi.spyOn(api, "diff").mockRejectedValue(new Error("unexpected diff read"));
let diff: ReturnType<typeof spyOnDiff>;

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(NOW);
  queryClient.clear();
  diff = spyOnDiff();
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  useFleet.setState({ snapshot: null, stale: true, link: "connecting", updated: 0 });
  useDrafts.getState().clear();
  window.history.replaceState(null, "", window.location.pathname);
});

const ok = <T,>(value: T): Outcome<T> => ({ kind: "ok", status: 200, value, notices: [] });
const refused = (status: number, message: string): Outcome<never> => ({
  kind: "refused",
  status,
  message,
});

function wrapper({ children }: { children: ReactNode }): ReactElement {
  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

async function settle(ms = 1): Promise<void> {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}

function sessionOf(snapshot: Snapshot, id: number): Session {
  for (const list of Object.values(snapshot.sessions ?? {})) {
    const found = list?.find((s) => s.id === id);
    if (found !== undefined) return found;
  }
  throw new Error(`the canned snapshot holds no session ${id}`);
}

// show stores the canned snapshot with the session's fields overridden, puts the route on the
// session's diff tab and returns the edited session.
function show(id = 1, over: Partial<Session> = {}): Session {
  const snapshot = fixtureSnapshot(NOW);
  Object.assign(sessionOf(snapshot, id), over);
  useFleet.getState().apply("snapshot", snapshot);
  useFleet.setState({ stale: false, link: "live" });
  window.history.replaceState(null, "", `#/s/${id}/diff`);
  return sessionOf(useFleet.getState().snapshot as Snapshot, id);
}

function draw(session: Session) {
  return render(<Changes session={session} />, { wrapper });
}

function patchOf(...files: Partial<File>[]): Patch {
  return {
    base: "a1b2c3d4e5f6",
    files: files.map((over) => ({
      path: "a.go",
      old_path: "",
      status: "M",
      binary: false,
      added: 1,
      removed: 0,
      hunks: [],
      omitted: "",
      ...over,
    })),
  };
}

const row = (name: RegExp): HTMLElement => screen.getByRole("button", { name });

describe("Changes: the sub-line, loading and errors", () => {
  it("says loading with the session's sub-line before the patch lands", async () => {
    const wait = deferred<Outcome<Patch>>();
    diff.mockReturnValue(wait.promise);
    draw(show());
    await settle();
    expect(screen.getByText("Loading…")).toBeTruthy();
    expect(screen.getByText("aidanb/fix-pane-cursor")).toBeTruthy();
    expect(screen.queryByText(/\bfiles?\b/)).toBeNull();
  });

  it("draws the file count, the summed counts and the short base once the patch is held", async () => {
    diff.mockResolvedValue(ok(fixturePatch()));
    draw(show());
    await settle();
    expect(screen.getByText("5 files")).toBeTruthy();
    expect(screen.getByText("+5252")).toBeTruthy();
    expect(screen.getByText("−36")).toBeTruthy();
    expect(screen.getByText("since a1b2c3d")).toBeTruthy();
    expect(screen.queryByText("aidanb/fix-pane-cursor")).toBeNull();
  });

  it("says 1 file for one", async () => {
    diff.mockResolvedValue(ok(patchOf({})));
    draw(show());
    await settle();
    expect(screen.getByText("1 file")).toBeTruthy();
    expect(screen.queryByText("1 files")).toBeNull();
  });

  it("leaves the base out of the sub-line when the patch has none", async () => {
    diff.mockResolvedValue(ok({ ...patchOf({}), base: "" }));
    draw(show());
    await settle();
    expect(screen.getByText("1 file")).toBeTruthy();
    expect(screen.queryByText(/since/)).toBeNull();
  });

  it("says why a read failed, with a retry that reads again", async () => {
    diff.mockResolvedValueOnce(refused(409, "session is busy"));
    diff.mockResolvedValueOnce(ok(fixturePatch()));
    draw(show());
    await settle();
    expect(screen.getByText("session is busy")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "retry" }));
    await settle();
    expect(screen.getByText("5 files")).toBeTruthy();
    expect(screen.queryByText("session is busy")).toBeNull();
  });

  it("says no changes since the base for an empty patch", async () => {
    diff.mockResolvedValue(ok({ base: "a1b2c3d4e5f6", files: [] }));
    const view = draw(show());
    await settle();
    expect(screen.getByText("No changes since a1b2c3d.")).toBeTruthy();
    view.unmount();
    queryClient.clear();

    diff.mockResolvedValue(ok({ base: "a1b2c3d4e5f6", files: null }));
    const second = draw(show());
    await settle();
    expect(screen.getByText("No changes since a1b2c3d.")).toBeTruthy();
    second.unmount();
    queryClient.clear();

    diff.mockResolvedValue(ok({ base: "", files: [] }));
    draw(show());
    await settle();
    expect(screen.getByText("No changes.")).toBeTruthy();
  });

  it("keeps the patch and says the refresh failed", async () => {
    const session = show();
    diff.mockResolvedValueOnce(ok(fixturePatch()));
    const view = draw(session);
    await settle();
    expect(screen.getByText("5 files")).toBeTruthy();

    diff.mockResolvedValueOnce(refused(409, "session is busy"));
    view.rerender(
      <Changes
        session={{ ...session, diff: { ...session.diff, added: session.diff.added + 1 } }}
      />,
    );
    await settle();
    expect(screen.getByText("5 files")).toBeTruthy();
    expect(screen.getByText(/Could not refresh: session is busy/)).toBeTruthy();

    diff.mockResolvedValueOnce(ok(fixturePatch()));
    const line = screen.getByText(/Could not refresh/);
    fireEvent.click(within(line).getByRole("button", { name: "retry" }));
    await settle();
    expect(screen.queryByText(/Could not refresh/)).toBeNull();
  });
});

describe("Changes: the files", () => {
  async function drawCanned(): Promise<ReturnType<typeof draw>> {
    diff.mockResolvedValue(ok(fixturePatch()));
    const view = draw(show());
    await settle();
    return view;
  }

  it("draws each file's status, name, directory or source and counts", async () => {
    await drawCanned();
    const pane = row(/pane\.go/).textContent ?? "";
    for (const part of ["M", "ui/", "+12", "−3"]) expect(pane).toContain(part);
    const header = row(/header\.go/).textContent ?? "";
    for (const part of ["R", "from ui/title.go", "+40", "−2"]) expect(header).toContain(part);
    const cursor = row(/cursor\.png/).textContent ?? "";
    for (const part of ["A", "assets/", "binary"]) expect(cursor).toContain(part);
    expect(cursor).not.toContain("+");
    const legacy = row(/legacy\.go/).textContent ?? "";
    expect(legacy).toContain("D");
    expect(legacy).toContain("−31");
    expect(legacy).not.toContain("+");
    const tables = row(/tables_gen\.go/).textContent ?? "";
    expect(tables).toContain("+5200");
    expect(tables).not.toContain("−");
  });

  it("a path with no directory has ./ and a copy names its source", async () => {
    diff.mockResolvedValue(
      ok(patchOf({ path: "README.md" }, { path: "b.go", old_path: "a.go", status: "C" })),
    );
    draw(show());
    await settle();
    expect(row(/README\.md/).textContent).toContain("./");
    expect(row(/b\.go/).textContent).toContain("from a.go");
  });

  it("a status this build does not know is drawn as sent", async () => {
    diff.mockResolvedValue(ok(patchOf({ status: "X" as File["status"] }, { path: "b.go" })));
    draw(show());
    await settle();
    expect(row(/a\.go/).textContent).toContain("X");
  });

  it("every file is closed at first when there are several", async () => {
    await drawCanned();
    const triggers = screen.getAllByRole("button").filter((b) => b.hasAttribute("aria-expanded"));
    expect(triggers).toHaveLength(5);
    for (const trigger of triggers) expect(trigger.getAttribute("aria-expanded")).toBe("false");
  });

  it("a lone file starts open", async () => {
    diff.mockResolvedValue(ok(patchOf({})));
    draw(show());
    await settle();
    expect(row(/a\.go/).getAttribute("aria-expanded")).toBe("true");
  });

  it("a lone file opens on the first load only", async () => {
    const session = show();
    diff.mockResolvedValueOnce(ok(patchOf({}, { path: "b.go" })));
    const view = draw(session);
    await settle();
    expect(row(/a\.go/).getAttribute("aria-expanded")).toBe("false");

    diff.mockResolvedValueOnce(ok(patchOf({})));
    view.rerender(
      <Changes
        session={{ ...session, diff: { ...session.diff, added: session.diff.added + 1 } }}
      />,
    );
    await settle();
    expect(row(/a\.go/).getAttribute("aria-expanded")).toBe("false");
  });

  it("the open set survives a refresh", async () => {
    const session = show();
    diff.mockResolvedValue(ok(fixturePatch()));
    const view = draw(session);
    await settle();
    fireEvent.click(row(/pane\.go/));
    expect(row(/pane\.go/).getAttribute("aria-expanded")).toBe("true");

    const refresh = async (n: number): Promise<void> => {
      view.rerender(
        <Changes
          session={{ ...session, diff: { ...session.diff, added: session.diff.added + n } }}
        />,
      );
      await settle();
    };
    await refresh(1);
    expect(row(/pane\.go/).getAttribute("aria-expanded")).toBe("true");
    fireEvent.click(row(/pane\.go/));
    expect(row(/pane\.go/).getAttribute("aria-expanded")).toBe("false");
    await refresh(2);
    expect(row(/pane\.go/).getAttribute("aria-expanded")).toBe("false");
  });

  it("draws the reply box under the files", async () => {
    diff.mockResolvedValue(ok(fixturePatch()));
    const view = draw(show());
    await settle();
    expect(screen.getByRole("textbox", { name: "Reply" })).toBeTruthy();
    view.unmount();
    queryClient.clear();

    draw(show(1, { status: "Paused", group: "paused" }));
    await settle();
    expect(screen.getByText("5 files")).toBeTruthy();
    expect(screen.getByText("Resume the session to reply.")).toBeTruthy();
  });

  it("reads the diff every 10 s while the session runs, and not otherwise", async () => {
    diff.mockResolvedValue(ok(fixturePatch()));
    const running = draw(show());
    await settle();
    expect(diff).toHaveBeenCalledTimes(1);
    await settle(10_000);
    expect(diff).toHaveBeenCalledTimes(2);
    running.unmount();
    queryClient.clear();
    diff.mockClear();

    draw(show(1, { status: "Ready", group: "your_turn" }));
    await settle();
    expect(diff).toHaveBeenCalledTimes(1);
    await settle(30_000);
    expect(diff).toHaveBeenCalledTimes(1);
  });
});

describe("Changes: an open file", () => {
  const same = { normalizer: (s: string) => s };

  // open clicks the file row's trigger and returns it.
  function open(name: RegExp): HTMLElement {
    const trigger = row(name);
    fireEvent.click(trigger);
    return trigger;
  }

  async function drawPatch(patch: Patch) {
    diff.mockResolvedValue(ok(patch));
    const view = draw(show());
    await settle();
    return view;
  }

  // A lone file starts open, so a test of one file's content reads it without a click.
  const hunkOf = (header: string, lines: string[] | null, cut: number[] | null = []) => ({
    header,
    lines,
    cut,
  });

  it("opening a file draws its hunk header and lines", async () => {
    await drawPatch(fixturePatch());
    open(/pane\.go/);
    const ranges = screen.getByText("@@ -88,9 +88,12 @@");
    expect(ranges.classList.contains("text-turn")).toBe(true);
    expect(screen.getByText("func (p *Pane) View() string {")).toBeTruthy();
    expect(screen.getByText("+\tif p.cursorVisible() {", same)).toBeTruthy();
    expect(screen.getByText("-\tp.drawCursor(frame)", same)).toBeTruthy();
  });

  it("an added line, a removed line and a context line have their own rows", async () => {
    await drawPatch(fixturePatch());
    open(/pane\.go/);
    const added = screen.getByText("+\tif p.cursorVisible() {", same);
    const removed = screen.getByText("-\tp.drawCursor(frame)", same);
    const context = screen.getByText("  \tframe := p.render()", same);
    expect(added.classList.contains("bg-diff-add")).toBe(true);
    expect(added.classList.contains("bg-diff-del")).toBe(false);
    expect(removed.classList.contains("bg-diff-del")).toBe(true);
    expect(removed.classList.contains("bg-diff-add")).toBe(false);
    expect(context.classList.contains("bg-diff-add")).toBe(false);
    expect(context.classList.contains("bg-diff-del")).toBe(false);
  });

  it('a "no newline" marker is muted', async () => {
    await drawPatch(
      patchOf({ hunks: [hunkOf("@@ -1 +1 @@", ["+a", "\\ No newline at end of file"])] }),
    );
    const marker = screen.getByText("\\ No newline at end of file", same);
    expect(marker.classList.contains("text-muted-foreground")).toBe(true);
    expect(marker.classList.contains("bg-diff-add")).toBe(false);
    expect(marker.classList.contains("bg-diff-del")).toBe(false);
  });

  it("a header that is not a hunk range is drawn whole", async () => {
    await drawPatch(patchOf({ hunks: [hunkOf("not a range", ["+a"])] }));
    expect(screen.getByText("not a range")).toBeTruthy();
  });

  it("a cut line ends in a mark", async () => {
    const view = await drawPatch(patchOf({ hunks: [hunkOf("@@ -1 +1 @@", ["+a", "+b"], [1])] }));
    const a = screen.getByText("+a", same);
    const b = screen.getByText("+b", same);
    expect(within(b).getByText("…")).toBeTruthy();
    expect(within(a).queryByText("…")).toBeNull();
    view.unmount();
    queryClient.clear();

    await drawPatch(patchOf({ hunks: [hunkOf("@@ -1 +1 @@", ["+a", "+b"], null)] }));
    expect(screen.queryByText("…")).toBeNull();
  });

  it("an empty line keeps its row", async () => {
    const view = await drawPatch(patchOf({ hunks: [hunkOf("@@ -1 +1 @@", [""])] }));
    // the header's row and the empty line's
    expect(view.container.querySelectorAll(".min-h-5")).toHaveLength(2);
  });

  it("a deleted file draws its removed lines", async () => {
    await drawPatch(fixturePatch());
    open(/legacy\.go/);
    expect(screen.getByText("-package ui", same)).toBeTruthy();
    expect(screen.getByText("-// legacy is gone.", same)).toBeTruthy();
  });

  it("a file too long to show says how many lines", async () => {
    const view = await drawPatch(fixturePatch());
    open(/tables_gen\.go/);
    expect(screen.getByText("Too long to show here (5200 changed lines).")).toBeTruthy();
    view.unmount();
    queryClient.clear();

    await drawPatch(patchOf({ omitted: "lines", added: 5200, removed: 7 }));
    expect(screen.getByText("Too long to show here (5207 changed lines).")).toBeTruthy();
  });

  it("a file the diff budget cut says so", async () => {
    await drawPatch(patchOf({ omitted: "budget" }));
    expect(screen.getByText("Not shown: the diff is over 1 MiB.")).toBeTruthy();
  });

  it("an omitted reason from a newer daemon is drawn as sent", async () => {
    await drawPatch(patchOf({ omitted: "huge" as File["omitted"] }));
    expect(screen.getByText("Not shown (huge).")).toBeTruthy();
    expect(screen.queryByText("No text changes.")).toBeNull();
  });

  it("a binary file says so", async () => {
    await drawPatch(fixturePatch());
    open(/cursor\.png/);
    expect(screen.getByText("Binary file.")).toBeTruthy();
  });

  it("an omitted reason beats a binary flag", async () => {
    await drawPatch(patchOf({ binary: true, omitted: "lines", added: 9 }));
    expect(screen.getByText("Too long to show here (9 changed lines).")).toBeTruthy();
    expect(screen.queryByText("Binary file.")).toBeNull();
  });

  it("a text file with no hunks says there are no text changes, for an empty and a null list", async () => {
    const view = await drawPatch(patchOf({ hunks: [] }));
    expect(screen.getByText("No text changes.")).toBeTruthy();
    view.unmount();
    queryClient.clear();

    await drawPatch(patchOf({ hunks: null }));
    expect(screen.getByText("No text changes.")).toBeTruthy();
  });

  it("a lone file's hunks are drawn on the first load", async () => {
    const pane = fixturePatch().files?.[0];
    if (pane === undefined) throw new Error("the canned patch holds no file");
    await drawPatch({ base: "a1b2c3d4e5f6", files: [pane] });
    expect(screen.getByText("@@ -88,9 +88,12 @@")).toBeTruthy();
  });

  it("diff lines are drawn as text, never as markup", async () => {
    const line = "+<b>bold</b><img src=x>";
    const view = await drawPatch(patchOf({ hunks: [hunkOf("@@ -1 +1 @@", [line])] }));
    const shown = screen.getByText(line, same);
    expect(shown.textContent).toBe(line);
    expect(view.container.querySelector("b, img")).toBeNull();
  });
});
