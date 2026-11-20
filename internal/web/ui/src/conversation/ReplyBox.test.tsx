import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api } from "@/api/request";
import "@/commands";
import { toastManager } from "@/components/ui/toast";
import { MAX_MESSAGE_BYTES } from "@/lib/utf8";
import { useDrafts } from "@/store/drafts";
import { makeSession, noticesOk, resetFleet, seed } from "@/commands/testkit";
import type { Session } from "@/api/gen";

import { ReplyBox } from "./ReplyBox";

const QUICK = ["yes", "continue", "/compact", "/clear"];

function setup(over: Partial<Session> = { status: "Running", group: "working" }) {
  const session = makeSession({ id: 2, ...over });
  seed([session]);
  return render(<ReplyBox session={session} />);
}

const box = (): HTMLTextAreaElement => screen.getByRole("textbox", { name: "Reply" });
const send = (): HTMLElement => screen.getByRole("button", { name: "send" });

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

describe("ReplyBox", () => {
  it("draws a textarea labelled Reply with the placeholder, a Send button and the quick replies while it is empty", () => {
    setup();
    expect(box()).toHaveAttribute("placeholder", "reply, or /command…");
    expect(send()).toBeInTheDocument();
    const group = screen.getByRole("group", { name: "Quick replies" });
    for (const q of QUICK)
      expect(within(group).getByRole("button", { name: q })).toBeInTheDocument();
  });

  it("a quick reply fills the box and the chips go", async () => {
    setup();
    await userEvent.click(screen.getByRole("button", { name: "/compact" }));
    expect(box().value).toBe("/compact");
    expect(useDrafts.getState().replies[2]).toBe("/compact");
    expect(screen.queryByRole("group", { name: "Quick replies" })).toBeNull();
  });

  it("keeps the draft across a remount", async () => {
    const { unmount } = setup();
    await userEvent.type(box(), "half a thought");
    unmount();
    render(<ReplyBox session={makeSession({ id: 2, status: "Running", group: "working" })} />);
    expect(box().value).toBe("half a thought");
  });

  it("an emptied box deletes the draft, and a different session's draft is its own", async () => {
    useDrafts.getState().setReply(3, "other");
    setup();
    await userEvent.type(box(), "x");
    expect(useDrafts.getState().replies[2]).toBe("x");
    await userEvent.clear(box());
    expect(useDrafts.getState().replies).not.toHaveProperty("2");
    expect(useDrafts.getState().replies[3]).toBe("other");
  });

  it("send is off for an empty or blank reply and on for text", () => {
    setup();
    expect(send()).toBeDisabled();
    fireEvent.change(box(), { target: { value: "   " } });
    expect(send()).toBeDisabled();
    fireEvent.change(box(), { target: { value: "yes" } });
    expect(send()).toBeEnabled();
  });

  it("send sends the typed text through session.send and a 200 empties the box", async () => {
    const spy = vi.spyOn(api, "send").mockResolvedValue(noticesOk());
    setup();
    fireEvent.change(box(), { target: { value: "go on" } });
    await userEvent.click(send());
    expect(spy).toHaveBeenCalledWith(2, { text: "go on" });
    await waitFor(() => expect(box().value).toBe(""));
  });

  it("a reply of exactly 64 KiB can be sent and one byte more cannot", () => {
    setup();
    fireEvent.change(box(), { target: { value: "a".repeat(MAX_MESSAGE_BYTES) } });
    expect(send()).toBeEnabled();
    expect(screen.queryByText(/Too long/)).toBeNull();
    fireEvent.change(box(), { target: { value: "a".repeat(MAX_MESSAGE_BYTES + 1) } });
    expect(send()).toBeDisabled();
    expect(screen.getByText("Too long: a reply is at most 64 KiB.")).toBeInTheDocument();
  });

  it("the limit counts UTF-8 bytes, not characters", () => {
    setup();
    fireEvent.change(box(), { target: { value: "é".repeat(32769) } });
    expect(send()).toBeDisabled();
    expect(screen.getByText("Too long: a reply is at most 64 KiB.")).toBeInTheDocument();
  });

  it("says to resume while the session's group is paused", () => {
    setup({ status: "Paused", group: "paused" });
    expect(screen.getByText("Resume the session to reply.")).toBeInTheDocument();
    expect(screen.queryByRole("textbox")).toBeNull();
  });

  it("draws nothing for a session that cannot take a reply", () => {
    const { container } = setup({ status: "Loading", group: "working" });
    expect(container).toBeEmptyDOMElement();
  });
});
