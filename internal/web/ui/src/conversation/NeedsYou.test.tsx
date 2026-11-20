import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Ask, Session } from "@/api/gen";
import "@/commands";
import { toastManager } from "@/components/ui/toast";
import { makeSession, resetFleet, seed } from "@/commands/testkit";
import { fixtureSnapshot } from "@/fixtures";
import { allSessions } from "@/fleet/groups";
import { useDrafts } from "@/store/drafts";

import { NeedsYou } from "./NeedsYou";

const canned = allSessions(fixtureSnapshot()).find((s) => s.id === 2)?.asks ?? [];
const permission = canned.find((a) => a.id === "fixture-permission") as Ask;
const question = canned.find((a) => a.id === "fixture-question") as Ask;

function setup(over: Partial<Session> = {}): ReturnType<typeof render> {
  const session = makeSession({
    id: 2,
    status: "Prompting",
    group: "needs_you",
    remote_url: "https://claude.ai/code/webshot2",
    wait: { reason: "Allow Bash", detail: "go test ./ui/..." },
    asks: [],
    ...over,
  });
  seed([session]);
  return render(<NeedsYou session={session} />);
}

beforeEach(() => {
  vi.spyOn(toastManager, "add").mockImplementation(() => "toast");
  window.history.replaceState(null, "", "#/s/2");
});

afterEach(() => {
  vi.restoreAllMocks();
  resetFleet();
  useDrafts.getState().clear();
  window.history.replaceState(null, "", window.location.pathname);
});

describe("NeedsYou", () => {
  it("says why the session waits: its reason, else asks: and its gist, else Needs you", () => {
    const first = setup();
    expect(screen.getByText("Allow Bash")).toBeInTheDocument();
    expect(screen.getAllByText("Needs you")).toHaveLength(1);
    first.unmount();
    const second = setup({ wait: { reason: "", detail: "" }, gist: "pick a plan" });
    expect(screen.getByText("asks: pick a plan")).toBeInTheDocument();
    expect(screen.getAllByText("Needs you")).toHaveLength(1);
    second.unmount();
    setup({ wait: { reason: "", detail: "" } });
    expect(screen.getAllByText("Needs you")).toHaveLength(2);
  });

  it("draws the wait's detail only while there are no asks", () => {
    const first = setup();
    expect(screen.getByText("go test ./ui/...").closest("pre")).not.toBeNull();
    first.unmount();
    setup({ asks: [permission] });
    expect(screen.queryByText("go test ./ui/...", { selector: "pre" })).toBeNull();
  });

  it("draws the asks between the reason and the link", () => {
    setup({ asks: [question] });
    const text = question.questions?.[0]?.question ?? "";
    const reason = screen.getByText("Allow Bash");
    const asked = screen.getByText(text);
    const link = screen.getByRole("link", { name: "Open in Claude" });
    expect(reason.compareDocumentPosition(asked) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(asked.compareDocumentPosition(link) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("Open in Claude is a link to remote_url that opens a new tab without an opener or a referrer", () => {
    setup();
    const link = screen.getByRole("link", { name: "Open in Claude" });
    expect(link).toHaveAttribute("href", "https://claude.ai/code/webshot2");
    expect(link).toHaveAttribute("target", "_blank");
    expect(link.getAttribute("rel")).toContain("noopener");
    expect(link.getAttribute("rel")).toContain("noreferrer");
    expect(screen.queryByRole("button", { name: "Open in Claude" })).toBeNull();
  });

  it("Open in Claude is the primary action when the session has no asks and secondary when it has", () => {
    const first = setup();
    expect(screen.getByRole("link", { name: "Open in Claude" })).toHaveClass("bg-primary");
    first.unmount();
    setup({ asks: [permission] });
    const link = screen.getByRole("link", { name: "Open in Claude" });
    expect(link).toHaveClass("bg-secondary");
    expect(link).not.toHaveClass("bg-primary");
  });

  it("says Remote Control is off when there is no remote_url, and draws no link", () => {
    setup({ remote_url: "" });
    expect(screen.getByText("Remote Control is off for this session.")).toBeInTheDocument();
    expect(screen.queryByRole("link")).toBeNull();
  });

  it("an unknown ask kind still leaves the way into Claude", () => {
    setup({ asks: [{ ...permission, kind: "plan" as Ask["kind"] }] });
    expect(screen.getByText(/This kind of ask is answered in Claude\./)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Allow" })).toBeNull();
    expect(screen.getByRole("link", { name: "Open in Claude" })).toBeInTheDocument();
  });
});
