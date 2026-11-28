// The terminal panel: the session's worktree shell, the third tab, shown while the host's terminal
// is on and the session runs. Its body is in the entry (a frame and its states; xterm.js stays in
// the frame's own document), so it adds no lazy chunk.
import { definePanel } from "@/app/panels";
import { SessionHead } from "@/session/SessionHead";

import { TerminalBody, terminalApplies } from "./Terminal";

export const terminalPanel = definePanel({
  id: "terminal",
  title: () => "Terminal",
  appliesTo: terminalApplies,
  tab: { order: 3, label: "terminal" },
  Head: SessionHead,
  Body: TerminalBody,
});
