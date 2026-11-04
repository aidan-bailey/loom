# PWA: a quieter fleet screen

Date: 2026-11-04. Branch `aidanb/pwm-dash-too-busy`. Front-end only: no daemon, wire or fixture change.

## Problem

The phone app's landing page (the fleet, `internal/web/static/js/fleet.mjs`) spends about 200px before its first session on two blocks:

- **Count tiles** (Needs you / Your turn / Working, with "oldest 52m", "1 new", "1 agent"). The same three counts are already in the pinned status row and in each group heading, so they show three times.
- **Usage boxes**, one per account with plan limits: 5h and 7d share, an 8-cell bar and a reset time.

The owner kept everything else on the page (status-row counts, workspace chips, group headings, and every part of a row).

## Decisions

1. **The count tiles go.** The status row's counts already open each group's next session. Their subtexts are on the rows already: the oldest wait is the first Needs-you row's age, "new" is the row's NEW mark, and running agents are in the row's meta line. `Tile`, `scrollToGroup` and the group headings' `id="g-…"` (only the tiles scrolled to them) go with them.
2. **The usage boxes go.** `UsageBoxes`, `Window` and `Bar` leave `fleet.mjs`; the fleet starts at the workspace chips (or at the first group when there is one workspace).
3. **Each session row shows its account's share, left of its age**, on the title line: `flow-docs  62%  52m`. No extra line.
   - The share is the entry `usageFor(snap, s.account)` returns (`js/usage.mjs`; `""` is the default account), its 5h window, or its 7d window when there is no 5h, as the session screen's Account tile already picks. Text is `shareText` ("62%", or "reset" once the window reset).
   - Only a Claude session (`s.claude`) shows it: another program records no account, and `usageFor` would read its `""` as the default account's.
   - No entry, or an entry with neither window, shows nothing.
   - Colour: dim like the age; amber (`--attention`) at 80% or more of a window that has not reset; a stale sample (`isStale` against `usage_stale_after_s`) stays dim whatever its share.
4. **The session screen's Account tile carries what the fleet dropped.** Its figure stays the 5h share (7d when there is no 5h). Its sub becomes two lines: the account, the shown window and its reset (`default 5h · 1h40m`), then the other window's share and reset when it has one (`7d 31% · Sat`). A reset part is left out when `resetText` is empty or the window has reset.
5. **Accepted loss:** an account no Claude session runs on shows its usage nowhere in the app. The TUI's usage strip and Settings → Accounts still show it.

## Styling

- Drop the CSS only the removed pieces used: `.sc-tile-n`, `.sc-tile-needs_you…`, `.sc-tile-your_turn…`, `.sc-zero…`, `.sc-usage`, `.sc-usage-box…`, `.sc-win…`, `.sc-bar…`, and the fleet-only `.sc-tiles` rule if the session screen's own rule covers it. Keep what the session screen's tiles use (`.sc-tiles`, `.sc-tile-fig`, `.sc-tile-sub`, `.sc-stale .sc-tile-fig`).
- The row's share is a new `.sc-use` span styled like `.sc-age` (dim, 12px, no wrap), with `.sc-use.sc-attn` for amber.
- The Account tile's second sub line may wrap no further than the tile's existing ellipsis.

## Docs

- `internal/web/README.md`: the skin paragraph's "the fleet lists the `shown` workspaces under count tiles and the usage boxes" becomes the share on each row; the session screen's Account tile gains its reset and the other window.
- `USAGE.md`: the phone section's fleet bullet drops the tiles and usage boxes and says each row shows its account's share; the session bullet's usage tile gains the reset and the 7d share.
- `usage.mjs`'s header comment ("The fleet draws the boxes and the session screen its tile") follows.

## Verification

- `node --check` on every changed module.
- `CGO_ENABLED=0 go test ./internal/web/...` (the static tests resolve every reference and named import, refuse style attributes, and check the stylesheet's braces), plus `go run ./tools/claudemd`.
- `go run ./tools/webshot -out <scratch dir>` and look at `fleet.png`, `session.png` and `waiting.png`: no tiles or usage boxes; shares on the Claude rows of the fixture (`default` 62%, `work` 18%); the Account tile's two sub lines.
