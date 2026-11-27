// The fake terminal's members are vi.fn() mocks that the tests pass to expect() unbound; none reads `this`.
/* eslint-disable @typescript-eslint/unbound-method */
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { LoomTerminal } from "@/terminal/frame";

import { PhoneInput } from "./PhoneInput";

function fake(): LoomTerminal {
  return {
    send: vi.fn(() => true),
    key: vi.fn(() => true),
    text: vi.fn(() => ""),
    setTouch: vi.fn(),
    setLink: vi.fn(),
    reconnect: vi.fn(),
    focus: vi.fn(),
    close: vi.fn(),
    onState: vi.fn(),
    onChord: vi.fn(),
  };
}

function bytes(call: unknown[]): number[] {
  return Array.from(call[0] as Uint8Array);
}

const KEY_NAMES = ["Ctrl-C", "Ctrl-D", "Tab", "Esc", "Up", "Down", "Left", "Right"];

function line(): HTMLInputElement {
  return screen.getByRole("textbox", { name: "Command line" });
}

describe("PhoneInput's keys", () => {
  it("sends each key's name through key() at once", async () => {
    const terminal = fake();
    render(<PhoneInput terminal={terminal} live renderer="dom" />);
    const user = userEvent.setup();
    for (const name of KEY_NAMES) {
      await user.click(screen.getByRole("button", { name }));
    }
    expect(vi.mocked(terminal.key).mock.calls.map((c) => c[0])).toEqual([
      "ctrl-c",
      "ctrl-d",
      "tab",
      "esc",
      "up",
      "down",
      "left",
      "right",
    ]);
    expect(terminal.send).not.toHaveBeenCalled();
  });

  it("disables every key until the terminal is live, and enables them when it is", () => {
    const terminal = fake();
    const view = render(<PhoneInput terminal={terminal} live={false} renderer="dom" />);
    for (const name of KEY_NAMES) {
      expect(screen.getByRole("button", { name })).toBeDisabled();
    }
    view.rerender(<PhoneInput terminal={terminal} live renderer="dom" />);
    for (const name of KEY_NAMES) {
      expect(screen.getByRole("button", { name })).toBeEnabled();
    }
  });

  it("a press on the row keeps the focus on the command line", async () => {
    const terminal = fake();
    render(<PhoneInput terminal={terminal} live renderer="dom" />);
    const user = userEvent.setup();
    await user.click(line());
    await user.click(screen.getByRole("button", { name: "Ctrl-C" }));
    expect(terminal.key).toHaveBeenCalledWith("ctrl-c");
    expect(document.activeElement).toBe(line());
  });
});

describe("PhoneInput's command line", () => {
  it("Enter sends the text as UTF-8 and a carriage return, clears the line and keeps the focus", async () => {
    const terminal = fake();
    render(<PhoneInput terminal={terminal} live renderer="dom" />);
    const user = userEvent.setup();
    await user.type(line(), "echo é{Enter}");
    expect(terminal.send).toHaveBeenCalledTimes(1);
    expect(bytes(vi.mocked(terminal.send).mock.calls[0] ?? [])).toEqual(
      Array.from(new TextEncoder().encode("echo é\r")),
    );
    expect(line().value).toBe("");
    expect(document.activeElement).toBe(line());
  });

  it("Enter on an empty line sends a bare carriage return", async () => {
    const terminal = fake();
    render(<PhoneInput terminal={terminal} live renderer="dom" />);
    const user = userEvent.setup();
    await user.click(line());
    await user.keyboard("{Enter}");
    expect(vi.mocked(terminal.send).mock.calls).toHaveLength(1);
    expect(bytes(vi.mocked(terminal.send).mock.calls[0] ?? [])).toEqual([13]);
  });

  it("keeps the text when send answers false, and clears it when a later one answers true", async () => {
    const terminal = fake();
    vi.mocked(terminal.send).mockReturnValueOnce(false).mockReturnValue(true);
    render(<PhoneInput terminal={terminal} live renderer="dom" />);
    const user = userEvent.setup();
    await user.type(line(), "ls{Enter}");
    expect(line().value).toBe("ls");
    await user.keyboard("{Enter}");
    const calls = vi.mocked(terminal.send).mock.calls;
    expect(calls).toHaveLength(2);
    expect(bytes(calls[0] ?? [])).toEqual(bytes(calls[1] ?? []));
    expect(line().value).toBe("");
  });

  it("Enter does nothing while the terminal is not live, and keeps the text", async () => {
    const terminal = fake();
    const view = render(<PhoneInput terminal={terminal} live={false} renderer="dom" />);
    const user = userEvent.setup();
    await user.type(line(), "ls{Enter}");
    expect(terminal.send).not.toHaveBeenCalled();
    expect(line().value).toBe("ls");
    view.rerender(<PhoneInput terminal={terminal} live renderer="dom" />);
    await user.keyboard("{Enter}");
    expect(terminal.send).toHaveBeenCalledTimes(1);
    expect(line().value).toBe("");
  });

  it("the command line is a 16px field with every correction off", () => {
    render(<PhoneInput terminal={fake()} live renderer="dom" />);
    const field = line();
    expect(field).toHaveAttribute("type", "text");
    expect(field).toHaveAttribute("autocorrect", "off");
    expect(field).toHaveAttribute("autocapitalize", "none");
    expect(field).toHaveAttribute("autocomplete", "off");
    expect(field).toHaveAttribute("spellcheck", "false");
    expect(field).toHaveAttribute("enterkeyhint", "send");
    expect(field).toHaveClass("text-[16px]");
  });
});
