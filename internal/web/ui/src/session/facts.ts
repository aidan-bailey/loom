import type { Pr, Session } from "@/api/gen";

// parityText words the TUI's parity token: "" while parity is unknown, "✓ even" when the branch is
// level with its upstream, else "↑<ahead> ↓<behind>".
export function parityText(session: Session): string {
  if (!session.parity_known) return "";
  if (session.ahead === 0 && session.behind === 0) return "✓ even";
  return `↑${session.ahead} ↓${session.behind}`;
}

// checksText words a PR: its state unless it is open, else its checks.
export function checksText(pr: Pr): string {
  if (pr.state !== "open") return pr.state;
  switch (pr.checks) {
    case "passing":
      return "✓ checks pass";
    case "pending":
      return "checks pending";
    case "failing":
      return "✗ checks fail";
    case "none":
      return "no checks";
    default:
      return "";
  }
}

const GLYPHS: Record<string, string> = {
  needs_you: "●",
  your_turn: "◆",
  working: "◐",
  paused: "○",
};

// groupGlyph is a group's glyph; a group this build does not know gets Paused's.
export function groupGlyph(group: string): string {
  return GLYPHS[group] ?? "○";
}

const TONES: Record<string, string> = {
  needs_you: "text-attention",
  your_turn: "text-turn",
  working: "text-working",
};

// groupTone is the token class of a group's glyph.
export function groupTone(group: string): string {
  return TONES[group] ?? "text-muted-foreground";
}
