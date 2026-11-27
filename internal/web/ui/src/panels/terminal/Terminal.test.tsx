import { act, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";

import type { Session } from "@/api/gen";
import type { PanelProps } from "@/app/panels";
import { usePalette, usePaletteKey } from "@/desktop/paletteState";
import { fixtureSnapshot } from "@/fixtures";
import { allSessions } from "@/fleet/groups";
import { useFleet } from "@/store/fleet";
import { frameTerminal, type FrameState, type LoomTerminal } from "@/terminal/frame";
import { CLOSE_EXITED, CLOSE_OFF, CLOSE_REVOKED } from "@/terminal/socket";
import { setViewportWidth } from "@/test-layout";

import { markInspectorPress, Terminal, TerminalBody, terminalApplies } from "./Terminal";

vi.mock("@/terminal/frame", () => ({ frameTerminal: vi.fn() }));

// A LoomTerminal of vi.fn() members that `frameTerminal` answers; `emit` gives the panel a state
// as the frame would, and `chord` is ⌘K pressed inside the frame. Story 5's tests use it too.
export type FakeTerminal = { [K in keyof LoomTerminal]: Mock<LoomTerminal[K]> } & {
  emit(state: FrameState): void;
  chord(): void;
};

export function fakeTerminal(
  initial: FrameState = { phase: "connecting", renderer: "dom" },
): FakeTerminal {
  let state = initial;
  let stateCb: (next: FrameState) => void = () => {};
  let chordCb: () => void = () => {};
  const fake: FakeTerminal = {
    send: vi.fn(() => true),
    key: vi.fn(() => true),
    text: vi.fn(() => ""),
    setTouch: vi.fn(),
    setLink: vi.fn(),
    reconnect: vi.fn(),
    focus: vi.fn(),
    close: vi.fn(),
    onState: vi.fn((cb: (next: FrameState) => void) => {
      stateCb = cb;
      cb(state);
    }),
    onChord: vi.fn((cb: () => void) => {
      chordCb = cb;
    }),
    emit(next) {
      state = next;
      act(() => {
        stateCb(next);
      });
    },
    chord() {
      chordCb();
    },
  };
  vi.mocked(frameTerminal).mockReturnValue(fake);
  return fake;
}

const LIVE: FrameState = { phase: "live", renderer: "webgl" };
const WAITING: FrameState = { phase: "waiting", retryAt: 0, renderer: "dom" };
const PROPS: PanelProps = { sessionId: 7, params: { id: "7" }, search: {} };

beforeEach(() => {
  vi.mocked(frameTerminal).mockReset();
  useFleet.setState({ snapshot: null, stale: false, link: "live", updated: 0 });
});

afterEach(() => {
  useFleet.setState({ snapshot: null, stale: true, link: "connecting", updated: 0 });
  usePalette.getState().setOpen(false);
});

function frameEl(): HTMLIFrameElement {
  const el = screen.getByTitle("Terminal");
  if (!(el instanceof HTMLIFrameElement)) throw new Error("the terminal's frame is not an iframe");
  return el;
}

function rootEl(): HTMLElement {
  const el = document.querySelector<HTMLElement>('[data-slot="terminal"]');
  if (el === null) throw new Error("no terminal root");
  return el;
}

// load is the frame's load event, after which the panel takes the interface.
function load(): void {
  fireEvent.load(frameEl());
}

// mount draws the panel and loads its frame, with a fake interface behind it.
function mount(fake = fakeTerminal()) {
  const view = render(<Terminal {...PROPS} />);
  load();
  return { fake, view };
}

describe("Terminal's frame", () => {
  it("draws the frame at /terminal.html with the session's id, titled Terminal", () => {
    render(<Terminal {...PROPS} />);
    expect(frameEl()).toHaveAttribute("src", "/terminal.html#7");
    expect(rootEl()).toContainElement(frameEl());
  });

  it("carries no state until the frame gives one, and says Connecting…", () => {
    const fake = fakeTerminal();
    render(<Terminal {...PROPS} />);
    expect(rootEl()).not.toHaveAttribute("data-terminal-state");
    expect(screen.getByText("Connecting…")).toBeInTheDocument();
    expect(fake.onState).not.toHaveBeenCalled();
  });

  it("takes the interface when the frame loads and carries the phase it gives", () => {
    const { fake } = mount();
    expect(fake.onState).toHaveBeenCalledTimes(1);
    expect(rootEl()).toHaveAttribute("data-terminal-state", "connecting");
    fake.emit(LIVE);
    expect(rootEl()).toHaveAttribute("data-terminal-state", "live");
    fake.emit(WAITING);
    expect(rootEl()).toHaveAttribute("data-terminal-state", "waiting");
  });

  it("says the terminal did not load when the frame has no interface", () => {
    vi.mocked(frameTerminal).mockReturnValue(null);
    render(<Terminal {...PROPS} />);
    load();
    expect(screen.getByText("The terminal did not load.")).toBeInTheDocument();
    expect(rootEl()).not.toHaveAttribute("data-terminal-state");
  });
});

describe("Terminal's states", () => {
  it("shows nothing over a live terminal", () => {
    const { fake } = mount();
    fake.emit(LIVE);
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("says Connecting… while connecting and Reconnecting… while waiting", () => {
    const { fake } = mount();
    expect(screen.getByRole("status")).toHaveTextContent("Connecting…");
    fake.emit(WAITING);
    expect(screen.getByRole("status")).toHaveTextContent("Reconnecting…");
    expect(screen.queryByRole("button", { name: "Start a new shell" })).toBeNull();
  });

  it("shows a close's reason, and no button for any ending but the shell's exit", () => {
    const { fake } = mount();
    fake.emit({
      phase: "closed",
      closed: { code: CLOSE_OFF, reason: "the terminal is off on this host" },
      renderer: "dom",
    });
    expect(screen.getByRole("status")).toHaveTextContent("the terminal is off on this host");
    expect(screen.queryByRole("button", { name: "Start a new shell" })).toBeNull();
    fake.emit({
      phase: "closed",
      closed: { code: CLOSE_REVOKED, reason: "this device is no longer paired" },
      renderer: "dom",
    });
    expect(screen.getByRole("status")).toHaveTextContent("this device is no longer paired");
    expect(screen.queryByRole("button", { name: "Start a new shell" })).toBeNull();
  });

  it("shows the failure of a terminal that could not start as a reason, with no button (spec §68)", () => {
    const { fake } = mount();
    fake.emit({
      phase: "closed",
      closed: { code: 0, reason: "The terminal could not start: out of memory" },
      renderer: "dom",
    });
    expect(rootEl()).toHaveAttribute("data-terminal-state", "closed");
    expect(screen.getByRole("status")).toHaveTextContent(
      "The terminal could not start: out of memory",
    );
    expect(screen.queryByRole("button", { name: "Start a new shell" })).toBeNull();
  });

  it("offers Start a new shell after the shell exited, which connects again", async () => {
    const { fake } = mount();
    fake.emit({
      phase: "closed",
      closed: { code: CLOSE_EXITED, reason: "the shell exited" },
      renderer: "dom",
    });
    expect(screen.getByRole("status")).toHaveTextContent("the shell exited");
    await userEvent.click(screen.getByRole("button", { name: "Start a new shell" }));
    expect(fake.reconnect).toHaveBeenCalledTimes(1);
  });
});

describe("Terminal's touch mode and link", () => {
  it("gives the phone's layout touch mode and a desktop's none", () => {
    const { fake } = mount();
    expect(fake.setTouch).toHaveBeenLastCalledWith(true);

    setViewportWidth(1280);
    expect(fake.setTouch).toHaveBeenLastCalledWith(false);
    setViewportWidth(390);
    expect(fake.setTouch).toHaveBeenLastCalledWith(true);
  });

  it("gives the frame the link: up while the snapshot is fresh, down while it is stale", () => {
    const { fake } = mount();
    expect(fake.setLink).toHaveBeenLastCalledWith(true);
    act(() => {
      useFleet.setState({ stale: true, link: "offline" });
    });
    expect(fake.setLink).toHaveBeenLastCalledWith(false);
    act(() => {
      useFleet.setState({ stale: false, link: "live" });
    });
    expect(fake.setLink).toHaveBeenLastCalledWith(true);
  });
});

describe("Terminal's unmount", () => {
  it("closes the terminal once when the panel unmounts", () => {
    const { fake, view } = mount();
    view.unmount();
    expect(fake.close).toHaveBeenCalledTimes(1);
  });

  it("closes it before the frame leaves the document", () => {
    const fake = fakeTerminal();
    const view = render(<Terminal {...PROPS} />);
    const frame = frameEl();
    load();
    let attached: boolean | undefined;
    vi.mocked(fake.close).mockImplementation(() => {
      attached = frame.isConnected;
    });
    view.unmount();
    expect(attached).toBe(true);
    expect(frame.isConnected).toBe(false);
  });

  it("unmounts even when the close throws", () => {
    const { fake, view } = mount();
    vi.mocked(fake.close).mockImplementation(() => {
      throw new Error("the frame's document is gone");
    });
    expect(() => {
      view.unmount();
    }).not.toThrow();
    expect(fake.close).toHaveBeenCalledTimes(1);
  });

  it("closes nothing when the frame never gave an interface", () => {
    const fake = fakeTerminal();
    vi.mocked(frameTerminal).mockReturnValue(null);
    const view = render(<Terminal {...PROPS} />);
    load();
    view.unmount();
    expect(fake.close).not.toHaveBeenCalled();
  });
});

describe("Terminal's chord", () => {
  it("dispatches ⌘K on the app's document, where the palette's one listener answers", () => {
    function Listener(): null {
      usePaletteKey();
      return null;
    }
    const heard: KeyboardEvent[] = [];
    const record = (e: Event): void => {
      if (e instanceof KeyboardEvent) heard.push(e);
    };
    const { fake } = mount();
    render(<Listener />);
    document.addEventListener("keydown", record);
    try {
      expect(usePalette.getState().open).toBe(false);
      act(() => {
        fake.chord();
      });
    } finally {
      document.removeEventListener("keydown", record);
    }
    expect(heard).toHaveLength(1);
    expect(heard[0]).toMatchObject({ key: "k", metaKey: true, bubbles: true });
    expect(usePalette.getState().open).toBe(true);
  });
});

describe("Terminal's focus on the desktop (spec §57, §67)", () => {
  beforeEach(() => {
    setViewportWidth(1280);
  });

  const added: HTMLElement[] = [];
  afterEach(() => {
    for (const el of added.splice(0)) el.remove();
    Reflect.deleteProperty(document, "activeElement");
  });

  function focusedElement(tag: "input" | "button"): HTMLElement {
    const el = document.createElement(tag);
    document.body.appendChild(el);
    el.focus();
    expect(document.activeElement).toBe(el);
    added.push(el);
    return el;
  }

  // frameHasTheFocus makes the document name the frame as its focused element, as a browser does
  // while the focus is inside a frame; nothingHasTheFocus makes it name none.
  function frameHasTheFocus(): void {
    Object.defineProperty(document, "activeElement", { configurable: true, get: () => frameEl() });
  }
  function nothingHasTheFocus(): void {
    Object.defineProperty(document, "activeElement", { configurable: true, get: () => null });
  }

  // reconnected is a later live: the connection ends and a new one goes live.
  function reconnected(fake: FakeTerminal): void {
    fake.emit(WAITING);
    fake.emit(LIVE);
  }

  it("takes no focus before the terminal is live: not on taking the interface, not while waiting", () => {
    const { fake } = mount();
    expect(fake.focus).not.toHaveBeenCalled();
    fake.emit(WAITING);
    expect(fake.focus).not.toHaveBeenCalled();
  });

  it("focuses a terminal the user brought up at its first live", () => {
    const { fake } = mount();
    fake.emit(LIVE);
    expect(fake.focus).toHaveBeenCalledTimes(1);
  });

  it("takes the focus from no text field at its first live", () => {
    focusedElement("input");
    const { fake } = mount();
    fake.emit(LIVE);
    expect(fake.focus).not.toHaveBeenCalled();
  });

  it("takes the focus at its first live from a control that is not a text field", () => {
    focusedElement("button");
    const { fake } = mount();
    fake.emit(LIVE);
    expect(fake.focus).toHaveBeenCalledTimes(1);
  });

  it("refocuses at a later live while the frame has the focus", () => {
    const { fake } = mount();
    fake.emit(LIVE);
    expect(fake.focus).toHaveBeenCalledTimes(1);
    frameHasTheFocus();
    reconnected(fake);
    expect(fake.focus).toHaveBeenCalledTimes(2);
  });

  it("takes no focus at a later live while the body, a control, a text field or nothing has it", () => {
    const { fake } = mount();
    fake.emit(LIVE);
    expect(fake.focus).toHaveBeenCalledTimes(1);

    // The body has the focus (the document's default).
    reconnected(fake);
    expect(fake.focus).toHaveBeenCalledTimes(1);

    focusedElement("button");
    reconnected(fake);
    expect(fake.focus).toHaveBeenCalledTimes(1);

    focusedElement("input");
    reconnected(fake);
    expect(fake.focus).toHaveBeenCalledTimes(1);

    nothingHasTheFocus();
    reconnected(fake);
    expect(fake.focus).toHaveBeenCalledTimes(1);
  });

  it("takes none at its first live in the inspector, from a stored choice, and refocuses at a later live while the frame has the focus", () => {
    const fake = fakeTerminal();
    render(
      <aside data-slot="inspector">
        <Terminal {...PROPS} />
      </aside>,
    );
    load();
    fake.emit(LIVE);
    expect(fake.focus).not.toHaveBeenCalled();
    frameHasTheFocus();
    reconnected(fake);
    expect(fake.focus).toHaveBeenCalledTimes(1);
  });

  it("takes it in the inspector once its tab was pressed, and only for the terminal that press mounts", () => {
    const fake = fakeTerminal();
    markInspectorPress();
    const first = render(
      <aside data-slot="inspector">
        <Terminal {...PROPS} />
      </aside>,
    );
    load();
    fake.emit(LIVE);
    expect(fake.focus).toHaveBeenCalledTimes(1);
    first.unmount();

    const again = fakeTerminal();
    render(
      <aside data-slot="inspector">
        <Terminal {...PROPS} />
      </aside>,
    );
    load();
    again.emit(LIVE);
    expect(again.focus).not.toHaveBeenCalled();
  });

  it("takes no focus on the phone, where the keyboard must stay down", () => {
    setViewportWidth(390);
    const { fake } = mount();
    fake.emit(LIVE);
    frameHasTheFocus();
    reconnected(fake);
    expect(fake.focus).not.toHaveBeenCalled();
  });
});

function running(over: Partial<Session> = {}): Session {
  const base = allSessions(fixtureSnapshot())[0];
  if (base === undefined) throw new Error("the fixture holds no session");
  return { ...base, id: 7, status: "Running", group: "working", ...over };
}

// hostWith puts the sessions in the store's snapshot, with the host's terminal switch as given.
function hostWith(terminal: boolean, ...sessions: Session[]): void {
  useFleet.setState({
    snapshot: {
      ...fixtureSnapshot(),
      terminal,
      sessions: { "1": sessions },
    },
    stale: false,
    link: "live",
    updated: Date.now(),
  });
}

describe("terminalApplies", () => {
  it.each([
    ["a running session", true, { status: "Running", group: "working" }, true],
    ["a ready session in your turn", true, { status: "Ready", group: "your_turn" }, true],
    ["a prompting session", true, { status: "Prompting", group: "needs_you" }, true],
    ["the host's switch off", false, { status: "Running", group: "working" }, false],
    ["a paused session", true, { status: "Paused", group: "paused" }, false],
    ["a recoverable session", true, { status: "Recoverable", group: "paused" }, false],
    ["a ready session not yet started", true, { status: "Ready", group: "paused" }, false],
    ["a loading session", true, { status: "Loading", group: "working" }, false],
    ["a deleting session", true, { status: "Deleting", group: "working" }, false],
  ] as const)("%s", (_name, terminal, over, want) => {
    expect(terminalApplies(running(over), { terminal })).toBe(want);
  });
});

describe("TerminalBody", () => {
  it("draws the terminal for a session it applies to", () => {
    hostWith(true, running());
    render(<TerminalBody {...PROPS} />);
    expect(frameEl()).toHaveAttribute("src", "/terminal.html#7");
  });

  it("says the terminal is off on this host, with no frame", () => {
    hostWith(false, running());
    render(<TerminalBody {...PROPS} />);
    expect(screen.getByRole("status")).toHaveTextContent("The terminal is off on this host");
    expect(screen.queryByTitle("Terminal")).toBeNull();
  });

  it("says the session is not running when the host's terminal is on", () => {
    hostWith(true, running({ status: "Paused", group: "paused" }));
    render(<TerminalBody {...PROPS} />);
    expect(screen.getByRole("status")).toHaveTextContent("This session is not running");
    expect(screen.queryByTitle("Terminal")).toBeNull();
  });

  it("says the session is gone when the snapshot does not list it", () => {
    hostWith(true, running({ id: 8 }));
    render(<TerminalBody {...PROPS} />);
    expect(screen.getByRole("status")).toHaveTextContent("This session is gone.");
    expect(screen.queryByTitle("Terminal")).toBeNull();
  });

  it("swaps the frame for the message when the host turns the terminal off, closing it", () => {
    hostWith(true, running());
    const fake = fakeTerminal();
    render(<TerminalBody {...PROPS} />);
    load();
    act(() => {
      useFleet.getState().apply("terminal", { enabled: false });
    });
    expect(screen.queryByTitle("Terminal")).toBeNull();
    expect(screen.getByRole("status")).toHaveTextContent("The terminal is off on this host");
    expect(fake.close).toHaveBeenCalledTimes(1);
  });

  it("gives another session a frame of its own", () => {
    hostWith(true, running(), running({ id: 8 }));
    const fake = fakeTerminal();
    const view = render(<TerminalBody {...PROPS} />);
    const first = frameEl();
    load();
    view.rerender(<TerminalBody sessionId={8} params={{ id: "8" }} search={{}} />);
    const second = frameEl();
    expect(second).not.toBe(first);
    expect(second).toHaveAttribute("src", "/terminal.html#8");
    expect(fake.close).toHaveBeenCalledTimes(1);
  });
});

describe("Terminal's phone input", () => {
  function commandLine(): HTMLElement {
    return screen.getByRole("textbox", { name: "Command line" });
  }

  it("draws the phone's input under the frame once the frame has given a state", () => {
    mount();
    const line = commandLine();
    expect(rootEl()).toContainElement(line);
    expect(frameEl().compareDocumentPosition(line) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("draws none on the desktop", () => {
    setViewportWidth(1280);
    mount();
    expect(screen.queryByRole("textbox", { name: "Command line" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Ctrl-C" })).toBeNull();
  });

  it("draws none before the frame has an interface", () => {
    fakeTerminal();
    render(<Terminal {...PROPS} />);
    expect(screen.queryByRole("textbox", { name: "Command line" })).toBeNull();
  });

  it("gives the input live from the phase", async () => {
    const { fake } = mount();
    const tab = screen.getByRole("button", { name: "Tab" });
    expect(tab).toBeDisabled();
    fake.emit(LIVE);
    expect(tab).toBeEnabled();
    await userEvent.setup().click(tab);
    expect(fake.key).toHaveBeenCalledWith("tab");
    fake.emit(WAITING);
    expect(tab).toBeDisabled();
  });

  it("gives the sheet the renderer the frame reports, and follows a change of it", async () => {
    const { fake } = mount();
    fake.emit({ phase: "live", renderer: "webgl" });
    await userEvent.setup().click(screen.getByRole("button", { name: "Select" }));
    await screen.findByRole("dialog");
    const footer = document.querySelector('[data-slot="drawer-footer"]');
    expect(footer).toHaveTextContent("Renderer: WebGL");
    fake.emit({ phase: "live", renderer: "dom" });
    expect(footer).toHaveTextContent("Renderer: DOM");
  });

  it("types into the shell through the interface", async () => {
    const { fake } = mount();
    fake.emit(LIVE);
    await userEvent.setup().type(commandLine(), "ls{Enter}");
    expect(fake.send).toHaveBeenCalledTimes(1);
    expect(Array.from(vi.mocked(fake.send).mock.calls[0]?.[0] ?? [])).toEqual(
      Array.from(new TextEncoder().encode("ls\r")),
    );
  });
});
