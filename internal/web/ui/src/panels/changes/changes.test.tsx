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
