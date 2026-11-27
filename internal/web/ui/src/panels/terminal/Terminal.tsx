// The terminal panel's body (spec §39–§43, §55, §57, §61): the frame of `/terminal.html`, driven
// only through the interface `frameTerminal` reads off it (terminal/frame.ts), the frame's states
// drawn over it, and on the desktop the focus. The frame owns xterm.js, the socket and the
// reconnecting; nothing here reaches into the frame's document (ui_rules_test.go pins it).
import { useEffect, useLayoutEffect, useRef, useState, type ReactElement } from "react";

import type { Session } from "@/api/gen";
import type { HostFacts, PanelProps } from "@/app/panels";
import { isTextField } from "@/app/shell";
import { useLayout } from "@/app/viewport";
import { sessionActions } from "@/commands/session";
import { Button } from "@/components/ui/button";
import { NoSession } from "@/session/NoSession";
import { useHostFacts, useLink, useSession } from "@/store/fleet";
import { frameTerminal, type FrameState, type LoomTerminal } from "@/terminal/frame";
import { CLOSE_EXITED, describeClose } from "@/terminal/socket";

import { PhoneInput } from "./PhoneInput";

// terminalApplies is whether the session has a terminal tab on this host: the host's switch is on
// and the session is started, active and not paused. `sessionActions(...).diff` is that set (not
// busy, not in the paused group, which holds the Recoverable and unstarted sessions), as the
// changes tab uses it; the daemon's request refuses the rest, and a refusal closes the connection
// with its reason.
export function terminalApplies(session: Session, host: HostFacts): boolean {
  return host.terminal && sessionActions(session).diff;
}

// The inspector's terminal tab was pressed (spec §57): the terminal that press mounts takes the
// focus, and one the inspector mounts from a stored choice does not. The tab's button marks it
// (desktop/Inspector.tsx), the terminal it mounts takes the mark at its first commit, and the
// inspector takes it again at the end of the same commit, so a press whose terminal does not mount
// (the boundary's fallback, a session that does not run) leaves no mark for a later terminal.
let inspectorPressed = false;

// markInspectorPress records that the user pressed the inspector's terminal tab.
export function markInspectorPress(): void {
  inspectorPressed = true;
}

// takeInspectorPress is whether the inspector's terminal tab was pressed since the last take.
export function takeInspectorPress(): boolean {
  const was = inspectorPressed;
  inspectorPressed = false;
  return was;
}

// openPalette is the frame's chord (⌘K pressed inside it) as the app's own key event: the app's
// one key listener answers it on `document`, and the entry bundle imports nothing of the desktop
// chunk that holds the palette.
function openPalette(): void {
  document.dispatchEvent(new KeyboardEvent("keydown", { key: "k", metaKey: true, bubbles: true }));
}

// Notice is what the panel says over the frame: nothing while live, else the state, and for the
// shell's exit (4004) alone a button that connects again. A terminal that could not start is a
// `closed` state with code 0 (spec §68): its reason is shown as any other, with no button.
function Notice(props: {
  state: FrameState | null;
  failed: boolean;
  terminal: LoomTerminal | null;
}): ReactElement | null {
  const { state, failed, terminal } = props;
  let text: string;
  let again = false;
  if (failed) {
    text = "The terminal did not load.";
  } else if (state === null || state.phase === "connecting") {
    text = "Connecting…";
  } else if (state.phase === "waiting") {
    text = "Reconnecting…";
  } else if (state.phase === "closed") {
    text = state.closed === undefined ? "The terminal closed." : describeClose(state.closed);
    again = state.closed?.code === CLOSE_EXITED;
  } else {
    return null;
  }
  return (
    <div
      role="status"
      className="absolute inset-x-0 bottom-0 flex flex-wrap items-center gap-3 bg-card px-3 py-2 text-sm text-attention"
    >
      <span>{text}</span>
      {again && terminal !== null ? (
        <Button
          variant="secondary"
          size="lg"
          onClick={() => {
            terminal.reconnect();
          }}
        >
          Start a new shell
        </Button>
      ) : null}
    </div>
  );
}

// Terminal draws the session's shell: the frame, its interface once the frame has loaded, and the
// states over it. The session comes from its props only. It holds the interface and the latest
// state, which the phone's input (PhoneInput) is given from here.
export function Terminal(props: PanelProps): ReactElement {
  const phone = useLayout() === "phone";
  const { stale } = useLink();
  const root = useRef<HTMLDivElement>(null);
  const frame = useRef<HTMLIFrameElement>(null);
  const [terminal, setTerminal] = useState<LoomTerminal | null>(null);
  const [state, setState] = useState<FrameState | null>(null);
  const [failed, setFailed] = useState(false);
  // brought: whether the user brought the panel up (spec §57), decided once at the first commit.
  // In the middle column it is always so; in the inspector only the press of its tab does it.
  const brought = useRef<boolean | null>(null);
  const wasLive = useRef(false);
  const lived = useRef(false);

  useLayoutEffect(() => {
    if (brought.current !== null) return;
    const pressed = takeInspectorPress();
    const inInspector = (root.current?.closest('[data-slot="inspector"]') ?? null) !== null;
    brought.current = !inInspector || pressed;
  }, []);

  // The close runs in a layout effect's cleanup, which React runs before it removes the frame
  // from the document (spec §61), and in a `try`, since a call into a discarded document may throw.
  useLayoutEffect(() => {
    if (terminal === null) return;
    return () => {
      try {
        terminal.close();
      } catch {
        // The frame is gone already; its connection went with it.
      }
    };
  }, [terminal]);

  useEffect(() => {
    if (terminal === null) return;
    terminal.onState(setState);
    terminal.onChord(openPalette);
  }, [terminal]);

  useEffect(() => {
    terminal?.setTouch(phone);
  }, [terminal, phone]);

  useEffect(() => {
    terminal?.setLink(!stale);
  }, [terminal, stale]);

  // The focus (spec §57 as §67 narrows it). The panel never calls focus() before a live: until the
  // frame's terminal has started it would only be held. At the first live of a terminal the user
  // brought up it focuses unless a text field of the app has the focus then; at every other live
  // (a later one, or the first of a terminal that was not brought up) it focuses only while the
  // app's focused element is the frame itself, so a reconnect gives the focus back to a terminal
  // being typed in and never takes it from the body, the reply box or another control.
  const phase = state?.phase;
  useEffect(() => {
    const live = phase === "live";
    const entered = live && !wasLive.current;
    wasLive.current = live;
    if (!entered) return;
    const first = !lived.current;
    lived.current = true;
    if (terminal === null || phone) return;
    const active = document.activeElement;
    const focus =
      first && brought.current === true
        ? !isTextField(active)
        : frame.current !== null && active === frame.current;
    if (focus) terminal.focus();
  }, [phase, terminal, phone]);

  return (
    <div
      ref={root}
      data-slot="terminal"
      data-terminal-state={phase}
      className="flex min-h-[24rem] flex-1 flex-col"
    >
      <div className="relative flex min-h-0 flex-1 flex-col">
        <iframe
          ref={frame}
          src={`/terminal.html#${String(props.sessionId ?? "")}`}
          title="Terminal"
          onLoad={(e) => {
            const next = frameTerminal(e.currentTarget);
            setTerminal(next);
            setFailed(next === null);
          }}
          className="min-h-[50dvh] w-full flex-1 border-0 bg-background"
        />
        <Notice state={state} failed={failed} terminal={terminal} />
      </div>
      {phone && terminal !== null && state !== null ? (
        <PhoneInput terminal={terminal} live={phase === "live"} renderer={state.renderer} />
      ) : null}
    </div>
  );
}

// TerminalBody is the panel's Body: the terminal for a session it applies to, else why it does not
// (a direct link to the tab while the host's terminal is off, or the session is not running). The
// frame is keyed by the session, so another session gets a frame of its own.
export function TerminalBody(props: PanelProps): ReactElement {
  const session = useSession(props.sessionId);
  const host = useHostFacts();
  if (session === undefined) return <NoSession sessionId={props.sessionId} />;
  if (!terminalApplies(session, host)) {
    return (
      <p role="status" className="px-4 py-8 text-center text-muted-foreground">
        {host.terminal
          ? "This session is not running, so it has no terminal."
          : "The terminal is off on this host. loom web terminal on turns it on."}
      </p>
    );
  }
  return <Terminal key={session.id} {...props} />;
}
