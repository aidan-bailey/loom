# A quieter fleet screen: implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Drop the fleet's count tiles and usage boxes, show each Claude session's account share on its row, and move the reset times and the other window into the session screen's Account tile.

**Architecture:** Front-end only, in `internal/web/static`. `js/usage.mjs` gains the two pure helpers both screens use (`shownWindow`, `rowShare`); `js/fleet.mjs` loses `Tile`, `UsageBoxes`, `Window`, `Bar`, `scrollToGroup`; `js/session.mjs`'s Account tile reads `shownWindow`. Spec: `docs/superpowers/specs/2026-11-04-pwa-quiet-fleet-design.md`.

**Tech Stack:** Preact + htm modules with no build step; Go tests over the static files (`internal/web/static_test.go`); `tools/webshot` for renders.

**Testing:** the app has no JavaScript test harness by design (`internal/web/README.md`: "the logic stays in Go"), so each task is checked with `node --check`, the static tests and a webshot render, not a red-green unit test.

**Settled while planning:** the Account tile's legend becomes the account's name, and its sub lines drop it (`5h · 1h40m`, `7d 31% · Sat`): a session tile is about 95px inside, 14 characters of 11px mono, and `default 5h · 1h40m` would be cut.

---

### Task 1: the helpers, the fleet and its CSS

**Files:**
- Modify: `internal/web/static/js/usage.mjs`
- Modify: `internal/web/static/js/fleet.mjs`
- Modify: `internal/web/static/app.css` (the tile and usage rules near `.sc-tiles`, and the fleet rows block)

- [ ] **Step 1: add the helpers to `usage.mjs`**

Change the header comment's last line to `// The fleet's rows and the session screen's Account tile read them.`, and append:

```js
// HOT_PCT is the share at which a fleet row's figure turns amber.
export const HOT_PCT = 80;

// shownWindow is the window a session's share shows, with its label: the 5h
// one, or the 7d one when the account has no 5h; and the other window, if
// any. null for no entry or an entry with neither.
export function shownWindow(u) {
  if (!u) return null;
  if (u.five_hour) return { w: u.five_hour, label: "5h", other: u.seven_day || null, otherLabel: "7d" };
  if (u.seven_day) return { w: u.seven_day, label: "7d", other: null, otherLabel: "" };
  return null;
}

// rowShare is what a fleet row shows of its account's usage: {text, hot}, or
// null. Only a Claude session has one: another program records no account,
// and usageFor would read its "" as the default account's. hot is a share at
// HOT_PCT or more of a window that has not reset, from a sample that is not
// stale.
export function rowShare(snap, s, now) {
  if (!s.claude) return null;
  const u = usageFor(snap, s.account);
  const sw = shownWindow(u);
  if (!sw) return null;
  const stale = isStale(u.at, snap.usage_stale_after_s, now);
  const hot = !stale && !resetPassed(sw.w, now) && (Number(sw.w.pct) || 0) >= HOT_PCT;
  return { text: shareText(sw.w, now), hot };
}
```

- [ ] **Step 2: the fleet**

In `fleet.mjs`:

1. Replace the usage import with `import { rowShare } from "./usage.mjs";`.
2. `Row` takes a `share` prop and draws it before the age:

```js
function Row({ s, chip, fresh, share }) {
```
```js
        ${fresh ? html`<span class="sc-new">NEW</span>` : null}
        ${share ? html`<span class=${"sc-use" + (share.hot ? " sc-hot" : "")}>${share.text}</span>` : null}
        <span class="sc-age">${ago(s.since)}</span>
```

and its comment's first line becomes `// Row is one session: its glyph, title, its account's share and age; why it`, reflowing the rest.
3. Delete `scrollToGroup`, `Tile`, `Bar`, `Window` and `UsageBoxes` with their comments.
4. In `Fleet`: delete the `oldest`, `fresh` and `agents` constants; `rows` passes `share=${rowShare(snap, s, now)}`; delete the `<nav class="sc-tiles">…</nav>` and the `<${UsageBoxes} … />` lines; the group heading loses `id=${"g-" + g.key}`.
5. The tick's comment becomes `// Ages, the NEW marks and the shares' staleness move with the clock, not`.

- [ ] **Step 3: the CSS**

In `app.css`, delete `.sc-tile-n`, `.sc-tile-needs_you`, `.sc-tile-needs_you .sc-legend,` / `.sc-tile-needs_you .sc-tile-n`, `.sc-tile-your_turn .sc-tile-n`, the three `.sc-zero` rules, and the whole usage block from its comment through `.sc-bar-off`; keep `.sc-stale .sc-tile-fig { color: var(--dim); }` as a lone rule. Keep `.sc-tiles`, `.sc-tile .sc-legend`, `.sc-tile-fig`, `.sc-tile-sub` and the `.sc-glyph…` rules (the session screen and the rows use them). After `.sc-age` add:

```css
.sc-use { color: var(--dim); font-size: 12px; white-space: nowrap; }
.sc-use.sc-hot { color: var(--attention); }
```

- [ ] **Step 4: check**

Run: `node --check internal/web/static/js/usage.mjs && node --check internal/web/static/js/fleet.mjs && grep -rn "sc-zero\|sc-usage\|sc-win\|sc-bar\|sc-tile-n\b\|UsageBoxes\|scrollToGroup" internal/web/static`
Expected: no output (no syntax error, no leftover reference).

Run: `CGO_ENABLED=0 go test ./internal/web/...`
Expected: `ok`.

- [ ] **Step 5: commit**

```bash
git commit -m "feat(web): the fleet drops its tiles and usage boxes for a share per row" -- internal/web/static/js/usage.mjs internal/web/static/js/fleet.mjs internal/web/static/app.css
```

### Task 2: the session screen's Account tile

**Files:**
- Modify: `internal/web/static/js/session.mjs` (`Tiles`, and the `./usage.mjs` import)
- Modify: `internal/web/static/app.css` (beside `.sc-tile-sub`)

- [ ] **Step 1: the tile**

Import `{ usageFor, shareText, isStale, resetPassed, resetText, shownWindow }` from `./usage.mjs`, and replace the account block of `Tiles` (from `const u = usageFor(snap, s.account);` to its closing `}`) with:

```js
  const u = usageFor(snap, s.account);
  const sw = shownWindow(u);
  if (sw) {
    const old = isStale(u.at, snap.usage_stale_after_s, now);
    const reset = (w) => (resetPassed(w, now) ? "" : resetText(w.resets_at, now));
    const first = [sw.label, reset(sw.w)].filter(Boolean).join(" · ");
    const second = sw.other ? [sw.otherLabel + " " + shareText(sw.other, now), reset(sw.other)].filter(Boolean).join(" · ") : "";
    tiles.push(html`<div key="account" class=${"sc-box sc-tile" + (old ? " sc-stale" : "")}>
      <span class="sc-legend">${u.account}</span>
      <span class="sc-tile-fig">${shareText(sw.w, now)}</span>
      <span class="sc-tile-sub">${first}</span>
      ${second ? html`<span class="sc-tile-sub">${second}</span>` : null}
    </div>`);
  }
```

and in the `Tiles` comment, `its account's usage` becomes `its account's usage (the shown window's share and reset, then the other window's)`.

- [ ] **Step 2: the CSS**

After `.sc-tile-sub`, add `.sc-tile-sub + .sc-tile-sub { margin-top: 0; }`.

- [ ] **Step 3: check**

Run: `node --check internal/web/static/js/session.mjs && CGO_ENABLED=0 go test ./internal/web/...`
Expected: `ok`.

- [ ] **Step 4: commit**

```bash
git commit -m "feat(web): the Account tile shows both windows and their resets" -- internal/web/static/js/session.mjs internal/web/static/app.css
```

### Task 3: docs, render and gates

**Files:**
- Modify: `internal/web/README.md` (the skin paragraph)
- Modify: `USAGE.md` (the phone section's fleet bullet)
- Modify: `docs/superpowers/specs/2026-11-04-pwa-quiet-fleet-design.md` (decision 4: the legend is the account)

- [ ] **Step 1: README**

In the skin paragraph, `The fleet lists the \`shown\` workspaces under count tiles and the usage boxes (\`js/usage.mjs\` formats a window)` becomes `The fleet lists the \`shown\` workspaces, each Claude session's row showing its account's share beside its age, amber from 80% (\`rowShare\` in \`js/usage.mjs\`, which also formats a window); a session's Account tile shows that share, its reset, and the other window's share and reset`.

- [ ] **Step 2: USAGE.md**

The fleet bullet's opening, from `Tiles count the sessions` through `dimmed once the sample is old.`, becomes: `Each Claude session's row shows its account's plan usage beside its age (the 5-hour share, or the weekly one for an account without a 5-hour limit), amber from 80%; the counts are in the status row.` and `its account's usage` later in the bullet becomes `its account's usage (both windows, and when each resets)`.

- [ ] **Step 3: spec**

Decision 4's sub sentence becomes: `Its legend is the account's name, and its sub is two lines: the shown window and its reset (\`5h · 1h40m\`), then the other window's share and reset when it has one (\`7d 31% · Sat\`).`

- [ ] **Step 4: render**

Run: `CGO_ENABLED=0 go run ./tools/webshot -out <scratchpad>/shots2`
Look at `fleet.png`, `session.png`, `waiting.png`. Expected: no tiles or usage boxes; a share left of the age on each Claude row whose account has usage; the Account tile with the account in its border and two sub lines, uncut.

- [ ] **Step 5: gates**

Run: `CGO_ENABLED=0 go test ./internal/web/... ./tools/... && go run ./tools/claudemd && gofmt -l $(git ls-files '*.go' | grep -v '^vendor/')`
Expected: `ok` for each package, the lint clean, no gofmt output.

- [ ] **Step 6: commit**

```bash
git commit -m "docs(web): the fleet's shares and the Account tile" -- internal/web/README.md USAGE.md docs/superpowers/specs/2026-11-04-pwa-quiet-fleet-design.md
```
