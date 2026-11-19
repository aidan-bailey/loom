import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Item, Step, Turn } from "@/api/gen";
import { AGENT_ID } from "@/app/routes";
import { fixtureTurns } from "@/fixtures";
import { clock, TurnList, turnLabel } from "./TurnList";

function turn(over: Partial<Turn> = {}): Turn {
  return {
    id: 1,
    at: "2026-11-18T09:05:00Z",
    from: "you",
    name: "",
    text: "hello",
    cut: false,
    command: false,
    partial: false,
    items: null,
    ...over,
  };
}

const NOW = Date.parse("2026-11-18T12:00:00Z");
const AT = "2026-11-18T09:10:00Z";

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(NOW);
});
afterEach(() => {
  vi.useRealTimers();
});

const step = (over: Partial<Step> = {}): Step => ({
  tool: "Bash",
  summary: "go test",
  at: new Date(NOW - 3 * 60_000).toISOString(),
  state: "done",
  finished: false,
  ...over,
});
const stepsItem = (steps: Step[], over: Partial<Item> = {}): Item => ({
  kind: "steps",
  at: AT,
  total: steps.length,
  steps,
  ...over,
});
const thinking = (text: string): Item => ({ kind: "thinking", at: AT, text });
const textItem: Item = { kind: "text", at: AT, blocks: [{ t: "p", c: [{ t: "text", v: "ok" }] }] };
const lines = (n: number): string =>
  Array.from({ length: n }, (_, i) => `line ${i + 1}`).join("\n");
const list = (turns: Turn[], live = false): ReturnType<typeof render> =>
  render(<TurnList turns={turns} sessionId={1} live={live} remote="https://claude.ai/code/x" />);

describe("AGENT_ID", () => {
  it("AGENT_ID: admits the ids the daemon writes and refuses the rest", () => {
    for (const ok of ["a1f00dcafe0001", "agent_1", "a-b"]) expect(AGENT_ID.test(ok), ok).toBe(true);
    for (const bad of ["", "a b", "a/b", "../x", "a?b=1"]) {
      expect(AGENT_ID.test(bad), bad).toBe(false);
    }
  });
});

describe("turnLabel", () => {
  it("turnLabel: each from has its own label", () => {
    expect(turnLabel(turn({ from: "you" }))).toBe("YOU");
    expect(turnLabel(turn({ from: "plugin", name: "Ralph" }))).toBe("RALPH");
    expect(turnLabel(turn({ from: "plugin" }))).toBe("PLUGIN");
    expect(turnLabel(turn({ from: "peer", name: "api" }))).toBe("FROM API");
    expect(turnLabel(turn({ from: "peer" }))).toBe("FROM ANOTHER SESSION");
    expect(turnLabel(turn({ from: "task" }))).toBe("BACKGROUND TASK");
    expect(turnLabel(turn({ from: "brief" }))).toBe("BRIEF");
    expect(turnLabel(turn({ from: "mystery" as Turn["from"] }))).toBe("MYSTERY");
  });
});

describe("clock", () => {
  it("clock: the device's 24-hour hh:mm, empty for none", () => {
    const iso = "2026-11-18T09:05:00Z";
    const d = new Date(iso);
    const two = (n: number): string => String(n).padStart(2, "0");
    expect(clock(iso)).toBe(`${two(d.getHours())}:${two(d.getMinutes())}`);
    expect(clock("")).toBe("");
    expect(clock("not a time")).toBe("");
  });
});

describe("TurnList", () => {
  it("TurnList: a prompt's box has its label, its time and its text", () => {
    list([turn()]);
    expect(screen.getByText(`YOU · ${clock("2026-11-18T09:05:00Z")}`)).toBeInTheDocument();
    expect(screen.getByText("hello")).toBeInTheDocument();
  });

  it("TurnList: a command prompt is in the mono font", () => {
    list([turn({ id: 1, text: "/compact", command: true }), turn({ id: 2, text: "plain" })]);
    expect(screen.getByText("/compact")).toHaveClass("font-mono");
    expect(screen.getByText("plain")).not.toHaveClass("font-mono");
  });

  it("TurnList: a prompt of more than 12 lines folds and opens", async () => {
    const user = userEvent.setup();
    const { container } = list([turn({ text: lines(20) })]);
    const box = container.querySelector("fieldset > div");
    expect(box?.textContent).toBe(lines(12));
    await user.click(screen.getByRole("button", { name: "▸ 8 more lines" }));
    expect(box?.textContent).toBe(lines(20));
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("TurnList: a prompt of 12 lines has no button", () => {
    list([turn({ text: lines(12) })]);
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("TurnList: a cut prompt ends with an ellipsis once unfolded", async () => {
    const user = userEvent.setup();
    const { container } = list([turn({ id: 1, text: "short", cut: true })]);
    expect(screen.getByText("short…")).toBeInTheDocument();
    list([turn({ id: 2, text: lines(20), cut: true })]);
    const long = container.ownerDocument.querySelectorAll("fieldset > div")[1];
    expect(long?.textContent).toBe(lines(12));
    await user.click(screen.getByRole("button"));
    expect(long?.textContent).toBe(lines(20) + "…");
  });

  it("TurnList: a partial turn says its start is earlier and has no prompt box", () => {
    list([turn({ partial: true, text: "hidden prompt" })]);
    expect(screen.getByText("Its start is earlier.")).toBeInTheDocument();
    expect(screen.queryByText("hidden prompt")).toBeNull();
    expect(screen.queryByText(/YOU/)).toBeNull();
  });

  it("TurnList: the newest turn is labelled working only while live", () => {
    const turns = [turn({ id: 1, items: [textItem] }), turn({ id: 2, items: [textItem] })];
    const { unmount } = list(turns, true);
    expect(screen.getAllByText("CLAUDE · ◐ WORKING")).toHaveLength(1);
    expect(screen.getAllByText(`CLAUDE · ${clock(AT)}`)).toHaveLength(1);
    unmount();
    list(turns, false);
    expect(screen.queryByText(/WORKING/)).toBeNull();
  });

  it("TurnList: a turn of compaction rules alone has no Claude label", () => {
    list([turn({ items: [{ kind: "compacted", at: AT }] })]);
    expect(screen.queryByText(/CLAUDE/)).toBeNull();
  });

  it("TurnList: a turn with no items draws its prompt alone", () => {
    list([turn({ items: null })]);
    expect(screen.getByText("hello")).toBeInTheDocument();
    expect(screen.queryByText(/CLAUDE/)).toBeNull();
  });

  it("TurnList: prompt text holding markup renders as text", () => {
    const { container } = list([turn({ text: "<b>hi</b><script>x</script>" })]);
    expect(container.textContent).toContain("<b>hi</b><script>x</script>");
    expect(container.querySelector("b, script")).toBeNull();
  });
});

describe("TurnList items", () => {
  it("TurnList: the fixture's conversation draws each kind of item", () => {
    render(
      <TurnList
        turns={fixtureTurns().turns ?? []}
        sessionId={1}
        live={false}
        remote="https://claude.ai/code/webshot"
      />,
    );
    expect(screen.getAllByText(/^YOU/).length).toBeGreaterThan(0);
    expect(screen.getByText(/^COMPACTED/)).toBeInTheDocument();
    expect(screen.getByText("Its start is earlier.")).toBeInTheDocument();
    expect(screen.getByText("× Interrupted by you")).toBeInTheDocument();
    const err = screen.getByText("API error: overloaded, retrying");
    expect(err.textContent?.startsWith("×")).toBe(false);
    expect(screen.getByRole("heading", { name: "Findings" })).toBeInTheDocument();
    expect(screen.getByText("7 steps")).toBeInTheDocument();
    expect(screen.getByText("· 1 failed")).toBeInTheDocument();
    expect(
      screen.getByText("▸ thinking: The ui tests may be slow, so start with TestPane"),
    ).toBeInTheDocument();
  });

  it("TurnList: a text item draws its tree and continues in Claude when cut", () => {
    const cut: Item = { ...textItem, truncated: true };
    const { container, unmount } = list([turn({ items: [cut] })]);
    expect(screen.getByText("ok")).toBeInTheDocument();
    expect(container.textContent).toContain("… continues in Claude");
    const a = screen.getByRole("link", { name: "Claude" });
    expect(a).toHaveAttribute("href", "https://claude.ai/code/x");
    expect(a).toHaveAttribute("target", "_blank");
    expect(a).toHaveAttribute("rel", "noopener noreferrer");
    unmount();
    const bare = render(
      <TurnList turns={[turn({ items: [cut] })]} sessionId={1} live={false} remote="" />,
    );
    expect(bare.container.textContent).toContain("… continues in Claude");
    expect(screen.queryByRole("link")).toBeNull();
    bare.unmount();
    const whole = list([turn({ items: [textItem] })]);
    expect(whole.container.textContent).not.toContain("continues in");
  });

  it("TurnList: a thinking item folds to its first non-blank line", async () => {
    const user = userEvent.setup();
    list([turn({ items: [{ ...thinking("\n  \nsecond\nthird"), truncated: true }] })]);
    const trigger = screen.getByRole("button", { name: "▸ thinking: second" });
    await waitFor(() => expect(screen.queryByText(/third/)).toBeNull());
    await user.click(trigger);
    expect((await screen.findByText(/third/)).textContent).toBe("\n  \nsecond\nthird…");
  });

  it("TurnList: a steps fold counts its steps and its failures", async () => {
    const user = userEvent.setup();
    list([
      turn({
        items: [
          stepsItem([step()]),
          stepsItem([step(), step(), step()], { failed: 2 }),
          stepsItem([step(), step(), step()], { total: 5 }),
        ],
      }),
    ]);
    expect(screen.getByText("1 step")).toBeInTheDocument();
    expect(screen.getByText("3 steps")).toBeInTheDocument();
    expect(screen.getByText("· 2 failed")).toBeInTheDocument();
    await user.click(screen.getByText("5 steps"));
    expect(await screen.findByText("2 earlier steps not shown")).toBeInTheDocument();
  });

  it("TurnList: a step's glyph and end by its state", async () => {
    list([turn({ items: [stepsItem([step({ summary: "p", state: "pending" })])] })], true);
    const live = (await screen.findByText("p")).parentElement as HTMLElement;
    expect(live.textContent).toContain("◐");
    expect(live.textContent).toContain("running 3m");
  });

  it("TurnList: a pending step in a turn that is not live has no glyph", async () => {
    const user = userEvent.setup();
    list([turn({ items: [stepsItem([step({ summary: "p", state: "pending" })])] })]);
    await user.click(screen.getByText("1 step"));
    const row = (await screen.findByText("p")).parentElement as HTMLElement;
    expect(row.textContent).not.toContain("◐");
    expect(row.textContent).toContain("no result");
  });

  it("TurnList: failed, interrupted and done steps draw their glyphs", async () => {
    const user = userEvent.setup();
    list([
      turn({
        items: [
          stepsItem([
            step({ summary: "f", state: "failed" }),
            step({ summary: "i", state: "interrupted" }),
            step({ summary: "d", state: "done" }),
          ]),
        ],
      }),
    ]);
    await user.click(screen.getByText("3 steps"));
    const f = (await screen.findByText("f")).parentElement as HTMLElement;
    const i = screen.getByText("i").parentElement as HTMLElement;
    const d = screen.getByText("d").parentElement as HTMLElement;
    expect(f.querySelector(".text-error")?.textContent).toBe("×");
    expect(i.textContent).toContain("×");
    expect(i.querySelector(".text-error")).toBeNull();
    expect(d.textContent).not.toMatch(/[×◐]/);
    expect(d.textContent).not.toContain("no result");
  });

  it("TurnList: a step that launched a subagent links to its screen", async () => {
    const user = userEvent.setup();
    list([
      turn({
        items: [
          stepsItem([
            step({ tool: "Agent", summary: "open", agent: "a1f00dcafe0001" }),
            step({ tool: "Agent", summary: "shut", agent: "a1f00dcafe0002", finished: true }),
            step({ tool: "Agent", summary: "bad", agent: "a b/c" }),
          ]),
        ],
      }),
    ]);
    await user.click(screen.getByText("3 steps"));
    await screen.findByText("open");
    const links = screen.getAllByRole("link");
    expect(links).toHaveLength(2);
    expect(links[0]).toHaveAttribute("href", "#/s/1/agent/a1f00dcafe0001");
    expect(links[0]?.textContent).toContain("◐");
    expect(links[0]?.textContent?.endsWith("›")).toBe(true);
    expect(links[1]?.textContent).not.toContain("◐");
    expect(links[1]?.textContent?.endsWith("›")).toBe(true);
    const bad = screen.getByText("bad").parentElement as HTMLElement;
    expect(bad.tagName).not.toBe("A");
    expect(bad.textContent).not.toContain("›");
  });

  it("TurnList: the live newest turn opens its last steps fold and last thinking item", async () => {
    const items = [
      stepsItem([step({ summary: "s1" })]),
      stepsItem([step({ summary: "s2" })]),
      thinking("think one"),
      thinking("think two"),
    ];
    const { unmount } = list([turn({ items })], true);
    expect(await screen.findByText("s2")).toBeInTheDocument();
    expect(await screen.findByText("think two")).toBeInTheDocument();
    expect(screen.queryByText("s1")).toBeNull();
    expect(screen.queryByText("think one")).toBeNull();
    unmount();
    list([turn({ items })], false);
    for (const x of ["s1", "s2", "think one", "think two"])
      expect(screen.queryByText(x)).toBeNull();
  });

  it("TurnList: only the newest turn's last folds open", async () => {
    const mk = (id: number, summary: string): Turn =>
      turn({ id, items: [stepsItem([step({ summary })])] });
    list([mk(1, "old"), mk(2, "new")], true);
    expect(await screen.findByText("new")).toBeInTheDocument();
    expect(screen.queryByText("old")).toBeNull();
  });

  it("TurnList: a fold closes by itself when the turn stops being live and a fold the reader opened stays open", async () => {
    const user = userEvent.setup();
    const turns = [turn({ items: [stepsItem([step({ summary: "auto" })])] })];
    const view = render(<TurnList turns={turns} sessionId={1} live={true} remote="" />);
    expect(await screen.findByText("auto")).toBeInTheDocument();
    view.rerender(<TurnList turns={turns} sessionId={1} live={false} remote="" />);
    await waitFor(() => expect(screen.queryByText("auto")).toBeNull());
    view.unmount();

    const held = render(<TurnList turns={turns} sessionId={1} live={false} remote="" />);
    await user.click(screen.getByText("1 step"));
    expect(await screen.findByText("auto")).toBeInTheDocument();
    held.rerender(<TurnList turns={turns} sessionId={1} live={false} remote="" />);
    expect(screen.getByText("auto")).toBeInTheDocument();
  });

  it("TurnList: a compacted rule carries its time", () => {
    list([
      turn({
        items: [
          { kind: "compacted", at: AT },
          { kind: "compacted", at: "" },
        ],
      }),
    ]);
    expect(screen.getByText(`COMPACTED ${clock(AT)}`)).toBeInTheDocument();
    expect(screen.getByText("COMPACTED")).toBeInTheDocument();
  });

  it("TurnList: a note is dim with a cross, or red when it is an error", () => {
    list([
      turn({
        items: [
          { kind: "note", at: AT, text: "Interrupted by you" },
          { kind: "note", at: AT, text: "API error: overloaded, retrying", error: true },
        ],
      }),
    ]);
    expect(screen.getByText("× Interrupted by you")).not.toHaveClass("text-error");
    expect(screen.getByText("API error: overloaded, retrying")).toHaveClass("text-error");
  });

  it("TurnList: an item of an unknown kind draws its kind and text in neutral styling", () => {
    const odd = (text?: string): Item =>
      ({ kind: "mystery", at: AT, ...(text === undefined ? {} : { text }) }) as unknown as Item;
    const { container } = list([turn({ text: "prompt", items: [odd("hello"), odd()] })]);
    expect(screen.getAllByText("mystery")).toHaveLength(2);
    expect(screen.getByText("hello")).toBeInTheDocument();
    expect(container.querySelector(".text-error")).toBeNull();
    expect(container.querySelector("[data-slot=collapsible]")).toBeNull();
  });

  it("TurnList: steps, thinking and notes holding markup render as text", async () => {
    const user = userEvent.setup();
    const { container } = list([
      turn({
        items: [
          stepsItem([step({ summary: "<img src=x onerror=alert(1)>" })]),
          thinking("<b>x</b>"),
          { kind: "note", at: AT, text: "<script>y</script>" },
        ],
      }),
    ]);
    await user.click(screen.getByText("1 step"));
    await user.click(screen.getByRole("button", { name: /thinking/ }));
    await screen.findByText("<b>x</b>");
    expect(container.textContent).toContain("<img src=x onerror=alert(1)>");
    expect(container.textContent).toContain("<script>y</script>");
    expect(container.querySelector("img, b, script")).toBeNull();
  });
});
