# Loom PWA: skin and glance

**Date:** 2026-10-23
**Status:** Approved design. Brainstormed with the user. Sub-project 1 of
the PWA upgrade below. The mockups are on a private page, the chosen skin
in its second row: https://claude.ai/artifact/Dv1GwHHRw8cJefivtMGvyh

## Problem

The PWA (`2026-10-21-loom-pwa-design.md`) shows the fleet and acts on it,
but it looks like a generic web form rather than loom, and it leaves out
facts the daemon already knows: how much of each account's plan is used,
which subagents a session runs, the pull request behind a branch, and the
last message as Claude wrote it (markdown, shown today as raw text). Its
workspace chips list every registered workspace, including ones nobody has
open, and every reply starts from an empty box.

This spec gives the app loom's own skin, adds those glances, makes the
chips follow loom's tabs, and adds quick replies the user defines.

## The upgrade, in three sub-projects

| # | Sub-project | Holds |
|---|---|---|
| 1 | **Skin and glance** (this spec) | The skin on every screen and a layout with room for 2 and 3; subscription usage, subagents by name, PR links, the last message as markdown, quick replies; workspace chips that follow loom's tabs |
| 2 | Read more and live activity | Recent conversation turns from the transcript, the per-file worktree diff, the tool Claude is running (a new PreToolUse hook) |
| 3 | Act more and notifications | Recover, resume with another model or account, a new session from a GitHub issue, quiet while at the desk, muting one session, an away digest |

Each gets its own spec, plan and build. This one leaves sub-project 2 its
entry points: the session screen's CHANGES tile will open the diff, an
"earlier turns" link above the last message the conversation, and the line
under the session's title will name the current tool.

## Findings

- **The open tabs already reach the web layer.** The TUI saves its tabs as
  the registry's open list (`Core.PersistOpenList`), `ModelView.Registry.Open`
  carries it resolved to registered workspaces in tab order, and the model
  publishes `ModelChanged` whenever its view differs (`core/state_publish.go`).
  The web hub already applies `ModelChanged` for devices. Several TUIs share
  one open list, written by whichever saved last; it survives the TUI's quit
  and the daemon's restart (`workspaces.json`).
- **Usage is probed every 2 minutes, and only while an account besides
  default is registered** (`maybeUsageProbe`, `usageInterval`). A failed
  probe keeps the last good sample and records the error. `AccountsView.Usage`
  reaches clients in `AccountsChanged`, which the web hub ignores today. The
  TUI dims a sample older than `ui.UsageStaleAfter` (4 minutes).
- **`github.PR` has no URL.** The PR query asks `gh pr list` for
  `number,headRefName,state,isDraft,reviewDecision,statusCheckRollup`; the
  issue queries already ask for `url`.
- **The last message has no length cap.** It is the parent's
  `last_assistant_message` from its latest Stop hook, and already rides
  every session's JSON as `last_message`.
- **goldmark is vendored** (v1.7.8, indirect through `charm.land/glamour`).
- **`web.json` is decoded leniently.** `loadWeb` uses `json.Unmarshal`, so an
  older build loads a file with a field it doesn't know and drops the field
  on its next write. A newer `version` it refuses outright, leaving the web
  off.
- **Subagents are already in the JSON** (`agents`: `name`, `description`,
  `idle`).

## Decisions

| # | Decision |
|---|---|
| 1 | **Skin:** loom's TUI on the phone (afterglow colours, monospace, glyph rows, a status line) with the dashboard's tiles: count tiles and usage boxes drawn as boxes whose label sits in their top border. Mockups: the second row of the page above. |
| 2 | **Theme:** dark only. One palette; `#2E2E2E` for the theme colour, the manifest and the splash. |
| 3 | **Font:** `ui-monospace, Menlo, monospace`, which is SF Mono on the iPhone. Nothing embedded. |
| 4 | **Workspaces shown:** a workspace gets a chip when it is open as a tab in loom (the open list) or has a session that isn't paused (in Needs you, Your turn or Working). With an empty open list (global mode, or never saved) every workspace is shown. The chips follow the tab order, and All covers the shown workspaces. |
| 5 | **Usage:** for each account with plan limits, the 5h and 7d windows: percent used, a bar, the reset time. Boxes on the fleet screen; on a session screen, its account's 5h in a tile. |
| 6 | **Last message as markdown:** the web layer parses it with goldmark into a tree of allowed nodes, and the app draws the tree as elements. No HTML string reaches the page. The session screen fetches the tree on demand. |
| 7 | **Quick replies:** the user's own list, edited in Settings, kept in `web.json` and shared by every paired phone; unset means the defaults `yes`, `continue`, `/compact`, `/clear`. A chip fills the reply box, and Send sends it. |
| 8 | **PR links:** the PR tile opens the pull request on GitHub, at the URL `gh` returns. |
| 9 | **Subagents by name:** each with its description, running or idle, on the session screen. |
| 10 | **Unchanged:** the actions and their confirmations, Open in Claude, the push kinds, pairing, and the stream's rules. Loom still answers no permission prompt (decision 5 of the PWA spec). |

## Architecture

Additive throughout: no `Core` method changes, the wire stays as it is
(`rpc.Protocol` unchanged), and the app's JSON only gains fields. Each new
route and event follows the checklist in `docs/claude/web-layer.md`.

### Daemon side

- `session/github`: the PR query asks for `url` too; `PR` gains `URL` and
  `State` gains `PRURL`, which `StateFor` copies.
- `core`:
  - `web.json` gains `quick_replies` (`[]string`, left out while unset).
    `webFileVersion` stays 1: an older build ignores the field, where a bump
    would make a downgrade cost the whole web.
  - `WebView.QuickReplies []string`: the stored list, nil while unset.
    `Clone` copies it.
  - `DefaultQuickReplies = []string{"yes", "continue", "/compact", "/clear"}`.
  - `WebHost.SetQuickReplies(replies []string) ([]string, error)`: nil
    restores the defaults (stores the list unset); otherwise it checks and
    stores the list (see Quick replies) and returns what it stored. A
    refusal wraps `ErrQuickReplies`.
  - `UsageStaleAfter = 2 * usageInterval`, exported, and
    `ui.UsageStaleAfter` becomes `core.UsageStaleAfter`, so the TUI and the
    phone dim a sample at the same age.

### Web layer (`internal/web`)

- The projection (`fleet.go`) gains the open list and the quick replies
  (from `ModelChanged`) and usage (from `AccountsChanged`).
- `workspaceJSON` gains `shown`, and the `workspaces` list is ordered: the
  open tabs in tab order, then the rest in serve order.
- `prJSON` gains `url`.
- New: `markdown.go` (the tree); the routes `GET /api/v1/sessions/{id}/message`
  and `PUT /api/v1/quick-replies`; the snapshot's `usage`,
  `usage_stale_after_s` and `quick_replies`; the stream events `usage` and
  `quick_replies`.

### App (`internal/web/static`)

- `app.css` rewritten: the tokens and components below, `color-scheme: dark`
  on `:root`, and no light block. `index.html` keeps one `theme-color`
  (`#2e2e2e`) and adds
  `<meta name="color-scheme" content="dark">`; the manifest's colours are
  `#2e2e2e`.
- `js/fleet.mjs`: the tiles, the usage boxes, chips by `shown`, the status
  line.
- `js/session.mjs`: the tiles, the needs-you box, the message, the agents,
  the actions, the reply box with its quick replies.
- `js/markdown.mjs`, new: draws a tree.
- `js/settings.mjs`: the quick-replies editor. `js/newsession.mjs`: the
  picker's groups. `js/app.mjs` and `js/api.mjs`: the new events, and the
  stream's state for the status line.

## The skin

- **Tokens** on `:root`, from afterglow: ground `#2E2E2E`, panel `#333435`,
  text `#d6d6d6`, dim `#a0a0a0` (5.2:1 on the ground), title `#ffffff`,
  border `#505050`, rule `#404040`, accent `#6c99bb`, amber `#e5b567` with
  its border `#9b7f50`, ok `#b4c973`, error `#e8836b`, workspace `#c98aa3`,
  selection `#5A647E`. Amber marks only what needs you.
- **Type:** `ui-monospace, Menlo, monospace`: 13px for text, 17px for
  titles, 11px for uppercase labels spaced 0.06 to 0.08em. Controls take
  `font: inherit`.
- **Components,** each a class:
  - the box with its label in its top border: the label is placed over the
    border with the ground as its background, so the box has no fill;
  - the tile: a box with a large figure and a sub-line;
  - the section rule (`NEEDS YOU 2 ────`);
  - glyph rows: ● needs you (amber), ◆ your turn (accent), ◐ working or a
    running agent (dim), ○ an idle agent;
  - chips, bordered, the selected one on the selection colour;
  - outline buttons (kill in the error colour) and the primary button
    (accent fill, ground text);
  - the 8-cell bar: `round(pct / 12.5)` cells of █ clamped to 0–8, the rest
    ░;
  - the fixed status line.
- **No `style` attribute** anywhere, since the CSP is `default-src 'self'`:
  a figure that varies, such as a bar, is text rather than a width.
- **Touch targets** at least 44px; **text contrast** at least 4.5:1.

## The fleet screen

Top to bottom, as the mockup "Fleet":

1. **Header:** `loom@<host>`, the settings link, `+ new`.
2. **Count tiles:** NEEDS YOU (amber), YOUR TURN (accent), WORKING, each
   with its count, dimmed at 0. Sub-lines: the oldest wait among the
   sessions needing you (`oldest 12m`, from `since`); how many your-turn
   sessions hold a turn this phone hasn't seen (`1 new`, `hasNewTurn`); how
   many subagents run in working sessions (`2 agents`, those not `idle`). A
   sub-line with nothing to count is left out. A tap scrolls to the tile's
   group (`scrollIntoView`) and leaves the route alone; a tile at 0 is not
   a link.
3. **Usage:** one box per account in `usage`, its name and plan in the
   border (the plan with a capital first letter). A row per window: `5h` or
   `7d`, the percent, the bar, the reset (`1h40m` within a day, else the
   weekday, `Thu`). A window whose reset has passed shows `reset` in place
   of the percent, an empty bar and no time. A sample older than
   `usage_stale_after_s` dims its box and adds its age. No section when
   `usage` is empty.
4. **Workspace chips:** All, then the shown workspaces in order, only while
   more than one is shown. The selected chip filters the tiles, the groups
   and the status line's counts; the usage boxes are per account and stay.
5. **Groups:** Needs you, Your turn and Working as today's cards, drawn as
   the skin's rows: the wait reason and its detail box, the last message
   clamped to two lines, `+a −r`, `PR #n ✓`, the workspace. Paused stays
   collapsed.
6. **Status line,** fixed at the bottom: `LIVE` in the ok colour while the
   stream is up, `RESTARTING` (dim) within the restart grace after a bye,
   `OFFLINE` in the error colour otherwise; then the host, the number of
   sessions the fleet lists and how many of them are paused. The stale bar
   and its words stay as they are.

## The session screen

As the mockup "Session, your turn", top to bottom:

1. **Bar:** `‹ fleet`; `claude ↗` (Open in Claude) when the session has a
   `remote_url` and doesn't need you.
2. **Header:** the title; under it the group's glyph, label and age, the
   workspace in its colour, and the branch.
3. **Needs you:** an amber box with the wait reason, its detail
   (preformatted), and Open in Claude as the primary button, or "Remote
   Control is off for this session."
4. **Tiles:**
   - CHANGES: `+a −r`; the sub-line is the TUI's parity token, `↑N ↓M`, or
     `✓ even` at 0 and 0, and nothing while parity is unknown.
   - PR: `#n`, linked to `pr.url` with ↗ when there is one. The sub-line is
     the checks of an open PR (`✓ checks pass`, `checks pending`,
     `✗ checks fail`, `no checks`), else `draft`, `merged` or `closed`.
   - ACCOUNT: the account's 5h percent, else its 7d; the sub-line names the
     account and the window (`personal 5h`).

   A tile whose data is missing is left out, and the row reflows.
5. **Last message:** the tree once fetched; the plain text before that, and
   if the fetch fails. When the tree is `truncated` it ends with
   `… continues in Claude`, linked when there is a `remote_url`.
6. **Agents:** a row per subagent: ◐ or ○, its name, `running` or `idle`,
   and its description under it. Left out when there are none.
7. **Actions:** pause or resume, push, merge and kill in one row, by
   `actionsFor` as today, with today's confirmation sheets.
8. **Reply,** fixed at the bottom: the quick-reply chips, shown only while
   the box is empty; the box (`reply, or /command…`); Send. A chip fills the
   box. A paused session shows "Resume the session to reply." instead, and
   a session that can't take a send for another reason shows neither, as
   today.

Room for sub-project 2: the CHANGES tile becomes the diff's link, an
"earlier turns" link goes above the last message, and the tool Claude is
running joins the header's second line.

## Settings and new session

- **Settings** gains a Quick replies section: a textarea with one reply per
  line, Save, and Reset to defaults (`replies: null`). It shows the stored
  list, or the defaults while unset. A refused list keeps the text in the
  box and shows the host's reason.
- **New session:** the workspace picker lists the shown workspaces under
  "Active", then the others under "Other workspaces" (two `optgroup`s; one
  plain list when every workspace is shown).

## The message tree

`GET /api/v1/sessions/{id}/message` answers
`{"text": "...", "blocks": [...], "truncated": false}`.

- `text` is the source the tree was built from: the session's last message
  as the web client's replica holds it. The app uses the tree only
  while `text` equals the `last_message` it holds, and fetches again when
  the stream changes the message. An unknown session is 404; a session with
  no last message gets `"text": ""` and `"blocks": []`.
- **Parsing:** goldmark with GitHub-flavoured markdown (tables,
  strikethrough, linkify); a line break inside a paragraph is kept as a
  `br`, as Claude's terminal shows it. Raw HTML, inline or as a block, becomes a `text`
  node of its source; an image becomes its alt text; a link keeps its
  `href` only when it is an absolute `http` or `https` URL, and is otherwise
  its text.
- **Nodes** are objects `{"t": type, …}` with their children in `c`:
  - blocks: `p`; `h` (`level`, 1–6); `ul`, `ol` (`start`); `li`; `quote`;
    `hr`; `code` (`lang`, `v`); `table` (`align`, one of `""`, `"left"`,
    `"center"`, `"right"` per column), `tr`, `th`, `td`;
  - inline: `text` (`v`); `code_inline` (`v`); `em`; `strong`; `del`; `br`;
    `a` (`href`).
- **Limits:** the first 64 KiB of the message, cut at a rune boundary; at
  most 5,000 nodes and 32 levels. Past any of them the rest is dropped and
  `truncated` is true.
- **Drawing** (`markdown.mjs`): elements only, built through Preact; the app
  never inserts an HTML string. An `a` gets `target="_blank"` and
  `rel="noopener noreferrer"`; a `code` block and a table scroll sideways in
  their own box; headings are bold in the title colour, `h` 1 and 2 at 15px
  and the rest at 13px.

## Quick replies

- `PUT /api/v1/quick-replies` takes `{"replies": [...]}`, or
  `{"replies": null}` for the defaults (a body without `replies` is the
  same). The host trims each entry, drops a
  duplicate after its first, and refuses (400 with its text) an entry that
  is empty, longer than 100 characters (runes), or holds a newline or another
  control character, and a list of more than 12. An empty list is allowed
  and shows no chips. Any other error maps through `modelStatus`, as
  `PUT /api/v1/device`'s do. 200 answers `{"quick_replies": [...]}`, the
  resolved list.
- The snapshot's `quick_replies` is the resolved list (the defaults while
  unset), and a change reaches every stream as a `quick_replies` event.
- A chip's text goes through the same send as typed text, so a reply that
  is an allowed slash command runs as one.

## Usage

- The snapshot's `usage` is `[{"account", "plan", "five_hour", "seven_day",
  "at"}]`, in the registry's order, the default account (`default`)
  first, as the TUI lists them. A window is
  `{"pct", "resets_at"}`, or `null` when the server gave none; `resets_at`
  and `at` are RFC 3339 times, and `resets_at` is `""` when the server gave
  no reset (the app then shows no time). An account whose sample isn't
  `Available`, or that has no sample yet, is left out.
- `usage_stale_after_s` is `core.UsageStaleAfter` in seconds. The app dims a
  sample whose `at` is older, by the phone's clock, as `ago` already reads
  `since`; staleness grows while no event arrives, so it can't be a flag
  sent once.
- A change to the projected usage reaches every stream as a `usage` event.

## Workspaces shown

- The projection computes `shown` from the served workspaces, the open list
  and each workspace's sessions, again on every `WorkspacesChanged`,
  `ViewsChanged` and `ModelChanged`. The `workspaces` event goes out when a
  flag or the order changes (the stream already compares the list it last
  sent), for example when a closed workspace's last running session pauses.
- **Matching** is by name, as the TUI keys its tabs. The open list is
  resolved to registered workspaces, so a stale name is ignored; a renamed
  workspace keeps its place; a tab whose workspace failed to load is shown,
  empty. The global workspace (name `""`) is never a tab, so it is shown
  only while it has a session that isn't paused, or when the open list is
  empty. Workspace terminals are not sessions here and show nothing.
- **Only the fleet's lists and chips filter.** The snapshot keeps every
  workspace's sessions, so a push or a link opens any session by ID. A
  session that needs you isn't paused, so its workspace is always shown and
  the badge matches what the fleet lists.

## API and stream

| Change | Where |
|---|---|
| `GET /api/v1/sessions/{id}/message` | new route, behind `authed` |
| `PUT /api/v1/quick-replies` | new route, behind `authed` |
| `workspaces[].shown`, and the order | snapshot; `workspaces` event |
| `pr.url` on a session | snapshot; `sessions` event |
| `usage`, `usage_stale_after_s` | snapshot; `usage` event |
| `quick_replies` | snapshot; `quick_replies` event |

All additive; nothing renamed or retyped. `internal/web/README.md`'s API
table, stream section and snapshot fields list them.

## Failure modes

- **A markdown fetch that fails, or a stale snapshot shown offline:** the
  plain text stays, and the app fetches again when the message changes or
  the screen reopens.
- **A usage probe that fails:** the last sample stays and dims with age.
  With no account besides default there are no probes and no usage section.
- **A `gh` that returns no URL, or one that isn't `https`:** no `pr.url`, and
  the PR tile isn't a link.
- **A downgrade to a build without quick replies:** it drops `quick_replies`
  on its next `web.json` write, and after the next upgrade the phones show
  the defaults.
- **A `web.json` that failed to load:** the web is off, as today, and
  `SetQuickReplies` refuses like every other write.

## Testing

- `internal/web`, over the production path (a booted model, the real
  `rpc.Server`, a client over `net.Pipe`, `httptest`):
  - the tree: each node type; raw HTML, inline and as a block, and images
    as text; `javascript:`, `mailto:` and relative links losing `href`; each
    limit setting `truncated`; the cut at a rune boundary;
  - `shown`, with both sides of each guard: an open tab with only paused
    sessions (shown); a closed workspace with a running session (shown),
    with only paused ones (hidden), with none (hidden); the global
    workspace; an empty open list (all shown); the order; a rename; a stale
    name;
  - the usage projection: the order, an account not `Available` left out, a
    missing window as `null`, `usage_stale_after_s` equal to
    `core.UsageStaleAfter`;
  - `prOf`: `url` kept for `https`, dropped otherwise;
  - the routes: the message (404, the empty answer, `text`); the quick
    replies (each refusal a 400 with the host's text, `readJSON`'s
    refusals, 200 with the resolved list, the event on every stream);
  - the snapshot's new fields, and the `usage`, `quick_replies` and
    `workspaces` events sent only on a change.
- `core`: `SetQuickReplies`' checks; a `web.json` round trip with
  `quick_replies`; a file without the field loading unset; the fixture of
  `TestModelView_CloneKeepsEveryField` setting `QuickReplies`.
- `session/github`: the PR query asks for `url`, parsing fills `PR.URL`, and
  `StateFor` copies it.
- The app: `node --check` on every module, and
  `TestStatic_IndexHasNoInlineScriptOrStyle` and
  `TestStatic_ModulesSetNoStyleAttribute` still passing.
- On the iPhone, through the `loom-dev` skill (never `./loom` or
  `loom serve stop` in a loom pane): the steps of `docs/claude/web-layer.md`,
  plus the tiles and their scroll, usage dimming once stale, the chips
  following the TUI's tabs (open and close one), a message with a table and
  a code block, a quick-reply edit on one phone showing on another, and the
  PR tile opening GitHub.

## Work packages

- **A. Daemon side:** `github.PR.URL` and its query; `quick_replies` in
  `web.json`, `WebView.QuickReplies`, `DefaultQuickReplies` and
  `WebHost.SetQuickReplies`; `core.UsageStaleAfter`, which
  `ui.UsageStaleAfter` then names.
- **B. Web layer:** the projection (`shown`, the order, usage, the quick
  replies), `pr.url`, the tree, both routes, the snapshot's fields and the
  events, with their tests.
- **C. The app:** the skin (`app.css`, `index.html`, the manifest), the
  fleet, the session screen, `markdown.mjs`, Settings, the new-session
  picker.
- **D. Docs:** `internal/web/README.md` (the API table, the stream, the
  snapshot); `core`'s and `internal/web`'s `CLAUDE.md` only for a new rule,
  after checking the budgets (`go run ./tools/claudemd -v`); `USAGE.md`'s
  web section where it describes the screens.

A, then B, then C; D closes.

## Alternatives considered

- **Other skins:** a native iOS look (grouped lists, a tab bar) fits the
  least above the fold and needs a light and a dark palette; a dashboard
  alone reads at arm's length but is a second look beside the TUI; a look of
  loom's own (a woven strip of the fleet) is new identity work; the TUI
  alone is denser, but no figure in it reads at a glance, which the tiles
  add.
- **Following the phone's light or dark mode:** two palettes to keep
  readable and in step.
- **Embedding JetBrains Mono:** about 200 KB in the binary plus its licence,
  for a font close to SF Mono.
- **Workspaces:** the open tabs only (a prompt in a closed workspace would
  push but not list), or the tabs plus sessions that need you (a session
  working there would hide).
- **Markdown:** goldmark rendering HTML that the app inserts as raw HTML
  (the first HTML string on the page, its safety resting on goldmark's
  options); a JS parser (logic in JS, which has no tests, and a second
  pinned library); the tree inside every `sessions` event (the last message
  is uncapped, and it would ride every change to a workspace's sessions).
- **Quick replies:** a fixed set, or the phone's recent replies (which
  would differ from phone to phone).

## Out of scope

- Sub-project 2 (the conversation, the diff, the current tool) and
  sub-project 3 (recover, resume with, a session from an issue, quiet at
  the desk, mute, the digest).
- Answering permission prompts in loom; a light theme; embedded fonts;
  syntax highlighting in code blocks; images in messages.
