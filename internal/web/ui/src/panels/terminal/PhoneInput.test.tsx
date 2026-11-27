// The fake terminal's members are vi.fn() mocks that the tests pass to expect() unbound; none reads `this`.
/* eslint-disable @typescript-eslint/unbound-method */
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

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

  it("Enter that commits an IME candidate sends nothing", async () => {
    const terminal = fake();
    render(<PhoneInput terminal={terminal} live renderer="dom" />);
    const user = userEvent.setup();
    await user.type(line(), "ni");
    fireEvent.keyDown(line(), { key: "Enter", isComposing: true });
    expect(terminal.send).not.toHaveBeenCalled();
    expect(line().value).toBe("ni");
    fireEvent.keyDown(line(), { key: "Enter" });
    expect(terminal.send).toHaveBeenCalledTimes(1);
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

describe("PhoneInput's copy sheet", () => {
  function sheetText(): HTMLElement | null {
    return document.querySelector<HTMLElement>('[data-slot="terminal-text"]');
  }

  function selectButton(): HTMLElement {
    const el = document.querySelector<HTMLElement>('[data-slot="terminal-select"]');
    if (el === null) throw new Error("no Select button");
    expect(el).toHaveAccessibleName("Select");
    return el;
  }

  async function open(user: ReturnType<typeof userEvent.setup>): Promise<void> {
    await user.click(selectButton());
    await screen.findByRole("dialog");
  }

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("Select opens a sheet holding the buffer's text, read once, in a selectable pre", async () => {
    const terminal = fake();
    vi.mocked(terminal.text).mockReturnValue("line one\nline two");
    const view = render(<PhoneInput terminal={terminal} live renderer="dom" />);
    const user = userEvent.setup();
    await open(user);
    const pre = sheetText();
    expect(pre?.tagName).toBe("PRE");
    expect(pre?.textContent).toBe("line one\nline two");
    expect(pre).toHaveClass("select-text");
    expect(terminal.text).toHaveBeenCalledTimes(1);
    vi.mocked(terminal.text).mockReturnValue("changed");
    view.rerender(<PhoneInput terminal={terminal} live renderer="dom" />);
    expect(sheetText()?.textContent).toBe("line one\nline two");
    expect(terminal.text).toHaveBeenCalledTimes(1);
  });

  it("scrolls the text to its end when the sheet opens", async () => {
    vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockReturnValue(500);
    render(<PhoneInput terminal={fake()} live renderer="dom" />);
    await open(userEvent.setup());
    expect(sheetText()?.scrollTop).toBe(500);
  });

  it.each([
    ["webgl", "Renderer: WebGL"],
    ["dom", "Renderer: DOM"],
  ] as const)("names the renderer in the footer (%s)", async (renderer, label) => {
    render(<PhoneInput terminal={fake()} live renderer={renderer} />);
    await open(userEvent.setup());
    const footer = document.querySelector('[data-slot="drawer-footer"]');
    expect(footer).toHaveTextContent(label);
  });

  it("Done closes the sheet", async () => {
    render(<PhoneInput terminal={fake()} live renderer="dom" />);
    const user = userEvent.setup();
    await open(user);
    await user.click(screen.getByRole("button", { name: "Done" }));
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
  });

  it("reads the buffer again each time it opens, and scrolls the remounted text to its end again", async () => {
    vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockReturnValue(500);
    const terminal = fake();
    vi.mocked(terminal.text).mockReturnValue("first");
    render(<PhoneInput terminal={terminal} live renderer="dom" />);
    const user = userEvent.setup();
    await open(user);
    await user.click(screen.getByRole("button", { name: "Done" }));
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
    vi.mocked(terminal.text).mockReturnValue("second");
    await open(user);
    expect(sheetText()?.textContent).toBe("second");
    expect(terminal.text).toHaveBeenCalledTimes(2);
    expect(sheetText()?.scrollTop).toBe(500);
  });

  it("writes nothing to the clipboard", async () => {
    const writeText = vi.fn();
    Object.defineProperty(navigator, "clipboard", {
      value: { writeText },
      configurable: true,
    });
    try {
      render(<PhoneInput terminal={fake()} live renderer="dom" />);
      const user = userEvent.setup();
      await open(user);
      await user.click(screen.getByRole("button", { name: "Done" }));
      expect(writeText).not.toHaveBeenCalled();
    } finally {
      Reflect.deleteProperty(navigator, "clipboard");
    }
  });

  it("Select works while the terminal is not live", async () => {
    const terminal = fake();
    vi.mocked(terminal.text).mockReturnValue("kept");
    render(<PhoneInput terminal={terminal} live={false} renderer="dom" />);
    expect(selectButton()).toBeEnabled();
    await open(userEvent.setup());
    expect(sheetText()?.textContent).toBe("kept");
  });
});
