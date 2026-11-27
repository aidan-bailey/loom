// The phone's input under the terminal's frame (spec §44, §55): on the phone the terminal takes no
// focus, so typing happens here. A row of keys sends a named key at once and a one-line command
// line sends its text and a carriage return; both wait for the frame's `live` phase, and nothing
// is queued, since the frame drops input it cannot send. Type imports only from the frame, so the
// entry bundle gains nothing of xterm.js.
import { useState, type ReactElement } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import type { KeyName, LoomTerminal, Renderer } from "@/terminal/frame";

// KEYS are the row's buttons in order: the key's name, its visible label and its accessible name.
const KEYS: { name: KeyName; label: string; aria: string }[] = [
  { name: "ctrl-c", label: "Ctrl-C", aria: "Ctrl-C" },
  { name: "ctrl-d", label: "Ctrl-D", aria: "Ctrl-D" },
  { name: "tab", label: "Tab", aria: "Tab" },
  { name: "esc", label: "Esc", aria: "Esc" },
  { name: "up", label: "↑", aria: "Up" },
  { name: "down", label: "↓", aria: "Down" },
  { name: "left", label: "←", aria: "Left" },
  { name: "right", label: "→", aria: "Right" },
];

const BUTTON = "min-h-[44px] min-w-[44px] shrink-0";

export function PhoneInput(props: {
  terminal: LoomTerminal;
  live: boolean;
  renderer: Renderer;
}): ReactElement {
  const { terminal, live } = props;
  const [line, setLine] = useState("");

  return (
    <div
      data-slot="terminal-phone-input"
      className="flex flex-col gap-2 border-t border-border bg-card px-3 py-2"
    >
      <div
        className="flex items-center gap-1.5"
        onMouseDown={(e) => {
          // A browser moves the focus on mouse-down (a touch screen on the mouse events it
          // synthesises after the tap); the click still fires, and the keyboard stays up.
          e.preventDefault();
        }}
      >
        <div className="flex min-w-0 flex-1 gap-1.5 overflow-x-auto">
          {KEYS.map((key) => (
            <Button
              key={key.name}
              type="button"
              variant="secondary"
              aria-label={key.aria}
              disabled={!live}
              className={BUTTON}
              onClick={() => {
                terminal.key(key.name);
              }}
            >
              {key.label}
            </Button>
          ))}
        </div>
      </div>
      <Input
        type="text"
        aria-label="Command line"
        placeholder="Command"
        autoCorrect="off"
        autoCapitalize="none"
        autoComplete="off"
        spellCheck={false}
        enterKeyHint="send"
        value={line}
        onChange={(e) => {
          setLine(e.target.value);
        }}
        onKeyDown={(e) => {
          if (e.key !== "Enter" || !live) return;
          if (terminal.send(new TextEncoder().encode(line + "\r"))) setLine("");
        }}
      />
    </div>
  );
}
