# Flow board preview (desktop)

On the desktop, a click on a flow board card opens a preview of that card's flow in a pane on the right of the board, as a GitHub Projects board previews an item; the phone keeps opening the flow full-page.

## Decisions

1. **The preview is the URL's.** The board's address takes an optional search, `#/flow-board?session=<id>&plan=<plan>`. `routeOf` reads it (only `session` and `plan`, and only when `session` is set; `#/flow-board` alone is today's route with an empty search) and `hrefFor("flowBoard", {}, {session, plan})` writes it. Back closes the preview, a reload keeps it, and a card opened in a new tab opens the board with it.
2. **A card stays a plain `<a>`.** On desktop (`desk2`, `desk3`) its href is the board with its own `session` and `plan`; on the phone it is the flow's own address, as today. The card whose `session` and `plan` the board's search names is marked (`aria-current="true"` and a ring). The board's Body reads the search only from its props, never from the route.
3. **The pane is desktop chrome, not a panel.** `desktop/FlowPreview.tsx` draws `<aside aria-label="Flow preview">`, 480px wide and at most 45vw, beside the middle, and `DesktopShell` draws it while the route is the board and its search names a session. It holds, in order:
   - a header: the session's title (or "Session <id>" once it is gone), ⤢ (a link to the flow full-page, `hrefFor("flow", {id}, {plan})`) and ✕ (a link to `#/flow-board`), both plain links acting on nothing;
   - the session's `SubLine` (status and age, repo, branch) and links to its conversation (`sessionHref`) and to the flow;
   - a scroller of its own holding the flow panel through `PanelHost` (`panel="flow"`, props `{ sessionId, params: { id }, search: { plan } }`) under an `elementPane`, drawn only once the scroller has mounted, inside a `Boundary` keyed by the session and plan.
4. **No new read.** The pane is the flow panel's Body, so it reads the flow through `useFlow` as the flow tab does and draws its loading, error and gone states (`NoSession` for a session that is gone).
5. **Out of scope:** Esc to close (the app's one document key listener is the palette's), resizing the pane, and a preview on the phone.

## Tests

- `routeOf` and `hrefFor` round-trip the board's search; `#/flow-board` alone has an empty search.
- A card links to the board's preview on desktop and to the flow on the phone; the card the search names is marked, no other.
- The pane draws the flow Body with the card's props, ✕ and ⤢ link where decision 3 says, and the shell draws no pane without a `session` in the search.
- The pane gives its Body its own scroll pane and never reads the window's scroll.
