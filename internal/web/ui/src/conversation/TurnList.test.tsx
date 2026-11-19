import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { Item, Turn } from "@/api/gen";
import { AGENT_ID } from "@/app/routes";
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

const AT = "2026-11-18T09:10:00Z";
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
