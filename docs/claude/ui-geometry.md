# UI geometry: box sizing, truncation and mouse hit-tests

**Symptoms:** a pane's right or bottom border stops short of its neighbour's edge; a card grid row is taller than its siblings and the grid misaligns; a "solid" selected row renders striped; a mouse drag selects text one or two rows off, or nothing in a pane the user focused.

**Rule of thumb:** measure, don't reason. Render the string in a test and count cells (`ui/pane_border_test.go`, `ui/card_test.go` and `ui/overview_test.go` do). An earlier fix here was "verified by construction" and wrong.

## lipgloss v2 box sizing (`charm.land/lipgloss/v2`)

- **`.Width(W)` and `.Height(H)` set the whole box, border included.** `.Width(38)` on a left-and-right rounded border yields a 38-wide box with a 36-cell content area. Never subtract `GetHorizontalFrameSize()` from the width you pass: the box comes out that many columns too narrow, `JoinVertical(Left, …)` right-pads it with spaces, and its border visibly falls short of a sibling drawn at full width. This was the root cause of a long "the pane's right border is too short" saga, fixed in `ui/split_pane.go`'s `renderPane` by passing the full pane width; `TestPaneBorderReachesRightEdge` and `TestSplitPaneStringBordersReachRightEdge` pin it.
- **The same subtraction survives** in `ui/workspace_tab_bar.go` (the bordered tab style) and `ui/overlay/file_explorer.go`. They show no symptom today; measure before changing them. (`SplitPane.SetSize` subtracting the frame to size its children's content is correct: that is a content width, not a `.Width`.)
- **`.Height(H)` is a minimum, not a cap.** Taller content overflows rather than truncating, so over-wide content that wraps grows the box past `H`. Widen the content area so it stops wrapping, or pre-truncate rows.
- **`lipgloss.Place` grows on over-tall content too**; it never clips. Clamp assembled output (`clampHeight`, `ui/split_pane.go`).
- **Over-wide lines inside a `.Width(W)` bordered box wrap**, making that box taller than its row siblings and breaking `JoinHorizontal` grid alignment. Truncate the right-hand column first, then the left against the remainder; never compose and hope (`renderOverviewCard`, `ui/overview.go`).

## Styling and truncation

- **Backgrounds don't survive embedded SGR resets.** `bg.Render(line)` over text holding inner `Render` calls paints the background only outside the inner spans, since each ends with a reset and lipgloss doesn't re-inject the outer background. Put `.Background(...)` on each inner style and use the outer style only for width padding (`RenderCard`'s selected branch, `ui/card.go`; `TestRenderCard_SelectedRailBackgroundIsSolid`).
- **`runewidth.Truncate(s, w, tail)` already reserves the tail's width.** Passing `w-1` under-fills by one cell.

## Mouse hit-tests

- `SplitPane.HitTest(localX, y)` (`ui/split_pane.go`) maps a cell to pane content. Each pane box is a one-row title border plus a body bordered left, right and bottom (no top), so content starts at `+1` on both axes: agent content rows are `[1, 1+agentHeight)` and terminal rows start at `agentHeight+3`. Over the diff overlay it returns `ok=false`. `TestSplitPaneHitTest` (`ui/selection_hittest_test.go`) pins it.
- The app translates screen cells before calling it: `mouse.X - m.listWidth` and `mouse.Y - m.topChromeHeight()` (`app/app.go`, `app/interact.go`). The split pane renders below the top chrome, which is the workspace tab bar plus the account strip's row when an extra account exists; using `tabBar.Height()` alone offsets every selection by the strip's row. `TestTopChromeHeight_CountsTheStrip` (`app/top_chrome_test.go`) pins the chrome height.
- Selection works over the rendered text, not emulator cells: each pane keeps the ANSI-stripped lines it last rendered and `ui/selection.go` extracts and highlights against them.
- Mouse messages reach Update in every state. Selection must run in both `stateDefault` and `stateInlineAttach`, since the panes are the main view in both: a first cut gated clicks to `stateDefault` and drag-select silently failed whenever the user had focused a pane. The wheel has no state gate. Overview drops mouse input.
- In interact mode (`app/interact.go`) the left button is deferred: a drag becomes loom's own selection, copied to the clipboard; a plain click with no motion is forwarded to the agent as a press and release (`forwardClickToFocused` → `ForwardMouse`, whose SGR coordinates are 1-indexed where `HitTest`'s are 0-indexed). Drags are never forwarded: loom sets tmux `mouse on`, so a forwarded drag enters tmux's copy mode and fills tmux's paste buffer instead of reaching the agent.
- The clipboard is written by `tea.SetClipboard` (OSC 52, which tunnels over SSH) with a local fallback (`app/clipboard.go`). A dev sandbox does not isolate it: a drag-select in a sandboxed loom writes the host's clipboard.

## What the gates still won't tell you

The border tests cover the split pane and the card tests the rail and grid; a new bordered widget, or a new mouse surface, has no geometry test until you write one.
