import { render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Job, Session, Snapshot } from "@/api/gen";
import "@/commands";
import { fixtureSnapshot } from "@/fixtures";
import { useFleet } from "@/store/fleet";

import { Info } from "./Info";

const NOW = Date.parse("2026-11-18T12:00:00Z");

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(NOW);
});

afterEach(() => {
  vi.useRealTimers();
  useFleet.setState({ snapshot: null, stale: true, link: "connecting", updated: 0 });
  window.localStorage.clear();
  window.history.replaceState(null, "", window.location.pathname);
});

function sessionOf(snapshot: Snapshot, id: number): Session {
  for (const list of Object.values(snapshot.sessions ?? {})) {
    const found = list?.find((s) => s.id === id);
    if (found !== undefined) return found;
  }
  throw new Error(`the canned snapshot holds no session ${id}`);
}

// show stores the canned snapshot (after `tweak` edits it) with the session's fields overridden,
// puts the route on the session's info tab and returns the edited session.
function show(
  id: number,
  over: Partial<Session> = {},
  tweak?: (snapshot: Snapshot) => void,
): Session {
  const snapshot = fixtureSnapshot(NOW);
  tweak?.(snapshot);
  Object.assign(sessionOf(snapshot, id), over);
  useFleet.getState().apply("snapshot", snapshot);
  useFleet.setState({ stale: false, link: "live" });
  window.history.replaceState(null, "", `#/s/${id}/info`);
  return sessionOf(useFleet.getState().snapshot as Snapshot, id);
}

describe("Info: the sub-line and the tiles", () => {
  it("draws the sub-line first", () => {
    render(<Info session={show(1)} />);
    expect(screen.getByText(/working/)).toBeTruthy();
    expect(screen.getByText("aidanb/fix-pane-cursor")).toBeTruthy();
  });

  it("the Changes tile is a link to the changes screen with its counts and parity", () => {
    render(<Info session={show(1)} />);
    const link = screen.getByRole("link", { name: /Changes/ });
    expect(link.getAttribute("href")).toBe("#/s/1/diff");
    expect(link.textContent).toContain("+212");
    expect(link.textContent).toContain("−40");
    expect(link.textContent).toContain("↑3 ↓0");
  });

  it("the Changes tile is a plain box while the diff cannot be read", () => {
    render(<Info session={show(1, { status: "Loading" })} />);
    expect(screen.getByText("Changes")).toBeTruthy();
    expect(screen.queryByRole("link", { name: /Changes/ })).toBeNull();
  });

  it("the Changes tile is left out with no counts and unknown parity", () => {
    render(<Info session={show(3)} />);
    expect(screen.queryByText("Changes")).toBeNull();
  });

  it("the Changes tile is drawn for known parity alone", () => {
    render(<Info session={show(3, { parity_known: true, ahead: 0, behind: 0 })} />);
    expect(screen.getByText("Changes")).toBeTruthy();
    expect(screen.getByText("+0")).toBeTruthy();
    expect(screen.getByText("✓ even")).toBeTruthy();
  });

  it("the PR tile links to the pull request in a new tab and words its checks", () => {
    const first = render(<Info session={show(1)} />);
    const link = screen.getByRole("link", { name: /PR/ });
    expect(link.getAttribute("href")).toBe("https://github.com/aidan-bailey/loom/pull/482");
    expect(link.getAttribute("target")).toBe("_blank");
    expect(link.getAttribute("rel")).toContain("noopener");
    expect(link.getAttribute("rel")).toContain("noreferrer");
    expect(link.textContent).toContain("#482");
    expect(link.textContent).toContain("checks pending");
    first.unmount();

    const pr = { number: 482, state: "open", checks: "passing", url: "https://x.test/pull/482" };
    render(<Info session={show(1, { pr: pr as Session["pr"] })} />);
    expect(screen.getByRole("link", { name: /PR/ }).textContent).toContain("✓ checks pass");
  });

  it("the PR tile is a plain box when the PR has no url, and merged words its state", () => {
    render(<Info session={show(6)} />);
    expect(screen.getByText("#31")).toBeTruthy();
    expect(screen.getByText("merged")).toBeTruthy();
    expect(screen.queryByRole("link", { name: /PR/ })).toBeNull();
  });

  it("no PR, no PR tile", () => {
    render(<Info session={show(3)} />);
    expect(screen.queryByText("PR")).toBeNull();
  });

  it("the Account tile draws the shown window's share and both resets", () => {
    render(<Info session={show(1)} />);
    expect(screen.getByText("default")).toBeTruthy();
    expect(screen.getByText("62%")).toBeTruthy();
    expect(screen.getByText("5h · 1h40m")).toBeTruthy();
    const day = new Date(NOW + 70 * 3_600_000).toLocaleDateString([], { weekday: "short" });
    expect(screen.getByText(`7d 31% · ${day}`)).toBeTruthy();
  });

  it("the Account tile of a named account with one window has one sub-line", () => {
    render(<Info session={show(6)} />);
    expect(screen.getByText("work")).toBeTruthy();
    expect(screen.getByText("18%")).toBeTruthy();
    expect(screen.getByText("5h · 3h05m")).toBeTruthy();
    expect(screen.queryByText(/7d/)).toBeNull();
  });

  it("the Account tile says reset once the window has reset and drops its reset text", () => {
    render(
      <Info
        session={show(1, {}, (snapshot) => {
          const five = snapshot.usage?.find((u) => u.account === "default")?.five_hour;
          if (five) five.resets_at = new Date(NOW - 60_000).toISOString();
        })}
      />,
    );
    expect(screen.getByText("reset")).toBeTruthy();
    expect(screen.getByText("5h")).toBeTruthy();
  });

  it("the Account tile's figure is muted when the sample is stale", () => {
    const at = (ms: number) => (snapshot: Snapshot) => {
      const entry = snapshot.usage?.find((u) => u.account === "default");
      if (entry) entry.at = new Date(NOW - ms).toISOString();
    };
    const first = render(<Info session={show(1, {}, at(901_000))} />);
    expect(screen.getByText("62%").className).toContain("text-muted-foreground");
    first.unmount();
    render(<Info session={show(1, {}, at(60_000))} />);
    expect(screen.getByText("62%").className).toContain("text-foreground");
  });

  it("no Account tile for a session that does not run Claude, nor without a usage entry", () => {
    const first = render(<Info session={show(3)} />);
    expect(screen.queryByText("default")).toBeNull();
    first.unmount();
    render(
      <Info
        session={show(1, {}, (snapshot) => {
          snapshot.usage = [];
        })}
      />,
    );
    expect(screen.queryByText("62%")).toBeNull();
  });
});

describe("Info: the subagents", () => {
  it("are rows with a name, a state and a description, linked by their ids", () => {
    render(<Info session={show(1)} />);
    expect(screen.getByRole("heading", { name: "Agents 2" })).toBeTruthy();
    const recon = screen.getByRole("link", { name: /recon/ });
    expect(recon.getAttribute("href")).toBe("#/s/1/agent/a1f00dcafe0001");
    expect(within(recon).getByText("running")).toBeTruthy();
    expect(within(recon).getByText("Map every caller of the cursor path")).toBeTruthy();
    const verifier = screen.getByRole("link", { name: /verifier/ });
    expect(verifier.getAttribute("href")).toBe("#/s/1/agent/a1f00dcafe0002");
    expect(within(verifier).getByText("idle")).toBeTruthy();
  });

  it("a subagent without a valid id is a row, not a link", () => {
    const agents = [
      { id: "", name: "no-id", description: "", idle: false },
      { id: "a b/c", name: "odd-id", description: "", idle: true },
      { id: "ok_id-1", name: "fine", description: "", idle: false },
    ];
    render(<Info session={show(1, { agents })} />);
    expect(screen.queryByRole("link", { name: /no-id/ })).toBeNull();
    expect(screen.queryByRole("link", { name: /odd-id/ })).toBeNull();
    expect(screen.getByText("odd-id")).toBeTruthy();
    expect(screen.getByRole("link", { name: /fine/ }).getAttribute("href")).toBe(
      "#/s/1/agent/ok_id-1",
    );
  });

  it("no subagents, no Agents heading", () => {
    render(<Info session={show(6)} />);
    expect(screen.queryByRole("heading", { name: /Agents/ })).toBeNull();
  });
});

describe("Info: the jobs", () => {
  const link = (job: number): HTMLElement => {
    const found = document.querySelector(`a[href="#/s/1/job/${job}"]`);
    if (found === null) throw new Error(`no link to job ${job}`);
    return found as HTMLElement;
  };

  it("are links to the job view with their glyph, title and line", () => {
    render(<Info session={show(1)} />);
    expect(screen.getByRole("heading", { name: "Jobs 5" })).toBeTruthy();
    const running = link(214);
    expect(running.textContent).toContain("◐");
    expect(running.textContent).toContain("ui tests");
    expect(running.textContent).toContain("job 214 · 2m 10s");
    const held = link(215);
    expect(held.textContent).toContain("○");
    expect(held.textContent).toContain("race tests");
    expect(held.textContent).toContain("job 215 · held · after job 214");
    const done = link(212);
    expect(done.textContent).toContain("✓");
    expect(done.textContent).toContain("go vet ./...");
    expect(done.textContent).toContain("job 212 · exit 0 · 4m ago");
    const failed = link(209);
    expect(failed.textContent).toContain("✗");
    expect(failed.textContent).toContain("job 209 · exit 1 · 20m ago");
    const queued = link(216);
    expect(queued.textContent).toContain("○");
    expect(queued.textContent).toContain("queued");
  });

  it("have their glyphs drawn in the tone of their state", () => {
    render(<Info session={show(1)} />);
    expect(within(link(214)).getByText("◐").className).toContain("text-turn");
    expect(within(link(212)).getByText("✓").className).toContain("text-working");
    expect(within(link(209)).getByText("✗").className).toContain("text-error");
  });

  it("show the raw state of a job whose state this build does not know", () => {
    render(
      <Info
        session={show(1, {}, (snapshot) => {
          const job = sessionOf(snapshot, 1).jobs?.find((j) => j.id === 214) as Job;
          job.state = "paused" as Job["state"];
          job.started = "";
        })}
      />,
    );
    const row = link(214);
    expect(within(row).getByText("?").className).toContain("text-muted-foreground");
    expect(row.textContent).toContain("job 214 · paused");
    expect(within(row).queryByText("◐")).toBeNull();
  });

  it("no jobs, no Jobs heading", () => {
    render(<Info session={show(6)} />);
    expect(screen.queryByRole("heading", { name: /Jobs/ })).toBeNull();
  });
});
