# PWA skin and glance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give loom's phone app loom's own skin (the TUI's afterglow look with dashboard tiles, dark only, SF Mono), and add the glances the daemon already knows: subscription usage, subagents by name, PR links, the last message as markdown, user-defined quick replies, and workspace chips that follow loom's tabs.

**Architecture:** Additive throughout, with no `Core` method and no wire change. The daemon side gains a PR URL from `gh`, a quick-reply list in `web.json` behind a new `WebHost.SetQuickReplies`, and an exported `core.UsageStaleAfter`. The web layer (`internal/web`) projects the open tabs, usage and quick replies from events it already receives (`ModelChanged`, `AccountsChanged`) into the snapshot and two new stream events, parses a last message into a safe tree with goldmark (`markdown.go`, served on demand), and stores quick replies through a new route. The app is restyled through `app.css` and redrawn screen by screen; it draws the message tree as elements and never inserts an HTML string.

**Tech Stack:** Go 1.25 (`go.mod`), goldmark v1.7.8 (already vendored), Preact + htm (pinned file), plain ES modules (`.mjs`) and CSS, testify.

**Spec:** `docs/superpowers/specs/2026-10-23-pwa-skin-glance-design.md`. **Mockups:** https://claude.ai/artifact/Dv1GwHHRw8cJefivtMGvyh (second row).

---

## Rules for every task

These come from the repo's `CLAUDE.md` files; they hold for every step.

- **Never run loom itself (`./loom`, `go run .`) or `loom serve stop` in a loom pane.** Try the app on the phone only through the `loom-dev` skill (`go run ./tools/loomdev`).
- **Keep scratch files out of the worktree**, and commit with explicit pathspecs: `git add -- <paths>` and `git commit -- <paths>`, never `-a`, `git add -A` or `.`.
- **Tests:** `CGO_ENABLED=0 go test ./...` (a plain `go test` fails where only clang is installed). Format with `gofmt -w` on the files you touched, never `gofmt -w .` (it rewrites `vendor/`).
- **The app:** modules are `.mjs`; no `style` attribute and no inline script anywhere (the CSP is `default-src 'self'`); the app's JSON only gains fields; never insert an HTML string into the page.
- **Never log a token, a cookie, a pairing code or a push subscription.**
- **Writing files:** the security hook blocks a Write whose text holds the word exec followed by an open paren, or React's raw-HTML prop name. Neither appears in this plan's code; keep it that way.
- **Commits** are conventional, scoped by package, and end with:

  ```
  Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_019icsHbrFq5LeZ4b4jFkSBe
  ```

## File map

| File | Change |
|---|---|
| `session/github/types.go`, `cli.go` | `PR.URL`, `State.PRURL`; the PR query asks for `url` |
| `session/github/cli_test.go`, `types_test.go` | the URL parsed, the flag pinned, `StateFor` copying it |
| `core/web.go` | `WebView.QuickReplies`, `MaxQuickReplies`, `ErrQuickReplies`, `DefaultQuickReplies`, `NormalizeQuickReplies` |
| `core/web_store.go` | `webState.quickReplies`, `web.json`'s `quick_replies` (load, check, save) |
| `core/web_host.go`, `core/loop_web.go` | `WebHost.SetQuickReplies` on the model and the loop |
| `core/state_views.go` | `ModelView.Clone` copies the quick replies |
| `core/usage.go`, `ui/account_usage.go` | `core.UsageStaleAfter`; `ui.UsageStaleAfter` names it |
| `core/quick_replies_test.go` (new), `core/state_views_test.go`, `core/web_host_test.go` | the daemon side's tests |
| `go.mod` | goldmark moves to the direct requirements |
| `internal/web/fleet.go` | `workspaceJSON.Shown`, `prJSON.URL`, usage and quick-reply JSON, the projection's `layout` |
| `internal/web/stream.go` | the hub reads the open list, usage and quick replies; snapshot fields; `usage` and `quick_replies` events |
| `internal/web/markdown.go` (new) | the message tree: `buildTree`, `mdNode`, `safeHref` |
| `internal/web/api.go` | `GET /api/v1/sessions/{id}/message`, `PUT /api/v1/quick-replies` |
| `internal/web/fleet_test.go`, `markdown_test.go` (new), `stream_test.go`, `api_test.go` | the web layer's tests |
| `internal/web/static/app.css` | rewritten: the skin |
| `internal/web/static/index.html` | one theme colour, `color-scheme` dark |
| `internal/web/static/js/skin.mjs`, `usage.mjs`, `markdown.mjs` (new) | glyphs and icons; usage helpers; the tree drawn as elements |
| `internal/web/static/js/fleet.mjs`, `session.mjs` | rewritten screens |
| `internal/web/static/js/settings.mjs`, `newsession.mjs`, `app.mjs`, `screens.mjs` | quick-reply editor; the picker's groups; new events and the `link` prop |
| `internal/web/static_test.go` | the stylesheet is dark only with every token |
| `internal/web/README.md`, `internal/web/CLAUDE.md`, `docs/claude/web-layer.md`, `USAGE.md` | docs |

Four tasks, one commit each: A (daemon side), B (web layer), C (the app), D (docs and the checklist). Each leaves the tree building and every test passing.

---

### Task A: Daemon side

**Files:**
- Modify: `session/github/types.go`, `session/github/cli.go`, `session/github/cli_test.go`, `session/github/types_test.go`
- Modify: `core/web.go`, `core/web_store.go`, `core/web_host.go`, `core/loop_web.go`, `core/state_views.go`, `core/usage.go`, `ui/account_usage.go`
- Create: `core/quick_replies_test.go`
- Modify: `core/state_views_test.go`, `core/web_host_test.go`

- [ ] **Step 1: Write the failing GitHub tests**

In `session/github/cli_test.go`, give each PR in `prListJSON` a URL:

```go
const prListJSON = `[
 {"number":45,"headRefName":"u/b","state":"OPEN","isDraft":false,"reviewDecision":"APPROVED","statusCheckRollup":[{"status":"COMPLETED","conclusion":"SUCCESS"}],"url":"https://github.com/o/r/pull/45"},
 {"number":40,"headRefName":"u/b","state":"CLOSED","isDraft":false,"reviewDecision":"","statusCheckRollup":[],"url":"https://github.com/o/r/pull/40"},
 {"number":46,"headRefName":"u/c","state":"OPEN","isDraft":true,"reviewDecision":"","statusCheckRollup":[{"status":"IN_PROGRESS"}],"url":"https://github.com/o/r/pull/46"}
]`
```

In `TestQuery_ParsesPRsAndIssues`, after `assert.Equal(t, ChecksPassing, pr.Checks)`, add:

```go
	assert.Equal(t, "https://github.com/o/r/pull/45", pr.URL, "the URL of the PR that won the branch")
```

In `TestQuery_PassesLoadBearingFlags`, replace the PR `--json` assertion with:

```go
	assert.Contains(t, ex.calls[0], "--json number,headRefName,state,isDraft,reviewDecision,statusCheckRollup,url")
```

In `session/github/types_test.go`, in `TestStateFor_JoinsPRByBranchAndIssueByNumber`, give the PR a URL and check that it is copied:

```go
		PRs:    map[string]PR{"u/b": {Number: 45, State: PROpen, Review: ReviewApproved, Checks: ChecksPassing, URL: "https://github.com/o/r/pull/45"}},
```

and after `assert.Equal(t, ChecksPassing, s.Checks)`:

```go
	assert.Equal(t, "https://github.com/o/r/pull/45", s.PRURL)
```

- [ ] **Step 2: Run them and see them fail**

Run: `CGO_ENABLED=0 go test ./session/github/`
Expected: FAIL to compile with `unknown field URL in struct literal` and `s.PRURL undefined`.

- [ ] **Step 3: Implement the PR URL**

In `session/github/types.go`, replace the `PR` type:

```go
// PR is one pull request keyed by its head branch in Snapshot.PRs. URL is
// its page on GitHub, as gh gives it.
type PR struct {
	Number int
	State  PRState
	Review Review
	Checks Checks
	URL    string
}
```

and the tail of `State` (from `HasPR` to the closing brace):

```go
	HasPR       bool
	PRNumber    int
	// PRState, Review, Checks and PRURL are meaningless unless HasPR: they
	// sit at their None zero values for a session with no PR, which is not
	// a state any real PR can be in. Always branch on HasPR first.
	PRState PRState
	Review  Review
	Checks  Checks
	PRURL   string
}
```

In `StateFor`, after `s.Checks = pr.Checks`, add `s.PRURL = pr.URL`.

In `session/github/cli.go`, add the field to `rawPR`:

```go
type rawPR struct {
	Number         int          `json:"number"`
	HeadRefName    string       `json:"headRefName"`
	State          string       `json:"state"`
	IsDraft        bool         `json:"isDraft"`
	ReviewDecision string       `json:"reviewDecision"`
	Rollup         []rollupItem `json:"statusCheckRollup"`
	URL            string       `json:"url"`
}
```

ask for it in `Query`:

```go
	out, err := gh(ctx, r, repoDir, "pr", "list", "--state", "all", "--limit", listLimit,
		"--json", "number,headRefName,state,isDraft,reviewDecision,statusCheckRollup,url")
```

and keep it when folding:

```go
		pr := PR{Number: p.Number, State: foldPRState(p.State, p.IsDraft), Review: foldReview(p.ReviewDecision), Checks: foldChecks(p.Rollup), URL: p.URL}
```

- [ ] **Step 4: Run them and see them pass**

Run: `CGO_ENABLED=0 go test ./session/github/`
Expected: `ok`.

- [ ] **Step 5: Write the failing quick-reply tests**

Create `core/quick_replies_test.go`:

```go
package core

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeQuickReplies(t *testing.T) {
	got, err := NormalizeQuickReplies([]string{"  yes ", "ship it", "yes", "/compact"})
	require.NoError(t, err)
	assert.Equal(t, []string{"yes", "ship it", "/compact"}, got, "trimmed, and a duplicate after its first dropped")

	got, err = NormalizeQuickReplies([]string{})
	require.NoError(t, err)
	assert.NotNil(t, got, "an empty list is a list")
	assert.Empty(t, got)

	twelve := make([]string, MaxQuickReplies)
	for i := range twelve {
		twelve[i] = strings.Repeat("a", i+1)
	}
	_, err = NormalizeQuickReplies(twelve)
	assert.NoError(t, err, "twelve is the most")
	_, err = NormalizeQuickReplies([]string{strings.Repeat("é", maxQuickReplyLen)})
	assert.NoError(t, err, "a hundred characters, however many bytes")

	for _, tc := range []struct {
		name    string
		replies []string
		why     string
	}{
		{"an empty reply", []string{"yes", "  "}, "reply 2 is empty"},
		{"a reply too long", []string{strings.Repeat("a", maxQuickReplyLen+1)}, "longer than 100 characters"},
		{"a newline", []string{"one\ntwo"}, "control character"},
		{"a tab", []string{"one\ttwo"}, "control character"},
		{"bytes that are not UTF-8", []string{"\xff"}, "not valid UTF-8"},
		{"thirteen replies", append(twelve, "thirteen"), "13 replies; at most 12"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NormalizeQuickReplies(tc.replies)
			require.ErrorIs(t, err, ErrQuickReplies)
			assert.Contains(t, err.Error(), tc.why)
		})
	}
}

func TestDefaultQuickReplies_IsAFreshListEachCall(t *testing.T) {
	a := DefaultQuickReplies()
	assert.Equal(t, []string{"yes", "continue", "/compact", "/clear"}, a)
	a[0] = "changed"
	assert.Equal(t, "yes", DefaultQuickReplies()[0], "a caller's edit reaches no one else")
}

// webFileQuickReplies is web.json's quick_replies, read back raw: nil when
// the file leaves them out.
func webFileQuickReplies(t *testing.T) []string {
	t.Helper()
	_, text := webHostFileOf(t)
	var f struct {
		QuickReplies []string `json:"quick_replies"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &f))
	return f.QuickReplies
}

func TestWebHost_SetQuickRepliesStoresTheListAndNilRestoresTheDefaults(t *testing.T) {
	m, _ := webHostModel(t)
	assert.Nil(t, m.Web().QuickReplies, "unset until the user sets some")

	stored, err := m.SetQuickReplies([]string{" ship it ", "yes", "yes"})
	require.NoError(t, err)
	assert.Equal(t, []string{"ship it", "yes"}, stored)
	assert.Equal(t, stored, m.Web().QuickReplies)
	assert.Equal(t, []string{"ship it", "yes"}, webFileQuickReplies(t), "kept in web.json")

	stored, err = m.SetQuickReplies([]string{})
	require.NoError(t, err)
	assert.NotNil(t, stored)
	assert.Empty(t, stored, "no chips is a choice")
	assert.Equal(t, []string{}, webFileQuickReplies(t), "kept as an empty list")

	stored, err = m.SetQuickReplies(nil)
	require.NoError(t, err)
	assert.Nil(t, stored)
	assert.Nil(t, m.Web().QuickReplies, "the defaults again")
	_, text := webHostFileOf(t)
	assert.NotContains(t, text, "quick_replies", "an unset list is left out of web.json")
}

func TestWebHost_SetQuickRepliesRefusesABadListAndChangesNothingWhenTheWriteFails(t *testing.T) {
	m, _ := webHostModel(t)
	_, err := m.SetQuickReplies([]string{"keep"})
	require.NoError(t, err)

	_, err = m.SetQuickReplies([]string{"two\nlines"})
	require.ErrorIs(t, err, ErrQuickReplies)
	assert.Equal(t, []string{"keep"}, m.Web().QuickReplies, "a refused list changes nothing")

	webHostBreakWrites(t)
	_, err = m.SetQuickReplies([]string{"lost"})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrQuickReplies, "a failed write is no refusal of the list")
	assert.Equal(t, []string{"keep"}, m.Web().QuickReplies, "the list is put back")
}

func TestWebHost_SetQuickRepliesIsRefusedWhileWebJSONIsLeftAlone(t *testing.T) {
	m := webModel(t, `{not json`)

	_, err := m.SetQuickReplies([]string{"yes"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "left alone")
	assert.NotErrorIs(t, err, ErrQuickReplies)
}

func TestInitWeb_LoadsTheQuickReplies(t *testing.T) {
	m := webModel(t, `{"version":1,"devices":[],"quick_replies":["yes","/compact"]}`)
	assert.Equal(t, []string{"yes", "/compact"}, m.Web().QuickReplies)

	m = webModel(t, `{"version":1,"devices":[],"quick_replies":[]}`)
	assert.NotNil(t, m.Web().QuickReplies, "an empty list stays a list")
	assert.Empty(t, m.Web().QuickReplies)

	m = webModel(t, `{"version":1,"devices":[]}`)
	assert.Nil(t, m.Web().QuickReplies, "a file without them leaves them unset")
}

func TestInitWeb_LeavesAFileWithQuickRepliesItWouldNotWriteAlone(t *testing.T) {
	for _, tc := range []struct{ name, replies, why string }{
		{"a reply with spaces around it", `[" yes"]`, "spaces around it"},
		{"a reply listed twice", `["yes","yes"]`, "listed twice"},
		{"an empty reply", `[""]`, "is empty"},
		{"too many", `["1","2","3","4","5","6","7","8","9","10","11","12","13"]`, "at most 12"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"version":1,"devices":[],"quick_replies":` + tc.replies + `}`

			m := webModel(t, body)

			assert.Contains(t, m.web.loadErr, tc.why)
			assert.Equal(t, body, string(webFileBytes(t, m)), "the file is as it was")
		})
	}
}

func TestSaveWeb_RoundTripsTheQuickReplies(t *testing.T) {
	for _, replies := range [][]string{{"yes", "ship it"}, {}} {
		m := webModel(t, webJSON)
		m.web.quickReplies = replies

		require.NoError(t, m.saveWeb())

		m2 := NewForTest(Options{})
		m2.initWeb()
		require.Empty(t, m2.web.loadErr)
		assert.Equal(t, replies, m2.Web().QuickReplies)
	}
}
```

In `core/state_views_test.go`, `TestModelView_CloneKeepsEveryField`: after `m.web.rev = 3`, add

```go
	m.web.quickReplies = []string{"yes", "/compact"}
```

after `require.EqualValues(t, 3, v.Web.Rev, "fixture: and the revision")`, add

```go
	require.Equal(t, []string{"yes", "/compact"}, v.Web.QuickReplies, "fixture: and the quick replies")
```

and at the end of the test:

```go
	c.Web.QuickReplies[0] = "changed"
	assert.Equal(t, "yes", v.Web.QuickReplies[0], "a deep copy of the quick replies too")
```

In `core/web_host_test.go`, `TestWebHostLoop_AChangeWakesTheClientAndReachesTheModelView`: after the `SetNotify` case (the one whose `want` checks `Notify.Waiting`), add a case:

```go
		{
			name: "SetQuickReplies",
			prep: func(t *testing.T, l *Loop, clk *webHostClock) func() {
				return func() {
					_, err := l.SetQuickReplies([]string{"ship it"})
					require.NoError(t, err)
				}
			},
			want: func(t *testing.T, v WebView, _ []Event) { assert.Equal(t, []string{"ship it"}, v.QuickReplies) },
		},
```

and in `TestWebHostLoop_AfterStopNoCallSucceeds`, before `_, err = l.VAPIDKey()`:

```go
	_, err = l.SetQuickReplies([]string{"yes"})
	assert.ErrorIs(t, err, ErrWebOff, "a stopped loop stores no list")
```

- [ ] **Step 6: Run them and see them fail**

Run: `CGO_ENABLED=0 go test ./core/ -run 'QuickReplies|CloneKeepsEveryField|WebHostLoop'`
Expected: FAIL to compile with `undefined: NormalizeQuickReplies` (and `MaxQuickReplies`, `ErrQuickReplies`, `DefaultQuickReplies`, `m.SetQuickReplies`, `m.web.quickReplies`, `v.Web.QuickReplies`).

- [ ] **Step 7: Implement the quick replies in core**

In `core/web.go`, add the field at the end of `WebView`:

```go
	Rev     uint64      // bumped by every ConfigureWeb; the layer rebinds when it changes
	// QuickReplies are the user's quick replies, nil while unset: the app
	// then offers DefaultQuickReplies. An empty list is a list.
	QuickReplies []string
}
```

After the `ErrNoDevice` error block, add:

```go
// The bounds on the quick replies a user sets (NormalizeQuickReplies).
const (
	MaxQuickReplies  = 12
	maxQuickReplyLen = 100 // characters
)

// ErrQuickReplies is what a refused quick-reply list wraps.
var ErrQuickReplies = errors.New("quick replies")

// DefaultQuickReplies are the quick replies the app offers while the user has
// set none: a fresh slice each call, so no caller's edit reaches another.
func DefaultQuickReplies() []string {
	return []string{"yes", "continue", "/compact", "/clear"}
}

// NormalizeQuickReplies checks a quick-reply list and returns it as the model
// keeps it: each reply trimmed, a duplicate after its first dropped. It
// refuses, wrapping ErrQuickReplies, a reply that is empty, not valid UTF-8,
// longer than maxQuickReplyLen characters or holding a control character (a
// newline among them), and a list of more than MaxQuickReplies. An empty list
// is a list: the app shows no chips.
func NormalizeQuickReplies(replies []string) ([]string, error) {
	out := make([]string, 0, len(replies))
	for i, r := range replies {
		r = strings.TrimSpace(r)
		switch {
		case r == "":
			return nil, fmt.Errorf("%w: reply %d is empty", ErrQuickReplies, i+1)
		case !utf8.ValidString(r):
			return nil, fmt.Errorf("%w: reply %d is not valid UTF-8", ErrQuickReplies, i+1)
		case utf8.RuneCountInString(r) > maxQuickReplyLen:
			return nil, fmt.Errorf("%w: reply %d is longer than %d characters", ErrQuickReplies, i+1, maxQuickReplyLen)
		case strings.IndexFunc(r, unicode.IsControl) >= 0:
			return nil, fmt.Errorf("%w: reply %d holds a newline or another control character", ErrQuickReplies, i+1)
		}
		if !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	if len(out) > MaxQuickReplies {
		return nil, fmt.Errorf("%w: %d replies; at most %d", ErrQuickReplies, len(out), MaxQuickReplies)
	}
	return out, nil
}
```

In `core/web_store.go`, add to `webState`, after `now`:

```go
	quickReplies   []string         // the user's quick replies; nil while unset (the app offers DefaultQuickReplies)
```

make `view` copy them (in the `WebView` literal):

```go
	v := WebView{Listen: w.listen, Origin: w.origin, Serving: w.serving, Addr: w.addr, Error: w.servingErr, Rev: w.rev, QuickReplies: slices.Clone(w.quickReplies)}
```

add the field to `webFile`:

```go
type webFile struct {
	Version int             `json:"version"`
	Listen  string          `json:"listen"`
	Origin  string          `json:"origin"`
	VAPID   *webFileVAPID   `json:"vapid,omitempty"`
	Devices []webFileDevice `json:"devices"`
	// QuickReplies is left out while unset; an empty list is kept as [].
	QuickReplies *[]string `json:"quick_replies,omitempty"`
}
```

check it in `adopt`, before the final `slices.SortFunc`:

```go
	if f.QuickReplies != nil {
		replies, err := NormalizeQuickReplies(*f.QuickReplies)
		if err != nil {
			return err
		}
		if !slices.Equal(replies, *f.QuickReplies) {
			return errors.New("quick_replies: a reply has spaces around it or is listed twice")
		}
		w.quickReplies = replies
	}
```

and write it in `saveWeb`, after the devices loop:

```go
	if w.quickReplies != nil {
		replies := slices.Clone(w.quickReplies)
		f.QuickReplies = &replies
	}
```

In `core/web_host.go`, add to the `WebHost` interface, after `SetPushSubscription`:

```go
	// SetQuickReplies stores the quick replies every paired phone offers, or
	// with nil goes back to DefaultQuickReplies, and returns the list as
	// stored, nil when unset. A list NormalizeQuickReplies refuses wraps
	// ErrQuickReplies.
	SetQuickReplies(replies []string) ([]string, error)
```

and the model's method, after `SetPushSubscription`:

```go
// SetQuickReplies stores the user's quick replies, or with nil goes back to
// the defaults (WebHost), and returns the list as stored, nil when unset. A
// list NormalizeQuickReplies refuses changes nothing; so does a write that
// fails. It works while the web is off, like RevokeDevice, and is refused
// while web.json is left alone. Loop goroutine only.
func (m *Model) SetQuickReplies(replies []string) ([]string, error) {
	w := &m.web
	if w.loadErr != "" {
		return nil, w.leftAlone()
	}
	var next []string
	if replies != nil {
		got, err := NormalizeQuickReplies(replies)
		if err != nil {
			return nil, err
		}
		next = got
	}
	prev := w.quickReplies
	w.quickReplies = next
	if err := m.saveWeb(); err != nil {
		w.quickReplies = prev
		return nil, fmt.Errorf("save quick replies: %w", err)
	}
	return slices.Clone(next), nil
}
```

In `core/loop_web.go`, after `SetPushSubscription`:

```go
// SetQuickReplies runs Model.SetQuickReplies on the loop. After Stop it
// answers ErrWebOff.
func (l *Loop) SetQuickReplies(replies []string) (stored []string, err error) {
	err = ErrWebOff
	l.webCall(func(m *Model) bool {
		stored, err = m.SetQuickReplies(replies)
		return err == nil
	})
	return stored, err
}
```

In `core/state_views.go`, `ModelView.Clone`, after `v.Web.Devices = slices.Clone(v.Web.Devices)`:

```go
	v.Web.QuickReplies = slices.Clone(v.Web.QuickReplies)
```

- [ ] **Step 8: Export the usage staleness bound**

In `core/usage.go`, replace the `usageInterval` comment and add the bound after it:

```go
// usageInterval is the usage probes' cadence. Plan usage moves slowly and
// the views dim a sample older than two intervals (UsageStaleAfter); the
// moments the user is choosing an account expedite a probe instead
// (RequestUsageProbe).
const usageInterval = 2 * time.Minute

// UsageStaleAfter is when a usage sample is old enough for a view to dim it
// and show its age: two probe intervals, so one late probe dims nothing. The
// TUI and the phone both read it.
const UsageStaleAfter = 2 * usageInterval
```

In `ui/account_usage.go`, add `"github.com/aidan-bailey/loom/core"` to the imports and replace the constant:

```go
// UsageStaleAfter is when a usage sample renders dimmed with its age: the
// model's bound (core.UsageStaleAfter), which the phone reads too.
const UsageStaleAfter = core.UsageStaleAfter
```

- [ ] **Step 9: Run the packages and see them pass**

Run: `gofmt -w core/web.go core/web_store.go core/web_host.go core/loop_web.go core/state_views.go core/usage.go core/quick_replies_test.go core/state_views_test.go core/web_host_test.go ui/account_usage.go session/github/*.go && CGO_ENABLED=0 go test ./session/github/ ./core/ ./ui/ ./internal/web/`
Expected: `ok` for each. (`internal/web`'s fakes embed `core.WebHost`, so the new method needs nothing there.)

- [ ] **Step 10: Commit**

```bash
git add -- session/github/types.go session/github/cli.go session/github/cli_test.go session/github/types_test.go core/web.go core/web_store.go core/web_host.go core/loop_web.go core/state_views.go core/usage.go core/quick_replies_test.go core/state_views_test.go core/web_host_test.go ui/account_usage.go
git commit -F - -- session/github/types.go session/github/cli.go session/github/cli_test.go session/github/types_test.go core/web.go core/web_store.go core/web_host.go core/loop_web.go core/state_views.go core/usage.go core/quick_replies_test.go core/state_views_test.go core/web_host_test.go ui/account_usage.go <<'EOF'
feat(github,core,ui): PR URLs, quick replies in web.json, the usage staleness bound

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_019icsHbrFq5LeZ4b4jFkSBe
EOF
```

### Task B: Web layer

**Files:**
- Modify: `go.mod`
- Modify: `internal/web/fleet.go`, `internal/web/stream.go`, `internal/web/api.go`
- Create: `internal/web/markdown.go`, `internal/web/markdown_test.go`
- Modify: `internal/web/fleet_test.go`, `internal/web/stream_test.go`, `internal/web/api_test.go`

- [ ] **Step 1: Write the failing projection tests**

In `internal/web/fleet_test.go`, add `"fmt"` and `"github.com/aidan-bailey/loom/account"` to the imports. Change `TestWorkspaceOf`'s first assertion to:

```go
	assert.JSONEq(t, `{"id": 3, "name": "loom", "label": "loom", "shown": false}`, string(b), "shown is the projection's to set (layout)")
```

In `TestSnapshotJSON_HasTheContractsKeys`, give the literal `Usage: []usageJSON{}` and `QuickReplies: []string{}`, and expect the new keys:

```go
	assert.ElementsMatch(t, []string{"build", "host", "workspaces", "sessions", "device", "badge", "notices", "usage", "usage_stale_after_s", "quick_replies"}, got)
```

Add these tests at the end of the file:

```go
// shownOf is the projection's workspaces as "label:shown", in its order.
func shownOf(p *projection) []string {
	var out []string
	for _, w := range p.workspaces {
		out = append(out, fmt.Sprintf("%s:%v", w.Label, w.Shown))
	}
	return out
}

// projectionOf is a projection serving ws in that order, with the views each
// workspace holds and the open list open.
func projectionOf(t *testing.T, ws []core.WorkspaceView, views map[core.WorkspaceID][]core.InstanceView, open ...string) *projection {
	t.Helper()
	p := newProjection()
	p.setWorkspaces(ws)
	for id, list := range views {
		require.True(t, p.setViews(id, list))
	}
	p.setOpen(open)
	return p
}

func TestProjection_ShowsTheOpenTabsAndTheWorkspacesWithALiveSession(t *testing.T) {
	ws := []core.WorkspaceView{
		{ID: 1, Label: "global"},
		{ID: 2, Name: "loom", Label: "loom"},
		{ID: 3, Name: "flow", Label: "flow"},
		{ID: 4, Name: "system", Label: "system"},
		{ID: 5, Name: "idle", Label: "idle"},
	}
	views := map[core.WorkspaceID][]core.InstanceView{
		2: {fleetView(20, "paused", session.Paused)},
		3: {fleetView(30, "works", session.Running), fleetView(31, "paused", session.Paused)},
		5: {fleetView(50, "paused", session.Paused), fleetView(51, "recoverable", session.Recoverable)},
	}

	p := projectionOf(t, ws, views, "system", "loom")

	assert.Equal(t, []string{"system:true", "loom:true", "global:false", "flow:true", "idle:false"}, shownOf(p),
		"the tabs first in tab order, shown with no session or only paused ones; then the rest in serve order, shown while a session isn't paused")
}

func TestProjection_WithNoTabOpenEveryWorkspaceIsShown(t *testing.T) {
	ws := []core.WorkspaceView{{ID: 1, Label: "global"}, {ID: 2, Name: "loom", Label: "loom"}}

	p := projectionOf(t, ws, nil)
	assert.Equal(t, []string{"global:true", "loom:true"}, shownOf(p), "global mode, or a list never saved")

	assert.True(t, p.setOpen([]string{"gone"}))
	assert.Equal(t, []string{"global:true", "loom:true"}, shownOf(p), "a list that names no served workspace is no list")
}

func TestProjection_TheGlobalWorkspaceIsShownOnlyForALiveSession(t *testing.T) {
	ws := []core.WorkspaceView{{ID: 1, Label: "global"}, {ID: 2, Name: "loom", Label: "loom"}}
	p := projectionOf(t, ws, map[core.WorkspaceID][]core.InstanceView{1: {fleetView(10, "asks", session.Prompting)}}, "", "loom")
	assert.Equal(t, []string{"loom:true", "global:true"}, shownOf(p), "shown for its waiting session; a tab named \"\" is not it")

	require.True(t, p.setViews(1, []core.InstanceView{fleetView(10, "asks", session.Paused)}))
	assert.Equal(t, []string{"loom:true", "global:false"}, shownOf(p))
}

func TestProjection_AClosedWorkspaceHidesWhenItsLastLiveSessionPauses(t *testing.T) {
	ws := []core.WorkspaceView{{ID: 2, Name: "loom", Label: "loom"}, {ID: 3, Name: "flow", Label: "flow"}}
	p := projectionOf(t, ws, map[core.WorkspaceID][]core.InstanceView{3: {fleetView(30, "works", session.Running)}}, "loom")
	require.Equal(t, []string{"loom:true", "flow:true"}, shownOf(p))

	require.True(t, p.setViews(3, []core.InstanceView{fleetView(30, "works", session.Paused)}))

	assert.Equal(t, []string{"loom:true", "flow:false"}, shownOf(p))
	require.Len(t, p.sessions[3], 1, "its sessions stay in the snapshot: a link still opens them")
}

func TestProjection_ARenamedTabKeepsItsPlace(t *testing.T) {
	p := projectionOf(t, []core.WorkspaceView{{ID: 2, Name: "loom", Label: "loom"}, {ID: 3, Name: "flow", Label: "flow"}}, nil, "flow", "loom")
	require.Equal(t, []string{"flow:true", "loom:true"}, shownOf(p))

	// RenameWorkspace renames the served workspace and the open list together.
	p.setWorkspaces([]core.WorkspaceView{{ID: 2, Name: "loom", Label: "loom"}, {ID: 3, Name: "river", Label: "river"}})
	require.True(t, p.setOpen([]string{"river", "loom"}))

	assert.Equal(t, []string{"river:true", "loom:true"}, shownOf(p))
	assert.False(t, p.setOpen([]string{"river", "loom"}), "the same list again changes nothing")
}

func TestUsageOf_FollowsTheRegistryAndLeavesOutWhatHasNoLimits(t *testing.T) {
	at := time.Date(2026, 10, 23, 9, 0, 0, 0, time.FixedZone("CEST", 2*60*60))
	resets := time.Date(2026, 10, 23, 12, 40, 0, 0, time.UTC)
	v := core.AccountsView{
		Names: core.AccountNames{Present: true, Default: "default", Names: []string{"default", "work", "api", "new"}},
		Usage: map[string]account.Usage{
			"default": {Available: true, Plan: "max", FiveHour: &account.Window{Pct: 62, ResetsAt: resets}, SevenDay: &account.Window{Pct: 38}, At: at},
			"work":    {Available: true, Plan: "team", SevenDay: &account.Window{Pct: 71, ResetsAt: resets}, At: at},
			"api":     {Available: false, At: at},
			"ghost":   {Available: true, Plan: "pro", At: at},
		},
	}

	b, err := json.Marshal(usageOf(v))

	require.NoError(t, err)
	assert.JSONEq(t, `[
		{"account": "default", "plan": "max", "five_hour": {"pct": 62, "resets_at": "2026-10-23T12:40:00Z"}, "seven_day": {"pct": 38, "resets_at": ""}, "at": "2026-10-23T07:00:00Z"},
		{"account": "work", "plan": "team", "five_hour": null, "seven_day": {"pct": 71, "resets_at": "2026-10-23T12:40:00Z"}, "at": "2026-10-23T07:00:00Z"}
	]`, string(b), "no plan limits (api), no sample yet (new) and no registered name (ghost) are left out")
	assert.NotNil(t, usageOf(core.AccountsView{}), "an empty list, never null")
}

func TestPrOf_KeepsTheURLOnlyWhenItIsHTTPS(t *testing.T) {
	base := github.State{Known: true, HasPR: true, PRNumber: 318, PRState: github.PROpen, Checks: github.ChecksPassing}
	for _, tc := range []struct{ url, want string }{
		{"https://github.com/o/r/pull/318", "https://github.com/o/r/pull/318"},
		{"http://github.com/o/r/pull/318", ""},
		{"javascript:void(0)", ""},
		{"/o/r/pull/318", ""},
		{"", ""},
	} {
		s := base
		s.PRURL = tc.url
		pr := prOf(s)
		require.NotNil(t, pr, tc.url)
		assert.Equal(t, tc.want, pr.URL, tc.url)
	}

	b, err := json.Marshal(prOf(base))
	require.NoError(t, err)
	assert.JSONEq(t, `{"number": 318, "state": "open", "checks": "passing"}`, string(b), "no URL, no key")
}

func TestResolvedQuickReplies(t *testing.T) {
	assert.Equal(t, core.DefaultQuickReplies(), resolvedQuickReplies(nil), "unset: the defaults")
	none := resolvedQuickReplies([]string{})
	assert.NotNil(t, none, "none: an empty list, never null")
	assert.Empty(t, none)
	assert.Equal(t, []string{"ship it"}, resolvedQuickReplies([]string{"ship it"}))
}
```

In `internal/web/stream_test.go`, `TestStream_SendsTheWorkspacesWhenTheyChange`, the workspace now carries its flag:

```go
	assert.Equal(t, workspaceJSON{ID: e.ws, Name: "renamed", Label: "renamed", Shown: true}, toy, "the ID stays, and with no tab open it is shown")
```

- [ ] **Step 2: Run them and see them fail**

Run: `CGO_ENABLED=0 go test ./internal/web/ -run 'Projection|UsageOf|PrOf|ResolvedQuickReplies|WorkspaceOf|SnapshotJSON|SendsTheWorkspaces'`
Expected: FAIL to compile: `unknown field Shown`, `p.setOpen undefined`, `undefined: usageOf`, `pr.URL undefined`, `undefined: resolvedQuickReplies`.

- [ ] **Step 3: Implement the projection**

In `internal/web/fleet.go`, add `"reflect"`, `"slices"`, `"github.com/aidan-bailey/loom/account"` and `"github.com/aidan-bailey/loom/config"` to the imports.

Replace `workspaceJSON` and `prJSON`:

```go
// workspaceJSON is a served workspace. It carries no path. Shown is whether
// the fleet lists it (layout).
type workspaceJSON struct {
	ID    core.WorkspaceID `json:"id"`
	Name  string           `json:"name"`
	Label string           `json:"label"`
	Shown bool             `json:"shown"`
}
```

```go
// prJSON is a session's pull request. URL is its page on GitHub, left out
// unless gh gave an https one.
type prJSON struct {
	Number int    `json:"number"`
	State  string `json:"state"`
	Checks string `json:"checks"`
	URL    string `json:"url,omitempty"`
}
```

After `agentJSON`, add the usage types:

```go
// windowJSON is one plan window: the share used, 0 to 100, and when it resets
// as an RFC 3339 time, "" when the server gave none.
type windowJSON struct {
	Pct      float64 `json:"pct"`
	ResetsAt string  `json:"resets_at"`
}

// usageJSON is one account's plan usage as the app reads it. A window the
// server did not report is null; At is when the sample was taken, which the
// app compares with usage_stale_after_s.
type usageJSON struct {
	Account  string      `json:"account"`
	Plan     string      `json:"plan"`
	FiveHour *windowJSON `json:"five_hour"`
	SevenDay *windowJSON `json:"seven_day"`
	At       string      `json:"at"`
}
```

Replace `snapshotJSON` and add the two event bodies after `sessionsJSON`:

```go
// snapshotJSON is the first event of every stream: everything the app shows,
// so nothing is replayed. Sessions is keyed by workspace ID, and Badge is the
// number of sessions in needs_you across the workspaces. Usage is each
// account's plan usage, UsageStaleAfterS the age in seconds past which the
// app dims a sample (core.UsageStaleAfter), and QuickReplies the list the app
// offers above its reply box.
type snapshotJSON struct {
	Build            string                             `json:"build"`
	Host             string                             `json:"host"`
	Workspaces       []workspaceJSON                    `json:"workspaces"`
	Sessions         map[core.WorkspaceID][]sessionJSON `json:"sessions"`
	Device           deviceJSON                         `json:"device"`
	Badge            int                                `json:"badge"`
	Notices          []noticeJSON                       `json:"notices"`
	Usage            []usageJSON                        `json:"usage"`
	UsageStaleAfterS int                                `json:"usage_stale_after_s"`
	QuickReplies     []string                           `json:"quick_replies"`
}
```

```go
// usageEventJSON is the `usage` event: every account's usage, as the snapshot
// has it.
type usageEventJSON struct {
	Usage []usageJSON `json:"usage"`
}

// quickRepliesJSON is the `quick_replies` event, and the answer of
// PUT /api/v1/quick-replies: the list the app offers.
type quickRepliesJSON struct {
	QuickReplies []string `json:"quick_replies"`
}
```

In `prOf`, keep an https URL:

```go
	return &prJSON{Number: s.PRNumber, State: state, Checks: checks, URL: httpsURL(s.PRURL)}
}

// httpsURL is u when it is an absolute https URL with a host, else "".
func httpsURL(u string) string {
	p, err := url.Parse(u)
	if err != nil || p.Scheme != "https" || p.Host == "" {
		return ""
	}
	return u
}
```

After `noticeOf`, add:

```go
// usageOf is every account's plan usage as the app reads it, in the
// registry's order (default first): an account whose sample has no plan
// limits, or that has no sample yet, is left out. It is never nil, so an
// empty list compares equal to the one the projection starts with.
func usageOf(v core.AccountsView) []usageJSON {
	out := []usageJSON{}
	for _, name := range v.Names.Names {
		u, ok := v.Usage[name]
		if !ok || !u.Available || u.At.IsZero() {
			continue
		}
		out = append(out, usageJSON{
			Account:  name,
			Plan:     u.Plan,
			FiveHour: windowOf(u.FiveHour),
			SevenDay: windowOf(u.SevenDay),
			At:       u.At.UTC().Format(time.RFC3339),
		})
	}
	return out
}

// windowOf is one window as the app reads it, nil for none.
func windowOf(w *account.Window) *windowJSON {
	if w == nil {
		return nil
	}
	out := &windowJSON{Pct: w.Pct}
	if !w.ResetsAt.IsZero() {
		out.ResetsAt = w.ResetsAt.UTC().Format(time.RFC3339)
	}
	return out
}

// resolvedQuickReplies is the list the app offers: the user's, or the
// defaults while they have set none. It is never nil, so the app always reads
// a list, empty when the user wants no chips.
func resolvedQuickReplies(stored []string) []string {
	if stored == nil {
		return core.DefaultQuickReplies()
	}
	return append([]string{}, stored...)
}

// openNames is the open list's names, in tab order.
func openNames(open []config.Workspace) []string {
	names := make([]string, 0, len(open))
	for _, w := range open {
		names = append(names, w.Name)
	}
	return names
}
```

Replace the `projection` type, `newProjection`, `setWorkspaces` and `setViews` (through the end of `setViews`) with:

```go
// projection is the fleet as the app reads it: the served workspaces, the
// open tabs first in tab order and the rest in serve order, each flagged
// shown or not (layout); each workspace's sessions in the model's display
// order; the accounts' usage; and the quick replies. It takes no lock; the
// hub's mutex guards it. Its slices are replaced, never edited, so a copy of
// one stays valid.
type projection struct {
	workspaces   []workspaceJSON
	sessions     map[core.WorkspaceID][]sessionJSON
	usage        []usageJSON
	quickReplies []string
	// serveOrder is the workspaces as the model serves them, and open the
	// open list's names in tab order: layout builds workspaces from them.
	serveOrder []workspaceJSON
	open       []string
}

func newProjection() *projection {
	return &projection{
		workspaces:   []workspaceJSON{},
		sessions:     map[core.WorkspaceID][]sessionJSON{},
		usage:        []usageJSON{},
		quickReplies: core.DefaultQuickReplies(),
		serveOrder:   []workspaceJSON{},
	}
}

// setWorkspaces replaces the served set. A workspace that stays keeps its
// sessions, a new one starts with none until its views arrive, and one that
// went loses them.
func (p *projection) setWorkspaces(views []core.WorkspaceView) {
	order := make([]workspaceJSON, 0, len(views))
	sessions := make(map[core.WorkspaceID][]sessionJSON, len(views))
	for _, v := range views {
		order = append(order, workspaceOf(v))
		if kept, ok := p.sessions[v.ID]; ok {
			sessions[v.ID] = kept
		} else {
			sessions[v.ID] = []sessionJSON{}
		}
	}
	p.serveOrder, p.sessions = order, sessions
	p.layout()
}

// setViews replaces one workspace's sessions, leaving workspace terminals
// out. It reports false, and changes nothing, for a workspace that is not
// served: the replica drops such views too.
func (p *projection) setViews(ws core.WorkspaceID, views []core.InstanceView) bool {
	if _, ok := p.sessions[ws]; !ok {
		return false
	}
	out := make([]sessionJSON, 0, len(views))
	for _, v := range views {
		if v.IsWorkspaceTerminal {
			continue
		}
		out = append(out, sessionOf(ws, v))
	}
	p.sessions[ws] = out
	p.layout()
	return true
}

// setOpen takes the open list's names, in tab order, and reports whether
// they changed.
func (p *projection) setOpen(names []string) bool {
	if slices.Equal(names, p.open) {
		return false
	}
	p.open = slices.Clone(names)
	p.layout()
	return true
}

// setUsage takes the accounts' view and reports whether the usage the app
// reads changed.
func (p *projection) setUsage(v core.AccountsView) bool {
	u := usageOf(v)
	if reflect.DeepEqual(u, p.usage) {
		return false
	}
	p.usage = u
	return true
}

// setQuickReplies takes the stored quick replies (nil while unset) and
// reports whether the list the app offers changed.
func (p *projection) setQuickReplies(stored []string) bool {
	q := resolvedQuickReplies(stored)
	if slices.Equal(q, p.quickReplies) {
		return false
	}
	p.quickReplies = q
	return true
}

// layout puts the workspaces in the order the app lists them, the open tabs
// first in tab order and the rest in serve order, and flags each one the
// fleet shows: an open tab, or a workspace with a session that isn't paused.
// With no open tab the model serves (global mode, or a list never saved)
// every workspace is shown. Names match as the TUI keys its tabs; the global
// workspace, named "", is never a tab.
func (p *projection) layout() {
	at := make(map[string]int, len(p.serveOrder))
	for i, w := range p.serveOrder {
		if w.Name != "" {
			at[w.Name] = i
		}
	}
	placed := make([]bool, len(p.serveOrder))
	out := make([]workspaceJSON, 0, len(p.serveOrder))
	for _, name := range p.open {
		i, ok := at[name]
		if !ok || placed[i] {
			continue
		}
		placed[i] = true
		w := p.serveOrder[i]
		w.Shown = true
		out = append(out, w)
	}
	all := len(out) == 0
	for i, w := range p.serveOrder {
		if placed[i] {
			continue
		}
		w.Shown = all || p.live(w.ID)
		out = append(out, w)
	}
	p.workspaces = out
}

// live reports whether ws holds a session that isn't paused: one in Needs
// you, Your turn or Working.
func (p *projection) live(ws core.WorkspaceID) bool {
	for _, s := range p.sessions[ws] {
		if s.Group != groupPaused {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run them and see them pass**

Run: `CGO_ENABLED=0 go test ./internal/web/ -run 'Projection|UsageOf|PrOf|ResolvedQuickReplies|WorkspaceOf|SnapshotJSON|SendsTheWorkspaces'`
Expected: `ok`.

- [ ] **Step 5: Write the failing tree tests**

Create `internal/web/markdown_test.go` (this block is fenced with four backticks, since one of its cases holds three):

````go
package web

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// treeJSON is the tree of msg as the app reads it, and whether it was cut.
func treeJSON(t *testing.T, msg string) (string, bool) {
	t.Helper()
	blocks, truncated := buildTree(msg)
	b, err := json.Marshal(blocks)
	require.NoError(t, err)
	return string(b), truncated
}

func TestBuildTree_EachNodeType(t *testing.T) {
	for _, tc := range []struct{ name, md, want string }{
		{"a paragraph keeps its line break", "one\ntwo", `[{"t":"p","c":[{"t":"text","v":"one"},{"t":"br"},{"t":"text","v":"two"}]}]`},
		{"headings", "# One\n### Three", `[{"t":"h","level":1,"c":[{"t":"text","v":"One"}]},{"t":"h","level":3,"c":[{"t":"text","v":"Three"}]}]`},
		{"emphasis, strong, strikethrough and code", "*a* **b** ~~c~~ `d`", `[{"t":"p","c":[{"t":"em","c":[{"t":"text","v":"a"}]},{"t":"text","v":" "},{"t":"strong","c":[{"t":"text","v":"b"}]},{"t":"text","v":" "},{"t":"del","c":[{"t":"text","v":"c"}]},{"t":"text","v":" "},{"t":"code_inline","v":"d"}]}]`},
		{"a tight list", "- a\n- b", `[{"t":"ul","c":[{"t":"li","c":[{"t":"text","v":"a"}]},{"t":"li","c":[{"t":"text","v":"b"}]}]}]`},
		{"an ordered list keeps its start", "3. c\n4. d", `[{"t":"ol","start":3,"c":[{"t":"li","c":[{"t":"text","v":"c"}]},{"t":"li","c":[{"t":"text","v":"d"}]}]}]`},
		{"a quote", "> said", `[{"t":"quote","c":[{"t":"p","c":[{"t":"text","v":"said"}]}]}]`},
		{"a rule", "a\n\n---\n\nb", `[{"t":"p","c":[{"t":"text","v":"a"}]},{"t":"hr"},{"t":"p","c":[{"t":"text","v":"b"}]}]`},
		{"a fenced block", "```go\nx := 1\n```", `[{"t":"code","lang":"go","v":"x := 1"}]`},
		{"an indented block", "    x := 1", `[{"t":"code","v":"x := 1"}]`},
		{"a link", "[docs](https://x.example/a)", `[{"t":"p","c":[{"t":"a","href":"https://x.example/a","c":[{"t":"text","v":"docs"}]}]}]`},
		{"a bare link", "see https://x.example/a", `[{"t":"p","c":[{"t":"text","v":"see "},{"t":"a","href":"https://x.example/a","c":[{"t":"text","v":"https://x.example/a"}]}]}]`},
		{"a table", "| a | b |\n|:--|--:|\n| 1 | 2 |", `[{"t":"table","align":["left","right"],"c":[{"t":"tr","c":[{"t":"th","c":[{"t":"text","v":"a"}]},{"t":"th","c":[{"t":"text","v":"b"}]}]},{"t":"tr","c":[{"t":"td","c":[{"t":"text","v":"1"}]},{"t":"td","c":[{"t":"text","v":"2"}]}]}]}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, truncated := treeJSON(t, tc.md)
			assert.JSONEq(t, tc.want, got)
			assert.False(t, truncated)
		})
	}
}

func TestBuildTree_KeepsNoMarkupAndFollowsNoUnsafeLink(t *testing.T) {
	for _, tc := range []struct{ name, md, want string }{
		{"inline HTML is its source", "Run <b>this</b> now", `[{"t":"p","c":[{"t":"text","v":"Run "},{"t":"text","v":"<b>"},{"t":"text","v":"this"},{"t":"text","v":"</b>"},{"t":"text","v":" now"}]}]`},
		{"an HTML block is its source", "<div>\nhi\n</div>", `[{"t":"p","c":[{"t":"text","v":"<div>\nhi\n</div>"}]}]`},
		{"an image is its alt text", "![a cat](https://x.example/cat.png)", `[{"t":"p","c":[{"t":"text","v":"a cat"}]}]`},
		{"a javascript link is its text", "[go](javascript:void(0))", `[{"t":"p","c":[{"t":"text","v":"go"}]}]`},
		{"a mailto link is its text", "[mail](mailto:me@x.example)", `[{"t":"p","c":[{"t":"text","v":"mail"}]}]`},
		{"a relative link is its text", "[file](docs/a.md)", `[{"t":"p","c":[{"t":"text","v":"file"}]}]`},
		{"a bare email is its text", "me@x.example", `[{"t":"p","c":[{"t":"text","v":"me@x.example"}]}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := treeJSON(t, tc.md)
			assert.JSONEq(t, tc.want, got)
		})
	}
}

func TestBuildTree_Limits(t *testing.T) {
	blocks, truncated := buildTree("")
	assert.NotNil(t, blocks, "no message: an empty list, never null")
	assert.Empty(t, blocks)
	assert.False(t, truncated)

	_, truncated = buildTree(strings.Repeat("a", maxMessageBytes))
	assert.False(t, truncated, "64 KiB is parsed whole")

	_, truncated = buildTree(strings.Repeat("a", maxMessageBytes) + "tail")
	assert.True(t, truncated, "past 64 KiB the rest is dropped")

	// "é" is two bytes, so a cut at 64 KiB would split one.
	blocks, truncated = buildTree("a" + strings.Repeat("é", maxMessageBytes/2))
	require.True(t, truncated)
	require.NotEmpty(t, blocks)
	last := blocks[0].C[len(blocks[0].C)-1].V
	assert.True(t, utf8.ValidString(last), "the cut falls between runes")
	assert.True(t, strings.HasSuffix(last, "é"))

	_, truncated = buildTree(strings.Repeat("- a\n", maxTreeNodes))
	assert.True(t, truncated, "past 5,000 nodes the rest is dropped")

	_, truncated = buildTree(strings.Repeat(">", maxTreeDepth+2) + " deep")
	assert.True(t, truncated, "past 32 levels the rest is dropped")
}
````

- [ ] **Step 6: Run them and see them fail**

Run: `CGO_ENABLED=0 go test ./internal/web/ -run BuildTree`
Expected: FAIL to compile: `undefined: buildTree` (and `maxMessageBytes`, `maxTreeNodes`, `maxTreeDepth`).

- [ ] **Step 7: Implement the tree**

In `go.mod`, move `github.com/yuin/goldmark v1.7.8` out of the `// indirect` block into the first `require` block, without the comment, between `github.com/stretchr/testify v1.11.1` and `github.com/yuin/gopher-lua v1.1.2`. It is vendored already and `vendor/modules.txt` marks it explicit, so nothing else changes.

Create `internal/web/markdown.go`:

```go
package web

import (
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// The message tree: a session's last message parsed as markdown here, in Go,
// and sent as plain nodes the app draws as elements, so no HTML string ever
// reaches the page. Raw HTML stays text, an image is its alt text, and a link
// keeps its target only when it is an absolute http or https URL.

// The tree's bounds: the bytes of the message parsed, the nodes kept, and how
// deep they nest. Past any of them the rest is dropped and the tree says so.
const (
	maxMessageBytes = 64 << 10
	maxTreeNodes    = 5000
	maxTreeDepth    = 32
)

// mdNode is one node of the tree: T names its kind (p, h, ul, ol, li, quote,
// hr, code, table, tr, th, td; text, code_inline, em, strong, del, br, a), and
// the other fields are set only for the kinds that carry them. C holds the
// children.
type mdNode struct {
	T     string    `json:"t"`
	V     string    `json:"v,omitempty"`
	Level int       `json:"level,omitempty"`
	Start *int      `json:"start,omitempty"`
	Lang  string    `json:"lang,omitempty"`
	Href  string    `json:"href,omitempty"`
	Align []string  `json:"align,omitempty"`
	C     []*mdNode `json:"c,omitempty"`
}

// messageJSON is GET /api/v1/sessions/{id}/message: the tree of a session's
// last message, the text it was built from, and whether the tree is cut.
type messageJSON struct {
	Text      string    `json:"text"`
	Blocks    []*mdNode `json:"blocks"`
	Truncated bool      `json:"truncated"`
}

// buildTree parses msg's first maxMessageBytes, cut at a rune boundary, as
// GitHub-flavoured markdown (tables, strikethrough, bare links), and returns
// its blocks, never nil, and whether anything was dropped. A parser is made
// per call: goldmark does not promise that one is safe to share.
func buildTree(msg string) ([]*mdNode, bool) {
	src := []byte(msg)
	cut := false
	if len(src) > maxMessageBytes {
		n := maxMessageBytes
		for n > 0 && !utf8.RuneStart(src[n]) {
			n--
		}
		src, cut = src[:n], true
	}
	md := goldmark.New(goldmark.WithExtensions(extension.Table, extension.Strikethrough, extension.Linkify))
	doc := md.Parser().Parse(text.NewReader(src))
	b := &treeBuilder{src: src}
	blocks := b.children(doc, 1)
	if blocks == nil {
		blocks = []*mdNode{}
	}
	return blocks, cut || b.dropped
}

// treeBuilder turns goldmark's AST into the tree, counting what it keeps.
type treeBuilder struct {
	src     []byte
	nodes   int
	dropped bool
}

// room counts one node at depth, and reports false, marking the tree cut,
// once maxTreeNodes are kept or when the node would sit deeper than
// maxTreeDepth.
func (b *treeBuilder) room(depth int) bool {
	if b.nodes >= maxTreeNodes || depth > maxTreeDepth {
		b.dropped = true
		return false
	}
	b.nodes++
	return true
}

// leaf keeps nd at depth, or nothing past the bounds.
func (b *treeBuilder) leaf(depth int, nd *mdNode) []*mdNode {
	if !b.room(depth) {
		return nil
	}
	return []*mdNode{nd}
}

// parent keeps nd at depth with the children kids makes one level down, or
// nothing past the bounds.
func (b *treeBuilder) parent(depth int, nd *mdNode, kids func(depth int) []*mdNode) []*mdNode {
	if !b.room(depth) {
		return nil
	}
	nd.C = kids(depth + 1)
	return []*mdNode{nd}
}

// wrap keeps nd at depth with n's children under it.
func (b *treeBuilder) wrap(n gast.Node, depth int, nd *mdNode) []*mdNode {
	return b.parent(depth, nd, func(d int) []*mdNode { return b.children(n, d) })
}

// children converts n's children, each at depth.
func (b *treeBuilder) children(n gast.Node, depth int) []*mdNode {
	var out []*mdNode
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		out = append(out, b.node(c, depth)...)
	}
	return out
}

// node converts one goldmark node at depth: none past the bounds, several
// where goldmark nests what the tree keeps flat (a tight list's text block, a
// text followed by a line break), and for a node this build does not name,
// its content without its kind.
func (b *treeBuilder) node(n gast.Node, depth int) []*mdNode {
	switch n := n.(type) {
	case *gast.TextBlock:
		return b.children(n, depth)
	case *gast.Text:
		out := b.leaf(depth, &mdNode{T: "text", V: string(n.Value(b.src))})
		if n.SoftLineBreak() || n.HardLineBreak() {
			out = append(out, b.leaf(depth, &mdNode{T: "br"})...)
		}
		return out
	case *gast.String:
		return b.leaf(depth, &mdNode{T: "text", V: string(n.Value)})
	case *gast.RawHTML:
		return b.leaf(depth, &mdNode{T: "text", V: string(n.Segments.Value(b.src))})
	case *gast.CodeSpan:
		return b.leaf(depth, &mdNode{T: "code_inline", V: b.plain(n)})
	case *gast.Image:
		return b.leaf(depth, &mdNode{T: "text", V: b.plain(n)})
	case *gast.AutoLink:
		label := &mdNode{T: "text", V: string(n.Label(b.src))}
		href := safeHref(string(n.URL(b.src)))
		if href == "" {
			return b.leaf(depth, label)
		}
		return b.parent(depth, &mdNode{T: "a", Href: href}, func(d int) []*mdNode { return b.leaf(d, label) })
	case *gast.Link:
		if href := safeHref(string(n.Destination)); href != "" {
			return b.wrap(n, depth, &mdNode{T: "a", Href: href})
		}
		return b.children(n, depth)
	case *gast.Emphasis:
		if n.Level >= 2 {
			return b.wrap(n, depth, &mdNode{T: "strong"})
		}
		return b.wrap(n, depth, &mdNode{T: "em"})
	case *east.Strikethrough:
		return b.wrap(n, depth, &mdNode{T: "del"})
	case *gast.Paragraph:
		return b.wrap(n, depth, &mdNode{T: "p"})
	case *gast.Heading:
		return b.wrap(n, depth, &mdNode{T: "h", Level: n.Level})
	case *gast.List:
		if n.IsOrdered() {
			start := n.Start
			return b.wrap(n, depth, &mdNode{T: "ol", Start: &start})
		}
		return b.wrap(n, depth, &mdNode{T: "ul"})
	case *gast.ListItem:
		return b.wrap(n, depth, &mdNode{T: "li"})
	case *gast.Blockquote:
		return b.wrap(n, depth, &mdNode{T: "quote"})
	case *gast.ThematicBreak:
		return b.leaf(depth, &mdNode{T: "hr"})
	case *gast.FencedCodeBlock:
		return b.leaf(depth, &mdNode{T: "code", Lang: string(n.Language(b.src)), V: b.lines(n.Lines())})
	case *gast.CodeBlock:
		return b.leaf(depth, &mdNode{T: "code", V: b.lines(n.Lines())})
	case *gast.HTMLBlock:
		raw := string(n.Lines().Value(b.src))
		if n.HasClosure() {
			raw += string(n.ClosureLine.Value(b.src))
		}
		source := &mdNode{T: "text", V: strings.TrimSuffix(raw, "\n")}
		return b.parent(depth, &mdNode{T: "p"}, func(d int) []*mdNode { return b.leaf(d, source) })
	case *east.Table:
		align := make([]string, 0, len(n.Alignments))
		for _, a := range n.Alignments {
			align = append(align, alignOf(a))
		}
		return b.wrap(n, depth, &mdNode{T: "table", Align: align})
	case *east.TableHeader, *east.TableRow:
		return b.wrap(n, depth, &mdNode{T: "tr"})
	case *east.TableCell:
		if _, head := n.Parent().(*east.TableHeader); head {
			return b.wrap(n, depth, &mdNode{T: "th"})
		}
		return b.wrap(n, depth, &mdNode{T: "td"})
	}
	return b.children(n, depth)
}

// plain is the text under n without its markup: a code span's code, an
// image's alt text.
func (b *treeBuilder) plain(n gast.Node) string {
	var sb strings.Builder
	_ = gast.Walk(n, func(c gast.Node, entering bool) (gast.WalkStatus, error) {
		if !entering {
			return gast.WalkContinue, nil
		}
		switch c := c.(type) {
		case *gast.Text:
			sb.Write(c.Value(b.src))
			if c.SoftLineBreak() || c.HardLineBreak() {
				sb.WriteByte(' ')
			}
		case *gast.String:
			sb.Write(c.Value)
		}
		return gast.WalkContinue, nil
	})
	return sb.String()
}

// lines is a block's lines as one string, without its last newline.
func (b *treeBuilder) lines(segs *text.Segments) string {
	return strings.TrimSuffix(string(segs.Value(b.src)), "\n")
}

// alignOf is a column's alignment as the tree spells it: left, center, right,
// or "" for none.
func alignOf(a east.Alignment) string {
	switch a {
	case east.AlignLeft, east.AlignCenter, east.AlignRight:
		return a.String()
	}
	return ""
}

// safeHref is dest as the app may link to it: an absolute http or https URL
// with a host, in its parsed form; "" otherwise, so a javascript:, mailto: or
// relative link shows as its text.
func safeHref(dest string) string {
	u, err := url.Parse(dest)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	return u.String()
}
```

- [ ] **Step 8: Run them and see them pass**

Run: `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./internal/web/ -run BuildTree`
Expected: the build succeeds (the vendor tree is consistent with `go.mod`) and the tests pass. If goldmark's AST shapes a case differently from the expected JSON, print the tree (`t.Log(got)`), and fix the conversion in `markdown.go` when the spec's rule is broken (raw HTML not text, an unsafe `href` kept, a line break lost); only a harmless difference of shape (an extra empty `text`) changes the test's expectation instead.

- [ ] **Step 9: Write the failing stream and API tests**

In `internal/web/stream_test.go`, add `"github.com/aidan-bailey/loom/account"` to the imports and these tests:

```go
func TestStream_TheSnapshotCarriesTheUsageAndTheQuickReplies(t *testing.T) {
	e := newStreamEnv(t, streamSession{title: "one", status: session.Running})
	s := e.open(t, e.cookie)

	snap := unframe[snapshotJSON](t, s.expect("snapshot"))

	assert.Equal(t, core.DefaultQuickReplies(), snap.QuickReplies, "unset: the defaults")
	assert.NotNil(t, snap.Usage)
	assert.Empty(t, snap.Usage, "no account besides default: no probe, no usage")
	assert.Equal(t, int(core.UsageStaleAfter/time.Second), snap.UsageStaleAfterS)
}

func TestStream_SendsTheQuickRepliesWhenTheyChange(t *testing.T) {
	e := newStreamEnv(t, streamSession{title: "one", status: session.Running})
	s := e.open(t, e.cookie)
	s.expect("snapshot")

	_, err := e.loop.SetQuickReplies([]string{"ship it", "/compact"})
	require.NoError(t, err)
	got := unframe[quickRepliesJSON](t, s.expect("quick_replies"))
	assert.Equal(t, []string{"ship it", "/compact"}, got.QuickReplies)

	_, err = e.loop.SetQuickReplies([]string{})
	require.NoError(t, err)
	f := s.expect("quick_replies")
	assert.JSONEq(t, `{"quick_replies": []}`, string(f.Data), "no chips is an empty list, never null")
	s.none(100 * time.Millisecond)
}

func TestStream_SendsTheUsageWhenItChanges(t *testing.T) {
	e := newStreamEnv(t, streamSession{title: "one", status: session.Running})
	s := e.open(t, e.cookie)
	s.expect("snapshot")
	view := core.AccountsView{
		Names: core.AccountNames{Present: true, Default: "default", Names: []string{"default"}},
		Usage: map[string]account.Usage{"default": {Available: true, Plan: "max", FiveHour: &account.Window{Pct: 62}, At: time.Now()}},
	}

	e.layer.hub.apply([]core.Event{core.AccountsChanged{View: view}})

	got := unframe[usageEventJSON](t, s.expect("usage"))
	require.Len(t, got.Usage, 1)
	assert.Equal(t, "default", got.Usage[0].Account)
	require.NotNil(t, got.Usage[0].FiveHour)
	assert.Equal(t, 62.0, got.Usage[0].FiveHour.Pct)

	e.layer.hub.apply([]core.Event{core.AccountsChanged{View: view}})
	s.none(100 * time.Millisecond) // the same usage again sends nothing
}

func TestStream_TheWorkspacesFollowTheOpenTabs(t *testing.T) {
	e := newStreamEnv(t, streamSession{title: "one", status: session.Paused})
	s := e.open(t, e.cookie)
	snap := unframe[snapshotJSON](t, s.expect("snapshot"))
	for _, w := range snap.Workspaces {
		assert.True(t, w.Shown, "no tab open: %q is shown", w.Label)
	}

	e.tui.PersistOpenList([]string{"toy"})

	ws := unframe[workspacesJSON](t, s.expect("workspaces"))
	require.NotEmpty(t, ws.Workspaces)
	assert.Equal(t, workspaceJSON{ID: e.ws, Name: "toy", Label: "toy", Shown: true}, ws.Workspaces[0], "the tab first, shown with only a paused session")
	for _, w := range ws.Workspaces[1:] {
		assert.False(t, w.Shown, "%q is no tab and has no live session", w.Label)
	}
}
```

In `internal/web/api_test.go`, add `"github.com/aidan-bailey/loom/session/hooks"` (and `"strings"` if it is not imported yet) to the imports, and add:

```go
// apiWithMessage is a fixture session whose Claude ended its turn saying msg,
// as a Stop hook reports it.
func apiWithMessage(t *testing.T, title, msg string) *session.Instance {
	t.Helper()
	inst := apiInstance(t, title, session.Ready)
	require.True(t, inst.ApplyHookScan(session.HookScanResult{LaunchID: "0123456789abcdef", Replayed: true, Events: []hooks.Event{
		{Name: hooks.EventStop, LastAssistantMessage: msg, At: time.Now()},
	}}))
	return inst
}

func TestAPI_TheMessageIsTheTreeOfTheLastMessage(t *testing.T) {
	msg := "# Done\n\nSee [the PR](https://github.com/o/r/pull/318).\n\nRun <script>x</script> now"
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{apiWithMessage(t, "one", msg), apiInstance(t, "quiet", session.Running)}})

	status, out := env.do(http.MethodGet, fmt.Sprintf("/api/v1/sessions/%d/message", env.ids["one"]), nil)

	require.Equal(t, http.StatusOK, status, "body: %s", out)
	var got messageJSON
	require.NoError(t, json.Unmarshal(out, &got))
	assert.Equal(t, msg, got.Text, "the text the tree was built from")
	assert.False(t, got.Truncated)
	blocks, err := json.Marshal(got.Blocks)
	require.NoError(t, err)
	assert.JSONEq(t, `[
		{"t": "h", "level": 1, "c": [{"t": "text", "v": "Done"}]},
		{"t": "p", "c": [{"t": "text", "v": "See "}, {"t": "a", "href": "https://github.com/o/r/pull/318", "c": [{"t": "text", "v": "the PR"}]}, {"t": "text", "v": "."}]},
		{"t": "p", "c": [{"t": "text", "v": "Run "}, {"t": "text", "v": "<script>"}, {"t": "text", "v": "x"}, {"t": "text", "v": "</script>"}, {"t": "text", "v": " now"}]}
	]`, string(blocks), "the script is text")

	status, out = env.do(http.MethodGet, fmt.Sprintf("/api/v1/sessions/%d/message", env.ids["quiet"]), nil)
	require.Equal(t, http.StatusOK, status)
	assert.JSONEq(t, `{"text": "", "blocks": [], "truncated": false}`, string(out), "no last message: an empty tree")

	status, out = env.do(http.MethodGet, "/api/v1/sessions/999999/message", nil)
	assert.Equal(t, http.StatusNotFound, status)
	assert.Equal(t, "no such session", apiErrorOf(t, out))
}

func TestAPI_QuickRepliesStoreAListAndNullRestoresTheDefaults(t *testing.T) {
	env := newAPIEnv(t, apiFixture{})

	status, out := env.do(http.MethodPut, "/api/v1/quick-replies", map[string]any{"replies": []string{" ship it ", "yes", "yes"}})
	require.Equal(t, http.StatusOK, status, "body: %s", out)
	assert.JSONEq(t, `{"quick_replies": ["ship it", "yes"]}`, string(out))
	require.Eventually(t, func() bool {
		q := env.layer.client.Web().QuickReplies
		return len(q) == 2 && q[0] == "ship it" && q[1] == "yes"
	}, 5*time.Second, 10*time.Millisecond, "the web client's replica follows the model")

	status, out = env.do(http.MethodPut, "/api/v1/quick-replies", map[string]any{"replies": []string{}})
	require.Equal(t, http.StatusOK, status)
	assert.JSONEq(t, `{"quick_replies": []}`, string(out), "no chips is an empty list")

	status, out = env.do(http.MethodPut, "/api/v1/quick-replies", map[string]any{"replies": nil})
	require.Equal(t, http.StatusOK, status)
	assert.JSONEq(t, `{"quick_replies": ["yes", "continue", "/compact", "/clear"]}`, string(out), "null: the defaults")
}

func TestAPI_QuickRepliesRefuseABadList(t *testing.T) {
	env := newAPIEnv(t, apiFixture{})
	thirteen := make([]string, 13)
	for i := range thirteen {
		thirteen[i] = fmt.Sprintf("reply %d", i)
	}

	for _, tc := range []struct {
		name string
		body any
		want string
	}{
		{"an empty reply", map[string]any{"replies": []string{"yes", ""}}, "quick replies: reply 2 is empty"},
		{"a newline", map[string]any{"replies": []string{"one\ntwo"}}, "control character"},
		{"a reply too long", map[string]any{"replies": []string{strings.Repeat("a", 101)}}, "longer than 100 characters"},
		{"thirteen replies", map[string]any{"replies": thirteen}, "13 replies; at most 12"},
		{"a field the route does not take", map[string]any{"replys": []string{"yes"}}, "bad request"},
	} {
		status, out := env.do(http.MethodPut, "/api/v1/quick-replies", tc.body)
		assert.Equal(t, http.StatusBadRequest, status, tc.name)
		assert.Contains(t, apiErrorOf(t, out), tc.want, tc.name)
	}
	assert.Nil(t, env.layer.client.Web().QuickReplies, "nothing was stored")
}

func TestAPI_TheMessageAndTheQuickRepliesNeedADevice(t *testing.T) {
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{apiInstance(t, "one", session.Running)}})

	for _, c := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, fmt.Sprintf("/api/v1/sessions/%d/message", env.ids["one"]), nil},
		{http.MethodPut, "/api/v1/quick-replies", map[string]any{"replies": []string{"yes"}}},
	} {
		status, _, _ := env.send(c.method, c.path, c.body, false)
		assert.Equal(t, http.StatusUnauthorized, status, c.path)
	}
}
```

- [ ] **Step 10: Run them and see them fail**

Run: `CGO_ENABLED=0 go test ./internal/web/ -run 'TheSnapshotCarries|SendsTheQuickReplies|SendsTheUsage|FollowTheOpenTabs|TheMessage|QuickReplies'`
Expected: the stream tests FAIL (no `quick_replies`, `usage` or reordered `workspaces` event arrives; the snapshot lacks the fields' values), and the API tests FAIL with 404 or 405 (the routes don't exist yet).

- [ ] **Step 11: Wire the hub and the routes**

In `internal/web/stream.go`, add to the `hub` struct, after `pubSessions`:

```go
	// pubUsage and pubQuickReplies are the usage and the quick replies the
	// streams were last told, likewise.
	pubUsage        []usageJSON
	pubQuickReplies []string
```

initialise them in `newHub`, after `pubSessions`:

```go
		pubUsage:        []usageJSON{},
		pubQuickReplies: core.DefaultQuickReplies(),
```

replace `apply`'s doc comment and its `ModelChanged` case, and add an `AccountsChanged` case:

```go
// apply takes the events one Sync returned. A workspace, its sessions, the
// open tabs, the usage or the quick replies changing wakes the flusher, which
// publishes at most every publishEvery; a notice not tied to a request, and a
// device record changing, go out at once.
```

```go
		case core.ModelChanged:
			h.applyDevicesLocked(ev.View.Web.Devices)
			if h.fleet.setOpen(openNames(ev.View.Registry.Open)) {
				moved = true
			}
			if h.fleet.setQuickReplies(ev.View.Web.QuickReplies) {
				moved = true
			}
		case core.AccountsChanged:
			if h.fleet.setUsage(ev.View) {
				moved = true
			}
```

at the end of `flush` (after the loop that prunes `pubSessions`), publish the two lists and update its doc comment's first sentence to "flush publishes the workspace list, each workspace's session list, the usage and the quick replies that differ from what the streams were last told, the lists with the fleet's badge count as it stands now.":

```go
	if !reflect.DeepEqual(h.fleet.usage, h.pubUsage) {
		h.pubUsage = h.fleet.usage
		h.broadcastLocked("usage", usageEventJSON{Usage: h.fleet.usage})
	}
	if !slices.Equal(h.fleet.quickReplies, h.pubQuickReplies) {
		h.pubQuickReplies = h.fleet.quickReplies
		h.broadcastLocked("quick_replies", quickRepliesJSON{QuickReplies: h.fleet.quickReplies})
	}
```

and fill the new snapshot fields in `snapshotLocked`:

```go
		Notices:          h.notices,
		Usage:            h.fleet.usage,
		UsageStaleAfterS: int(core.UsageStaleAfter / time.Second),
		QuickReplies:     h.fleet.quickReplies,
```

In `internal/web/api.go`, register the routes in `routeAPI`: after the `push` route,

```go
	m.Handle("GET /api/v1/sessions/{id}/message", l.authed(l.message))
```

and after `PUT /api/v1/device`,

```go
	m.Handle("PUT /api/v1/quick-replies", l.authed(l.putQuickReplies))
```

and add the handlers after `putDevice`:

```go
// message answers GET /api/v1/sessions/{id}/message: the tree of the
// session's last message (buildTree), from the web client's replica, with the
// text it was built from, so the app can tell it is the message it shows. A
// workspace terminal is no session of the app's.
func (l *Layer) message(w http.ResponseWriter, r *http.Request, device string) {
	a := actionLog{device: device, name: "message"}
	id, ok := l.sessionFromPath(w, r, a)
	if !ok {
		return
	}
	a.sessionID = id
	v, ok := l.client.View(id)
	if !ok || v.IsWorkspaceTerminal {
		l.refuse(w, a, http.StatusNotFound, "no such session")
		return
	}
	out := messageJSON{Blocks: []*mdNode{}}
	if v.HasLastMessage {
		out.Text = v.LastMessage
		out.Blocks, out.Truncated = buildTree(v.LastMessage)
	}
	l.record(a, http.StatusOK, "ok")
	writeJSON(w, http.StatusOK, out)
}

// quickRepliesBody is PUT /api/v1/quick-replies: the replies to offer, or
// null (or no field) for the defaults.
type quickRepliesBody struct {
	Replies []string `json:"replies"`
}

// putQuickReplies stores the quick replies every paired phone offers
// (WebHost.SetQuickReplies) and answers the list as the app reads it. A list
// the model refuses is 400 with its words; any other error is the model's
// (modelStatus), as for the device's settings.
func (l *Layer) putQuickReplies(w http.ResponseWriter, r *http.Request, device string) {
	a := actionLog{device: device, name: "quick-replies"}
	var body quickRepliesBody
	if !l.decodeBody(w, r, a, &body) {
		return
	}
	stored, err := l.host.SetQuickReplies(body.Replies)
	if err != nil {
		status := modelStatus(err)
		if errors.Is(err, core.ErrQuickReplies) {
			status = http.StatusBadRequest
		}
		l.refuse(w, a, status, err.Error())
		return
	}
	l.record(a, http.StatusOK, "ok")
	writeJSON(w, http.StatusOK, quickRepliesJSON{QuickReplies: resolvedQuickReplies(stored)})
}
```

- [ ] **Step 12: Run everything and see it pass**

Run: `gofmt -w internal/web/fleet.go internal/web/stream.go internal/web/api.go internal/web/markdown.go internal/web/fleet_test.go internal/web/markdown_test.go internal/web/stream_test.go internal/web/api_test.go && CGO_ENABLED=0 go vet ./internal/web/ ./core/ ./session/github/ && CGO_ENABLED=0 go test ./...`
Expected: `ok` for every package. Then `CC=clang CGO_ENABLED=1 go test -race ./internal/web/` passes too: the projection runs under the hub's mutex.

- [ ] **Step 13: Commit**

```bash
git add -- go.mod internal/web/fleet.go internal/web/stream.go internal/web/api.go internal/web/markdown.go internal/web/fleet_test.go internal/web/markdown_test.go internal/web/stream_test.go internal/web/api_test.go
git commit -F - -- go.mod internal/web/fleet.go internal/web/stream.go internal/web/api.go internal/web/markdown.go internal/web/fleet_test.go internal/web/markdown_test.go internal/web/stream_test.go internal/web/api_test.go <<'EOF'
feat(web): the shown workspaces, usage, quick replies and the message tree

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_019icsHbrFq5LeZ4b4jFkSBe
EOF
```

### Task C: The app

The JavaScript has no test harness, so its logic stays thin and the Go side decides (the shown flags, the tree, the resolved replies); each module is checked with `node --check`, and the static tests keep the CSP rules. The screens are checked by eye on the phone in Task D.

**Files:**
- Modify: `internal/web/static_test.go`, `internal/web/static/index.html`
- Replace: `internal/web/static/app.css`, `internal/web/static/js/fleet.mjs`, `internal/web/static/js/session.mjs`
- Create: `internal/web/static/js/skin.mjs`, `internal/web/static/js/usage.mjs`, `internal/web/static/js/markdown.mjs`
- Modify: `internal/web/static/js/settings.mjs`, `internal/web/static/js/newsession.mjs`, `internal/web/static/js/app.mjs`, `internal/web/static/js/screens.mjs`

- [ ] **Step 1: Write the failing static test**

In `internal/web/static_test.go`, replace `TestStatic_StylesheetHasTheTokensInBothThemes` with:

```go
// The skin is dark only (spec 2026-10-23, "Decisions"): the stylesheet has no
// light block, tells the browser so, and defines every token on :root; the
// shell names one theme colour.
func TestStatic_TheSkinIsDarkWithEveryToken(t *testing.T) {
	assets := staticAssets(t)
	css := string(assets["/app.css"].body)
	require.NotContains(t, css, "prefers-color-scheme", "no light block")
	start := strings.Index(css, ":root {")
	require.GreaterOrEqual(t, start, 0, "app.css has no :root block")
	root := css[start:]
	root = root[:strings.Index(root, "}")]
	require.Contains(t, root, "color-scheme: dark;")
	for _, token := range []string{"bg", "panel", "fg", "title", "dim", "faint", "border", "rule", "accent", "attention", "ok", "error", "workspace", "selection", "mono"} {
		require.Contains(t, root, "--"+token+":", ":root lacks --"+token)
	}
	index := string(assets["/index.html"].body)
	require.Equal(t, 1, strings.Count(index, `name="theme-color"`), "one theme colour")
	require.Contains(t, index, `<meta name="theme-color" content="#2e2e2e">`)
	require.Contains(t, index, `<meta name="color-scheme" content="dark">`)
}
```

- [ ] **Step 2: Run it and see it fail**

Run: `CGO_ENABLED=0 go test ./internal/web/ -run TestStatic_TheSkinIsDarkWithEveryToken`
Expected: FAIL with `no light block` (today's `app.css` has the dark media block).

- [ ] **Step 3: Write the shell and the stylesheet**

In `internal/web/static/index.html`, replace the two `theme-color` lines with:

```html
<meta name="theme-color" content="#2e2e2e">
<meta name="color-scheme" content="dark">
```

Replace `internal/web/static/app.css` with:

```css
/* Loom's app in loom's own skin: the TUI's afterglow palette (ui/theme.go),
   monospace type, glyph rows, and boxes whose label sits in their top border
   (spec 2026-10-23). Dark only. Amber (--attention) marks only what needs
   you. Nothing takes a style attribute: the CSP is default-src 'self', so
   every look is a class here. */
:root {
  color-scheme: dark;
  --bg: #2e2e2e;
  --panel: #333435;
  --fg: #d6d6d6;
  --title: #ffffff;
  --dim: #a0a0a0;
  --faint: #5c5c5c;
  --border: #505050;
  --rule: #404040;
  --accent: #6c99bb;
  --attention: #e5b567;
  --attention-border: #9b7f50;
  --attention-rule: #5a4a2c;
  --ok: #b4c973;
  --error: #e8836b;
  --error-border: #815449;
  --workspace: #c98aa3;
  --selection: #5a647e;
  --code: #3a3a3a;
  --mono: ui-monospace, Menlo, monospace;
  --radius: 3px;
}

*,
*::before,
*::after {
  box-sizing: border-box;
}

html {
  -webkit-text-size-adjust: 100%;
}

body {
  margin: 0;
  background: var(--bg);
  color: var(--fg);
  font: 13px/1.45 var(--mono);
  -webkit-font-smoothing: antialiased;
  overscroll-behavior-y: none;
}

a {
  color: var(--accent);
}

h1,
h2,
h3,
h4,
p {
  margin: 0 0 0.75rem;
}

code {
  font-family: var(--mono);
  background: var(--code);
  color: #e6e6e6;
  border-radius: 2px;
  padding: 0 3px;
}

button {
  font: inherit;
  color: var(--fg);
  background: none;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  min-height: 44px;
  padding: 0 14px;
}

button:disabled {
  opacity: 0.45;
}

button.primary {
  background: var(--accent);
  border-color: var(--accent);
  color: var(--bg);
  font-weight: 700;
}

button.danger {
  color: var(--error);
  border-color: var(--error-border);
}

button.link {
  background: none;
  border: none;
  color: var(--dim);
  text-decoration: underline;
}

/* iOS zooms into a control whose text is under 16px. */
input,
textarea,
select {
  font-family: var(--mono);
  font-size: 16px;
}

input:focus,
textarea:focus,
select:focus {
  outline: 2px solid var(--accent);
  outline-offset: 1px;
}

input.text {
  width: 100%;
  min-height: 44px;
  padding: 0.6rem 0.8rem;
  color: var(--fg);
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: var(--radius);
}

.shell {
  display: flex;
  flex-direction: column;
  min-height: 100vh;
  min-height: 100dvh;
  padding: env(safe-area-inset-top) env(safe-area-inset-right) env(safe-area-inset-bottom) env(safe-area-inset-left);
}

.shell > main {
  flex: 1;
  width: 100%;
  max-width: 40rem;
  margin: 0 auto;
}

.splash {
  padding: 3rem 16px;
  text-align: center;
  color: var(--dim);
}

.stale-bar {
  position: sticky;
  top: env(safe-area-inset-top);
  z-index: 30;
  padding: 6px 16px;
  text-align: center;
  font-size: 12px;
  color: var(--dim);
  background: var(--panel);
  border-bottom: 1px solid var(--rule);
}

/* Toasts come down from the top: the fleet's status line and the session's
   reply box hold the bottom. */
.toasts {
  position: fixed;
  left: 0;
  right: 0;
  top: calc(env(safe-area-inset-top) + 8px);
  display: flex;
  flex-direction: column;
  gap: 6px;
  align-items: center;
  padding: 0 16px;
  pointer-events: none;
  z-index: 100;
}

.toast {
  max-width: 32rem;
  padding: 8px 12px;
  color: var(--fg);
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  box-shadow: 0 2px 12px rgba(0, 0, 0, 0.35);
  pointer-events: auto;
  cursor: pointer;
}

.toast.error {
  border-color: var(--error);
}

/* ---- pairing ---- */
.pair {
  padding: 2rem 16px;
}

.pair .wordmark {
  font-size: 2rem;
  font-weight: 700;
  color: var(--title);
}

.pair .lede,
.pair .hint {
  color: var(--dim);
}

.pair .steps {
  padding-left: 1.25rem;
  margin: 0 0 1.25rem;
}

.pair .steps li {
  margin-bottom: 0.5rem;
}

.pair form {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}

.pair input.code {
  font-size: 1.6rem;
  letter-spacing: 0.15em;
  text-align: center;
  text-transform: uppercase;
}

.pair .form-error {
  margin-top: 0.75rem;
  color: var(--error);
}

/* ---- screens: words and colours ---- */
.sc-screen { padding: 4px 16px 24px; }
.sc-dim { color: var(--dim); }
.sc-light { font-weight: 400; }
.sc-small { font-size: 12px; margin: 10px 0 0; }
.sc-err { color: var(--error); }
.sc-empty { text-align: center; margin-top: 48px; }
.sc-fill { flex: 1; }
.sc-attn { color: var(--attention); }
.sc-ws { color: var(--workspace); }
.sc-add { color: var(--ok); }
.sc-del { color: var(--error); }
.sc-mono { font-size: 12px; color: var(--dim); overflow-wrap: anywhere; }
.sc-wrap { white-space: normal; overflow-wrap: anywhere; }

/* ---- screens: buttons and sheets ---- */
.sc-btn {
  font: inherit;
  color: var(--fg);
  background: none;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 0 14px;
  min-height: 44px;
  text-decoration: none;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
}
.sc-btn:disabled { opacity: 0.4; }
.sc-primary { background: var(--accent); border-color: var(--accent); color: var(--bg); font-weight: 700; }
.sc-danger { background: var(--error); border-color: var(--error); color: var(--bg); font-weight: 700; }
.sc-danger-text { color: var(--error); border-color: var(--error-border); }
.sc-block { display: flex; width: 100%; margin-top: 12px; }
.sc-backdrop {
  position: fixed;
  inset: 0;
  z-index: 50;
  display: flex;
  align-items: flex-end;
  background: rgba(0, 0, 0, 0.55);
}
.sc-sheet {
  width: 100%;
  background: var(--bg);
  color: var(--fg);
  border-top: 1px solid var(--border);
  border-radius: 8px 8px 0 0;
  padding: 16px 16px calc(16px + env(safe-area-inset-bottom));
}
.sc-sheet:focus { outline: none; }
.sc-sheet-title { margin: 0 0 8px; font-size: 15px; color: var(--title); }
.sc-sheet-body { color: var(--dim); }
.sc-sheet-body code { overflow-wrap: anywhere; }
.sc-sheet-body select { width: 100%; margin-top: 8px; }
.sc-sheet-actions { display: flex; gap: 10px; margin-top: 16px; }
.sc-sheet-actions .sc-btn { flex: 1; }

/* ---- the skin's pieces ---- */
/* A box: a border whose label sits in its top edge. The label takes the
   ground as its background, so the box has no fill of its own. */
.sc-box {
  position: relative;
  display: block;
  width: 100%;
  min-height: 44px;
  margin: 0;
  padding: 13px 10px 7px;
  color: var(--fg);
  background: none;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  text-align: left;
  text-decoration: none;
}
.sc-legend {
  position: absolute;
  top: -9px;
  left: 6px;
  max-width: calc(100% - 12px);
  padding: 0 4px;
  background: var(--bg);
  font-size: 11px;
  line-height: 16px;
  font-weight: 700;
  color: var(--dim);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.sc-tiles { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 8px; margin-top: 14px; }
.sc-tile .sc-legend { letter-spacing: 0.06em; text-transform: uppercase; }
.sc-tile-n { display: block; font-size: 32px; font-weight: 700; line-height: 1; }
.sc-tile-fig { display: flex; align-items: center; gap: 4px; font-size: 17px; font-weight: 700; line-height: 1.2; color: var(--title); white-space: nowrap; }
.sc-tile-sub { display: block; margin-top: 4px; font-size: 11px; color: var(--dim); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.sc-tile-needs_you { border-color: var(--attention-border); }
.sc-tile-needs_you .sc-legend,
.sc-tile-needs_you .sc-tile-n { color: var(--attention); }
.sc-tile-your_turn .sc-tile-n { color: var(--accent); }
.sc-zero { border-color: var(--rule); }
.sc-zero .sc-legend,
.sc-zero .sc-glyph,
.sc-zero .sc-tile-n { color: var(--dim); }
.sc-glyph { color: var(--dim); }
.sc-glyph-needs_you { color: var(--attention); }
.sc-glyph-your_turn { color: var(--accent); }

/* Usage: a box per account, a row per window, an 8-cell bar. A stale sample
   is dimmed by colour, which keeps its text readable. */
.sc-usage { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 18px 8px; margin-top: 18px; }
.sc-usage-box { padding: 12px 10px 6px; font-size: 11px; line-height: 1.35; }
.sc-usage-box .sc-legend { color: var(--fg); }
.sc-win { display: flex; align-items: baseline; gap: 6px; }
.sc-win-pct { font-size: 16px; font-weight: 700; color: var(--title); }
.sc-win-reset { flex: 1; text-align: right; }
.sc-bar { font-size: 10px; }
.sc-bar-on { color: var(--accent); }
.sc-bar-off { color: var(--faint); }
.sc-stale .sc-win-pct,
.sc-stale .sc-tile-fig,
.sc-stale .sc-bar-on { color: var(--dim); }

/* Chips: a 44px button around a small bordered label. */
.sc-chips,
.sc-quick { display: flex; gap: 6px; overflow-x: auto; scrollbar-width: none; }
.sc-chips { margin-top: 6px; }
.sc-chip-btn { flex: none; min-height: 44px; padding: 0; border: 0; background: none; color: var(--fg); font-size: 12px; white-space: nowrap; }
.sc-chip-btn > span { display: inline-block; padding: 3px 10px; border: 1px solid var(--rule); border-radius: var(--radius); }
.sc-chip-btn.sc-on > span { color: var(--title); background: var(--selection); border-color: var(--selection); }

/* Section rules: the label, then a line to the edge. */
.sc-group { margin-top: 14px; }
.sc-section { margin-top: 16px; }
.sc-group-title,
.sc-rule {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 0;
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: var(--dim);
  scroll-margin-top: 48px;
}
.sc-group-title::after,
.sc-rule::after { content: ""; flex: 1; height: 1px; background: var(--rule); }
.sc-t-needs_you { color: var(--attention); }
.sc-t-needs_you::after { background: var(--attention-rule); }
details.sc-group > summary { cursor: pointer; list-style: none; min-height: 44px; }
details.sc-group > summary::-webkit-details-marker { display: none; }
details.sc-group > summary::before { content: "▸"; }
details.sc-group[open] > summary::before { content: "▾"; }

/* ---- the fleet ---- */
.sc-header { display: flex; align-items: center; gap: 4px; }
.sc-h1 { flex: 1; min-width: 0; margin: 0; font-size: 17px; font-weight: 700; color: var(--title); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.sc-h1.sc-wrap { margin: 4px 0 6px; white-space: normal; overflow: visible; }
.sc-icon { display: flex; align-items: center; justify-content: center; width: 44px; height: 44px; color: var(--dim); }
.sc-row-s { display: block; padding: 8px 0 10px; border-bottom: 1px solid var(--rule); color: var(--fg); text-decoration: none; }
.sc-row-top { display: flex; gap: 8px; align-items: baseline; }
.sc-row-title { flex: 1; min-width: 0; font-weight: 700; color: var(--title); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.sc-age { color: var(--dim); font-size: 12px; white-space: nowrap; }
.sc-new { color: var(--bg); background: var(--accent); font-size: 10px; font-weight: 700; padding: 0 5px; border-radius: 2px; }
.sc-row-line,
.sc-meta { display: flex; flex-wrap: wrap; gap: 0 12px; padding-left: 16px; font-size: 12px; }
.sc-meta { color: var(--dim); }
.sc-detail { margin: 4px 0 0 16px; padding: 3px 8px; background: var(--panel); border: 1px solid var(--rule); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.sc-last { padding-left: 16px; color: #c8c8c8; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
.sc-fleet { padding-bottom: calc(56px + env(safe-area-inset-bottom)); }
.sc-status {
  position: fixed;
  left: 0;
  right: 0;
  bottom: 0;
  z-index: 20;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 7px 16px calc(7px + env(safe-area-inset-bottom));
  font-size: 12px;
  background: var(--panel);
  border-top: 1px solid var(--rule);
}
.sc-link { padding: 0 6px; font-weight: 700; color: var(--bg); background: var(--dim); }
.sc-link-live { background: var(--ok); }
.sc-link-offline { background: var(--error); }
.sc-sep { color: var(--faint); }

/* ---- the session ---- */
.sc-topbar { display: flex; align-items: center; margin: 0 -12px; }
.sc-back,
.sc-claude { display: inline-flex; align-items: center; gap: 6px; min-height: 44px; padding: 0 12px; text-decoration: none; }
.sc-back { color: var(--dim); }
.sc-claude { color: var(--accent); }
.sc-subline { display: flex; flex-wrap: wrap; gap: 0 10px; font-size: 12px; color: var(--dim); }
.sc-attn-box { margin-top: 18px; padding: 14px 12px 12px; border-color: var(--attention-border); }
.sc-attn-box .sc-legend { color: var(--attention); letter-spacing: 0.06em; text-transform: uppercase; }
.sc-detail-box { margin: 8px 0 0; padding: 6px 8px; background: var(--panel); border: 1px solid var(--rule); font: 12px/1.4 var(--mono); white-space: pre-wrap; overflow-wrap: anywhere; }
.sc-session .sc-tiles { grid-template-columns: repeat(auto-fill, minmax(96px, 1fr)); margin-top: 18px; }
.sc-pre { margin: 6px 0 0; white-space: pre-wrap; overflow-wrap: anywhere; }
.sc-agent { padding-top: 6px; }
.sc-agent-desc { padding-left: 16px; font-size: 12px; color: var(--dim); }
.sc-actions { display: flex; gap: 8px; margin: 16px 0 20px; }
.sc-actions .sc-btn { flex: 1; padding: 0 6px; }
/* The session is a full-height column, so on a short page the reply box sits
   at the bottom (margin-top: auto) and not under the actions; it reaches into
   the shell's bottom inset, since it pads for the home indicator itself. */
.sc-session {
  display: flex;
  flex-direction: column;
  min-height: calc(100dvh - env(safe-area-inset-top) - env(safe-area-inset-bottom));
  padding-bottom: 0;
}
.sc-session > .sc-h1 { flex: none; }
.sc-reply {
  position: sticky;
  bottom: 0;
  z-index: 20;
  margin: auto -16px calc(-1 * env(safe-area-inset-bottom));
  padding: 0 12px calc(10px + env(safe-area-inset-bottom));
  background: var(--panel);
  border-top: 1px solid var(--rule);
}
.sc-reply-row { display: flex; gap: 8px; align-items: flex-end; padding-top: 8px; }
.sc-reply textarea {
  flex: 1;
  min-width: 0;
  min-height: 44px;
  max-height: 160px;
  padding: 10px;
  color: var(--fg);
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  resize: none;
}
.sc-reply textarea::placeholder { color: var(--dim); }

/* ---- the last message ---- */
.md { margin-top: 6px; overflow-wrap: anywhere; }
.md p,
.md ul,
.md ol,
.md blockquote,
.md-code,
.md-table { margin: 0 0 8px; }
.md h3,
.md h4 { margin: 10px 0 4px; font-size: 13px; color: var(--title); }
.md h3 { font-size: 15px; }
.md ul,
.md ol { padding-left: 20px; }
.md li { margin: 2px 0; }
.md li > p { margin: 0; }
.md blockquote { margin-left: 0; padding-left: 10px; border-left: 2px solid var(--border); color: var(--dim); }
.md hr { margin: 10px 0; border: 0; border-top: 1px solid var(--rule); }
.md del { color: var(--dim); }
.md-code { padding: 8px; background: var(--panel); border: 1px solid var(--rule); overflow-x: auto; white-space: pre; font: 12px/1.4 var(--mono); }
.md-code code { padding: 0; background: none; color: inherit; }
.md-table { overflow-x: auto; }
.md table { border-collapse: collapse; font-size: 12px; }
.md th,
.md td { padding: 3px 8px; border: 1px solid var(--rule); text-align: left; vertical-align: top; }
.md th { color: var(--title); }
.md .md-center { text-align: center; }
.md .md-right { text-align: right; }

/* ---- new session and settings ---- */
.sc-form { display: flex; flex-direction: column; gap: 14px; margin-top: 12px; }
.sc-field { display: flex; flex-direction: column; gap: 6px; }
.sc-field > span:first-child { color: var(--dim); font-size: 12px; }
.sc-form input[type="text"],
.sc-form textarea,
.sc-form select,
.sc-sheet-body select,
.sc-quick-edit {
  color: var(--fg);
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 10px;
}
.sc-launch { display: flex; flex-direction: column; gap: 12px; padding: 10px; border: 1px solid var(--border); border-radius: var(--radius); }
.sc-launch > summary { cursor: pointer; color: var(--dim); }
.sc-launch > .sc-field { margin-top: 12px; }
.sc-row { display: flex; align-items: center; justify-content: space-between; gap: 12px; min-height: 44px; border-bottom: 1px solid var(--rule); }
.sc-row input[type="checkbox"] { width: 22px; height: 22px; accent-color: var(--accent); }
.sc-notice { padding: 8px 0; border-bottom: 1px solid var(--rule); font-size: 12px; overflow-wrap: anywhere; }
.sc-section > .sc-block { margin-top: 14px; }
.sc-quick-edit { width: 100%; margin-top: 8px; resize: vertical; }
.sc-buttons { display: flex; gap: 8px; margin-top: 10px; }
.sc-buttons .sc-btn { flex: 1; }
```

- [ ] **Step 4: Run the static tests and see them pass**

Run: `CGO_ENABLED=0 go test ./internal/web/ -run TestStatic`
Expected: `ok` (the skin test, the braces check, the no-inline-markup and no-style checks).

- [ ] **Step 5: Write the shared modules**

Create `internal/web/static/js/skin.mjs`:

```js
// The skin's shared pieces: the glyph each group's rows carry, and the icons
// the screens draw. Inline SVG is markup, not script or style, so the CSP
// lets it through.
import { html } from "../lib/preact-htm.mjs";

export const GLYPHS = { needs_you: "●", your_turn: "◆", working: "◐", paused: "○" };

// glyphOf is a group's glyph; a group this build does not know gets Paused's.
export function glyphOf(group) {
  return GLYPHS[group] || GLYPHS.paused;
}

export function GearIcon() {
  return html`<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    <circle cx="12" cy="12" r="3"></circle>
    <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"></path>
  </svg>`;
}

// OutIcon marks a link that leaves the app: Claude, GitHub.
export function OutIcon() {
  return html`<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M7 17L17 7M8 7h9v9"></path></svg>`;
}
```

Create `internal/web/static/js/usage.mjs`:

```js
// Subscription usage as the daemon sends it (the snapshot's `usage` and the
// `usage` event): helpers that pick a session's entry and format a window.
// The fleet draws the boxes and the session screen its tile.

const HOUR = 3600000;

// usageFor is the usage entry of a session's account: "" is the default
// account, which the daemon names "default".
export function usageFor(snap, account) {
  const name = account || "default";
  return ((snap && snap.usage) || []).find((u) => u.account === name);
}

// cells is how many of the bar's 8 cells a share of 0 to 100 fills.
export function cells(pct) {
  return Math.min(8, Math.max(0, Math.round((Number(pct) || 0) / 12.5)));
}

// resetPassed: the window reset already, so its share describes a window
// that no longer exists.
export function resetPassed(w, now) {
  const t = Date.parse(w && w.resets_at);
  return !Number.isNaN(t) && t <= now;
}

// shareText is a window's share as the app shows it: "62%", or "reset" once
// the window has reset.
export function shareText(w, now) {
  return resetPassed(w, now) ? "reset" : Math.round(Number(w.pct) || 0) + "%";
}

// resetText is when a window resets: "1h40m" or "35m" within a day, else the
// weekday ("Thu"), and "" when the server gave no time.
export function resetText(iso, now) {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "";
  const left = Math.max(0, t - now);
  if (left < 24 * HOUR) {
    const mins = Math.ceil(left / 60000);
    const hours = Math.floor(mins / 60);
    return hours > 0 ? hours + "h" + String(mins % 60).padStart(2, "0") + "m" : mins + "m";
  }
  return new Date(t).toLocaleDateString([], { weekday: "short" });
}

// isStale: the sample is at least the daemon's bound old (staleAfterS
// seconds, core.UsageStaleAfter), by this phone's clock.
export function isStale(at, staleAfterS, now) {
  const t = Date.parse(at);
  return !Number.isNaN(t) && staleAfterS > 0 && now - t >= staleAfterS * 1000;
}

// planName is a plan with a capital first letter: "max" is "Max".
export function planName(plan) {
  return plan ? plan.charAt(0).toUpperCase() + plan.slice(1) : "";
}
```

Create `internal/web/static/js/markdown.mjs`:

```js
// The last message as the daemon parsed it (GET /api/v1/sessions/{id}/message):
// a tree of plain nodes, each drawn here as its element. The daemon kept no
// markup and no unsafe link, and this file never inserts an HTML string, so
// nothing a message says runs in or restyles the page.
import { html } from "../lib/preact-htm.mjs";

function kids(n) {
  return (n.c || []).map(node);
}

// cell is one table cell, aligned by its column.
function cell(c, k, align) {
  const cls = align[k] ? "md-" + align[k] : undefined;
  return c.t === "th"
    ? html`<th key=${k} class=${cls}>${kids(c)}</th>`
    : html`<td key=${k} class=${cls}>${kids(c)}</td>`;
}

function node(n, i) {
  switch (n.t) {
    case "text":
      return n.v || "";
    case "br":
      return html`<br key=${i} />`;
    case "p":
      return html`<p key=${i}>${kids(n)}</p>`;
    case "h": {
      const Tag = n.level <= 2 ? "h3" : "h4";
      return html`<${Tag} key=${i}>${kids(n)}</${Tag}>`;
    }
    case "ul":
      return html`<ul key=${i}>${kids(n)}</ul>`;
    case "ol":
      return html`<ol key=${i} start=${n.start === undefined ? 1 : n.start}>${kids(n)}</ol>`;
    case "li":
      return html`<li key=${i}>${kids(n)}</li>`;
    case "quote":
      return html`<blockquote key=${i}>${kids(n)}</blockquote>`;
    case "hr":
      return html`<hr key=${i} />`;
    case "code":
      return html`<pre key=${i} class="md-code"><code>${n.v || ""}</code></pre>`;
    case "code_inline":
      return html`<code key=${i}>${n.v || ""}</code>`;
    case "em":
      return html`<em key=${i}>${kids(n)}</em>`;
    case "strong":
      return html`<strong key=${i}>${kids(n)}</strong>`;
    case "del":
      return html`<del key=${i}>${kids(n)}</del>`;
    case "a":
      return n.href
        ? html`<a key=${i} href=${n.href} target="_blank" rel="noopener noreferrer">${kids(n)}</a>`
        : html`<span key=${i}>${kids(n)}</span>`;
    case "table":
      return html`<div key=${i} class="md-table"><table><tbody>
        ${(n.c || []).map((r, j) => html`<tr key=${j}>${(r.c || []).map((c, k) => cell(c, k, n.align || []))}</tr>`)}
      </tbody></table></div>`;
    default:
      // A node this build does not know keeps its content.
      return html`<span key=${i}>${kids(n)}</span>`;
  }
}

// Markdown draws a message's blocks.
export function Markdown({ blocks }) {
  return html`<div class="md">${(blocks || []).map(node)}</div>`;
}
```

- [ ] **Step 6: Rewrite the fleet screen**

Replace `internal/web/static/js/fleet.mjs` with:

```js
// The fleet screen, and the helpers the session screen shares with it.
import { html, useState, useEffect } from "../lib/preact-htm.mjs";
import { pref } from "./store.mjs";
import { glyphOf, GearIcon } from "./skin.mjs";
import { cells, resetPassed, resetText, isStale, planName, shareText } from "./usage.mjs";

export const GROUPS = [
  { key: "needs_you", title: "Needs you" },
  { key: "your_turn", title: "Your turn" },
  { key: "working", title: "Working" },
  { key: "paused", title: "Paused" },
];

// LINK is the status line's word for the stream's state (app.mjs's link).
const LINK = { live: "LIVE", restarting: "RESTARTING", offline: "OFFLINE" };

export function allSessions(snap) {
  return Object.values((snap && snap.sessions) || {}).flat();
}

// shownWorkspaces are the workspaces the fleet lists, in the daemon's order
// (the open tabs first). One without the flag, from an older daemon, is shown.
export function shownWorkspaces(snap) {
  return ((snap && snap.workspaces) || []).filter((w) => w.shown !== false);
}

// groupSessions returns {needs_you, your_turn, working, paused}, each an
// array: Needs you with the longest wait first, the others newest first.
// A filter of "" keeps every workspace.
export function groupSessions(sessions, filter) {
  const out = { needs_you: [], your_turn: [], working: [], paused: [] };
  for (const s of sessions) {
    if (filter !== "" && String(s.workspace) !== filter) continue;
    // A group this build does not know is listed under Paused, never dropped.
    (out[s.group] || out.paused).push(s);
  }
  const t = (s) => Date.parse(s.since) || 0;
  out.needs_you.sort((a, b) => t(a) - t(b));
  for (const k of ["your_turn", "working", "paused"]) out[k].sort((a, b) => t(b) - t(a));
  return out;
}

// readSeen is {"<session id>": ms}: when this phone last opened the session.
export function readSeen() {
  try {
    const v = JSON.parse(pref("seen") || "{}");
    return v && typeof v === "object" ? v : {};
  } catch (e) {
    return {};
  }
}

export function markSeen(id) {
  const seen = readSeen();
  seen[id] = Date.now();
  pref("seen", JSON.stringify(seen));
}

// pruneSeen drops the times of sessions the daemon no longer lists. An
// empty list prunes nothing: it is more likely not loaded than a fleet of none.
export function pruneSeen(ids) {
  if (ids.length === 0) return;
  const keep = new Set(ids.map(String));
  const seen = readSeen();
  let changed = false;
  for (const k of Object.keys(seen)) {
    if (!keep.has(k)) {
      delete seen[k];
      changed = true;
    }
  }
  if (changed) pref("seen", JSON.stringify(seen));
}

// hasNewTurn: a Your turn session whose turn ended after this phone last
// opened it, or that it never opened.
export function hasNewTurn(s, seen) {
  if (s.group !== "your_turn") return false;
  const opened = seen[s.id];
  return opened === undefined || (Date.parse(s.since) || 0) > opened;
}

export function ago(iso, now = Date.now()) {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "";
  const secs = Math.max(0, Math.round((now - t) / 1000));
  if (secs < 60) return "now";
  const mins = Math.floor(secs / 60);
  if (mins < 60) return mins + "m";
  const hours = Math.floor(mins / 60);
  if (hours < 48) return hours + "h";
  return Math.floor(hours / 24) + "d";
}

// runningAgents is how many of a session's subagents are running.
export function runningAgents(s) {
  return (s.agents || []).filter((a) => !a.idle).length;
}

function plural(n, word) {
  return n + " " + word + (n === 1 ? "" : "s");
}

// PrMark is a PR's state on a row: ✓, ✗ or … for an open PR's checks, else
// draft, merged or closed.
function PrMark({ pr }) {
  if (pr.state !== "open") return html`<span>${pr.state}</span>`;
  if (pr.checks === "passing") return html`<span class="sc-add">✓</span>`;
  if (pr.checks === "failing") return html`<span class="sc-del">✗</span>`;
  if (pr.checks === "pending") return html`<span>…</span>`;
  return null;
}

// Row is one session: its glyph, title and age; why it waits, or what it said
// last; and what it holds (its label where the group's title doesn't say it,
// running agents, changes, PR, workspace).
function Row({ s, chip, fresh }) {
  const waiting = s.group === "needs_you";
  const reason = waiting && s.wait ? s.wait.reason : "";
  const detail = waiting && s.wait ? s.wait.detail : "";
  const title = (GROUPS.find((g) => g.key === s.group) || {}).title;
  const agents = runningAgents(s);
  const d = s.diff || {};
  const meta = [];
  if (s.label && s.label !== title) meta.push(html`<span>${s.label}</span>`);
  if (agents > 0) meta.push(html`<span>${plural(agents, "agent")}</span>`);
  if (d.added || d.removed) meta.push(html`<span><span class="sc-add">+${d.added}</span> <span class="sc-del">−${d.removed}</span></span>`);
  if (s.pr) meta.push(html`<span>PR #${s.pr.number} <${PrMark} pr=${s.pr} /></span>`);
  if (chip && !waiting) meta.push(html`<span class="sc-ws">${chip}</span>`);
  return html`
    <a class="sc-row-s" href=${"#s/" + s.id}>
      <div class="sc-row-top">
        <span class=${"sc-glyph sc-glyph-" + s.group} aria-hidden="true">${glyphOf(s.group)}</span>
        <span class="sc-row-title">${s.title}</span>
        ${fresh ? html`<span class="sc-new">NEW</span>` : null}
        <span class="sc-age">${ago(s.since)}</span>
      </div>
      ${reason || (waiting && chip)
        ? html`<div class="sc-row-line">
            ${reason ? html`<span class="sc-attn">${reason}</span>` : null}
            ${waiting && chip ? html`<span class="sc-ws">${chip}</span>` : null}
          </div>`
        : null}
      ${detail ? html`<div class="sc-detail">${detail}</div>` : null}
      ${!waiting && s.last_message ? html`<div class="sc-last">${s.last_message}</div>` : null}
      ${meta.length > 0 ? html`<div class="sc-meta">${meta}</div>` : null}
    </a>
  `;
}

// scrollToGroup brings a group's heading into view; the route stays.
function scrollToGroup(key) {
  const el = document.getElementById("g-" + key);
  if (el) el.scrollIntoView({ behavior: "smooth", block: "start" });
}

// Tile is a count tile: a button that scrolls to its group, or a plain box at
// 0, which has nowhere to go.
function Tile({ group, title, count, sub }) {
  const cls = "sc-box sc-tile sc-tile-" + group + (count === 0 ? " sc-zero" : "");
  const inner = html`
    <span class="sc-legend"><span class=${"sc-glyph sc-glyph-" + group} aria-hidden="true">${glyphOf(group)}</span> ${title}</span>
    <span class="sc-tile-n">${count}</span>
    ${sub ? html`<span class="sc-tile-sub">${sub}</span>` : null}
  `;
  if (count === 0) return html`<div class=${cls}>${inner}</div>`;
  return html`<button class=${cls} type="button" onClick=${() => scrollToGroup(group)}>${inner}</button>`;
}

function Bar({ w, now }) {
  const on = resetPassed(w, now) ? 0 : cells(w.pct);
  return html`<span class="sc-bar" aria-hidden="true"><span class="sc-bar-on">${"█".repeat(on)}</span><span class="sc-bar-off">${"░".repeat(8 - on)}</span></span>`;
}

function Window({ label, w, now }) {
  if (!w) return null;
  return html`
    <div class="sc-win">
      <span class="sc-dim">${label}</span>
      <span class="sc-win-pct">${shareText(w, now)}</span>
      <${Bar} w=${w} now=${now} />
      <span class="sc-win-reset sc-dim">${resetPassed(w, now) ? "" : resetText(w.resets_at, now)}</span>
    </div>
  `;
}

// UsageBoxes is a box per account with plan limits, its name and plan in the
// border and a row per window; a sample past the daemon's bound is dimmed and
// shows its age.
function UsageBoxes({ usage, staleAfter, now }) {
  if (!usage || usage.length === 0) return null;
  return html`
    <section class="sc-usage" aria-label="Subscription usage">
      ${usage.map((u) => {
        const old = isStale(u.at, staleAfter, now);
        return html`<div key=${u.account} class=${"sc-box sc-usage-box" + (old ? " sc-stale" : "")}>
          <span class="sc-legend">${u.account}${u.plan ? html`<span class="sc-dim"> · ${planName(u.plan)}</span>` : null}${old ? html`<span class="sc-dim"> · ${ago(u.at, now)}</span>` : null}</span>
          <${Window} label="5h" w=${u.five_hour} now=${now} />
          <${Window} label="7d" w=${u.seven_day} now=${now} />
        </div>`;
      })}
    </section>
  `;
}

export function Fleet({ snap, stale, link, go }) {
  const [filter, setFilter] = useState(() => pref("filter") || "");
  const [, setTick] = useState(0);

  // Ages, the NEW marks and the usage's staleness move with the clock, not
  // only with the stream.
  useEffect(() => {
    const t = setInterval(() => setTick((n) => n + 1), 30000);
    return () => clearInterval(t);
  }, []);

  const everything = allSessions(snap);
  useEffect(() => {
    pruneSeen(everything.map((s) => s.id));
  }, [snap]);

  if (!snap) return html`<section class="sc-screen"><p class="sc-dim">Loading…</p></section>`;

  const workspaces = shownWorkspaces(snap);
  const shown = new Set(workspaces.map((w) => String(w.id)));
  const active = shown.has(filter) ? filter : "";
  const names = new Map(workspaces.map((w) => [String(w.id), w.label || w.name]));
  const groups = groupSessions(everything.filter((s) => shown.has(String(s.workspace))), active);
  const seen = readSeen();
  const now = Date.now();
  const listed = GROUPS.reduce((n, g) => n + groups[g.key].length, 0);
  const oldest = groups.needs_you.length > 0 ? ago(groups.needs_you[0].since, now) : "";
  const fresh = groups.your_turn.filter((s) => hasNewTurn(s, seen)).length;
  const agents = groups.working.reduce((n, s) => n + runningAgents(s), 0);
  const chipOf = (s) => (active === "" && workspaces.length > 1 ? names.get(String(s.workspace)) || "" : "");

  function pick(id) {
    setFilter(id);
    pref("filter", id);
  }

  const rows = (list) => list.map((s) => html`<${Row} key=${s.id} s=${s} chip=${chipOf(s)} fresh=${hasNewTurn(s, seen)} />`);

  return html`
    <section class="sc-screen sc-fleet">
      <header class="sc-header">
        <h1 class="sc-h1"><span class="sc-dim sc-light">loom@</span>${snap.host || "loom"}</h1>
        <a class="sc-icon" href="#settings" aria-label="Settings"><${GearIcon} /></a>
        <button class="sc-btn sc-primary" type="button" disabled=${stale} onClick=${() => go("#new")}>+ new</button>
      </header>
      <nav class="sc-tiles" aria-label="Sessions by state">
        <${Tile} group="needs_you" title="Needs you" count=${groups.needs_you.length} sub=${oldest ? "oldest " + oldest : ""} />
        <${Tile} group="your_turn" title="Your turn" count=${groups.your_turn.length} sub=${fresh > 0 ? fresh + " new" : ""} />
        <${Tile} group="working" title="Working" count=${groups.working.length} sub=${agents > 0 ? plural(agents, "agent") : ""} />
      </nav>
      <${UsageBoxes} usage=${snap.usage} staleAfter=${snap.usage_stale_after_s} now=${now} />
      ${workspaces.length > 1
        ? html`<nav class="sc-chips" aria-label="Workspaces">
            <button class=${"sc-chip-btn" + (active === "" ? " sc-on" : "")} type="button" aria-pressed=${active === ""} onClick=${() => pick("")}><span>all</span></button>
            ${workspaces.map(
              (w) => html`<button
                key=${w.id}
                class=${"sc-chip-btn" + (active === String(w.id) ? " sc-on" : "")}
                type="button"
                aria-pressed=${active === String(w.id)}
                onClick=${() => pick(String(w.id))}
              ><span>${w.label || w.name}</span></button>`,
            )}
          </nav>`
        : null}
      ${listed === 0 ? html`<p class="sc-dim sc-empty">No sessions${active ? " in this workspace" : ""}.</p>` : null}
      ${GROUPS.filter((g) => g.key !== "paused" && groups[g.key].length > 0).map(
        (g) => html`<section key=${g.key} class="sc-group">
          <h2 id=${"g-" + g.key} class=${"sc-group-title sc-t-" + g.key}>${g.title} ${groups[g.key].length}</h2>
          ${rows(groups[g.key])}
        </section>`,
      )}
      ${groups.paused.length > 0
        ? html`<details class="sc-group">
            <summary class="sc-group-title sc-t-paused">Paused ${groups.paused.length}</summary>
            ${rows(groups.paused)}
          </details>`
        : null}
      <footer class="sc-status">
        <span class=${"sc-link sc-link-" + (LINK[link] ? link : "offline")}>${LINK[link] || LINK.offline}</span>
        <span>${snap.host}</span>
        <span class="sc-sep" aria-hidden="true">│</span>
        <span>${plural(listed, "session")}</span>
        <span class="sc-fill"></span>
        ${groups.paused.length > 0 ? html`<span class="sc-dim">${groups.paused.length} paused</span>` : null}
      </footer>
    </section>
  `;
}
```

- [ ] **Step 7: Rewrite the session screen**

Replace `internal/web/static/js/session.mjs` with:

```js
// The session screen: why it waits, its tiles, the last message, its agents,
// the actions, and the reply box with the quick replies.
import { html, useState, useEffect } from "../lib/preact-htm.mjs";
import { call } from "./api.mjs";
import { ConfirmSheet, utf8Len } from "./sheets.mjs";
import { allSessions, ago, markSeen } from "./fleet.mjs";
import { GLYPHS, glyphOf, OutIcon } from "./skin.mjs";
import { usageFor, shareText, isStale } from "./usage.mjs";
import { Markdown } from "./markdown.mjs";

export const MAX_REPLY = 64 * 1024;

// actionsFor says which buttons to show. It follows status only; the
// daemon's admit stays the authority and a refusal comes back as a toast.
// It shows only what admit accepts: every op but resume is refused while the
// session is Loading or Deleting, and resume is refused unless it is Paused
// (a Recoverable or Not started session has nothing to resume).
export function actionsFor(s) {
  const busy = s.status === "Loading" || s.status === "Deleting";
  const paused = s.group === "paused";
  // Pause keeps the worktree, so push and merge work on a Paused session; a
  // Recoverable or Not started one may have no worktree to work on.
  const tree = !paused || s.status === "Paused";
  return {
    pause: !busy && !paused,
    resume: s.status === "Paused",
    push: !busy && tree,
    merge: !busy && tree,
    send: !busy && !paused,
    kill: !busy,
  };
}

// mergeCandidates: the other sessions of the same workspace, whose branch
// can be merged into this one.
export function mergeCandidates(snap, s) {
  return allSessions(snap).filter((o) => o.workspace === s.workspace && o.id !== s.id);
}

// parityText is the TUI's parity token: "↑2 ↓0", "✓ even" at 0 and 0, and ""
// while parity is unknown.
export function parityText(s) {
  if (!s.parity_known) return "";
  if (!s.ahead && !s.behind) return "✓ even";
  return "↑" + s.ahead + " ↓" + s.behind;
}

// checksText is the PR tile's sub-line: an open PR's checks, else its state.
export function checksText(pr) {
  if (pr.state !== "open") return pr.state;
  return { passing: "✓ checks pass", pending: "checks pending", failing: "✗ checks fail", none: "no checks" }[pr.checks] || "";
}

// Tiles are the session's facts as boxes: its changes, its pull request (a
// link to it on GitHub when the daemon has its URL) and its account's usage.
// A tile whose data is missing is left out, and the row reflows.
function Tiles({ s, snap, now }) {
  const tiles = [];
  const d = s.diff || {};
  if (d.added || d.removed || s.parity_known) {
    const parity = parityText(s);
    tiles.push(html`<div key="changes" class="sc-box sc-tile">
      <span class="sc-legend">Changes</span>
      <span class="sc-tile-fig"><span class="sc-add">+${d.added || 0}</span> <span class="sc-del">−${d.removed || 0}</span></span>
      ${parity ? html`<span class="sc-tile-sub">${parity}</span>` : null}
    </div>`);
  }
  if (s.pr) {
    const inner = html`
      <span class="sc-legend">PR</span>
      <span class="sc-tile-fig">#${s.pr.number}${s.pr.url ? html`<${OutIcon} />` : null}</span>
      <span class="sc-tile-sub">${checksText(s.pr)}</span>
    `;
    tiles.push(
      s.pr.url
        ? html`<a key="pr" class="sc-box sc-tile" href=${s.pr.url} target="_blank" rel="noopener noreferrer">${inner}</a>`
        : html`<div key="pr" class="sc-box sc-tile">${inner}</div>`,
    );
  }
  const u = usageFor(snap, s.account);
  const w = u && (u.five_hour || u.seven_day);
  if (w) {
    const old = isStale(u.at, snap.usage_stale_after_s, now);
    tiles.push(html`<div key="account" class=${"sc-box sc-tile" + (old ? " sc-stale" : "")}>
      <span class="sc-legend">Account</span>
      <span class="sc-tile-fig">${shareText(w, now)}</span>
      <span class="sc-tile-sub">${u.account} ${u.five_hour ? "5h" : "7d"}</span>
    </div>`);
  }
  return tiles.length > 0 ? html`<div class="sc-tiles">${tiles}</div>` : null;
}

// Message is the last message: its tree once fetched for this very text, the
// plain text before that and whenever the fetch fails.
function Message({ s, tree }) {
  const ok = tree && tree.text === s.last_message;
  return html`
    <section class="sc-section">
      <h2 class="sc-rule">Last message</h2>
      ${ok ? html`<${Markdown} blocks=${tree.blocks} />` : html`<p class="sc-pre">${s.last_message}</p>`}
      ${ok && tree.truncated
        ? html`<p class="sc-dim sc-small">… continues in ${s.remote_url ? html`<a href=${s.remote_url} target="_blank" rel="noopener">Claude</a>` : "Claude"}</p>`
        : null}
    </section>
  `;
}

// Agents are the session's subagents by name, each running or idle, with its
// description under it.
function Agents({ agents }) {
  return html`
    <section class="sc-section">
      <h2 class="sc-rule">Agents ${agents.length}</h2>
      ${agents.map(
        (a, i) => html`<div key=${i} class="sc-agent">
          <div class="sc-row-top">
            <span class="sc-glyph" aria-hidden="true">${a.idle ? "○" : "◐"}</span>
            <span class="sc-row-title">${a.name}</span>
            <span class="sc-age">${a.idle ? "idle" : "running"}</span>
          </div>
          ${a.description ? html`<div class="sc-agent-desc">${a.description}</div>` : null}
        </div>`,
      )}
    </section>
  `;
}

export function Session({ snap, stale, route, go, act, toast, pending }) {
  const id = route.id;
  const [text, setText] = useState("");
  const [sheet, setSheet] = useState("");
  const [source, setSource] = useState("");
  const [busy, setBusy] = useState(false);
  const [tree, setTree] = useState(null);

  // The time this phone last opened the session feeds the fleet's NEW mark;
  // it is written on arrival and again on leaving, so a turn that ended while
  // the session was open does not light it.
  useEffect(() => {
    setText("");
    setSheet("");
    setTree(null);
    if (id === undefined) return undefined;
    markSeen(id);
    return () => markSeen(id);
  }, [id]);

  const s = snap && id !== undefined ? allSessions(snap).find((x) => x.id === id) : undefined;
  const msg = s ? s.last_message : "";

  // The tree is fetched for the message the stream holds, and again when the
  // stream brings another. The plain text shows until it arrives, and stays
  // when the fetch fails or the app is offline.
  useEffect(() => {
    if (id === undefined || !msg || stale || (tree && tree.text === msg)) return undefined;
    let live = true;
    call("GET", "/api/v1/sessions/" + id + "/message").then(({ status, data }) => {
      if (live && status === 200 && data && data.text === msg) setTree(data);
    });
    return () => {
      live = false;
    };
  }, [id, msg, stale]);

  if (!s) {
    return html`
      <section class="sc-screen">
        <nav class="sc-topbar"><a class="sc-back" href="#">‹ fleet</a></nav>
        <p class="sc-dim sc-empty">${snap && pending !== id ? "This session is gone." : "Loading…"}</p>
      </section>
    `;
  }

  const can = actionsFor(s);
  const off = stale || busy;
  const base = "/api/v1/sessions/" + s.id;
  const candidates = mergeCandidates(snap, s);
  const reply = text.trim();
  const tooLong = utf8Len(reply) > MAX_REPLY;
  const replies = snap.quick_replies || [];
  const waiting = s.group === "needs_you";
  const ws = (snap.workspaces || []).find((w) => w.id === s.workspace);
  const now = Date.now();

  async function run(action, body) {
    setBusy(true);
    try {
      const r = await act("POST", base + "/" + action, body);
      if (r.status === 202) toast("Still running; the result will show here.", false);
      return r;
    } finally {
      setBusy(false);
    }
  }

  async function send() {
    const r = await run("send", { text: reply });
    if (r.ok && r.status !== 202) setText("");
  }

  async function kill() {
    const r = await run("kill");
    setSheet("");
    if (r.ok && r.status !== 202) go("#");
  }

  async function pushBranch() {
    await run("push");
    setSheet("");
  }

  async function merge() {
    await run("merge", { source: Number(source) });
    setSheet("");
  }

  function openMerge() {
    if (candidates.length === 0) {
      toast("No other session in this workspace to merge in.", true);
      return;
    }
    setSource(String(candidates[0].id));
    setSheet("merge");
  }

  return html`
    <section class="sc-screen sc-session">
      <nav class="sc-topbar" aria-label="Session">
        <a class="sc-back" href="#">‹ fleet</a>
        <span class="sc-fill"></span>
        ${s.remote_url && !waiting
          ? html`<a class="sc-claude" href=${s.remote_url} target="_blank" rel="noopener">claude <${OutIcon} /></a>`
          : null}
      </nav>
      <h1 class="sc-h1 sc-wrap">${s.title}</h1>
      <div class="sc-subline">
        <span><span class=${"sc-glyph sc-glyph-" + s.group} aria-hidden="true">${glyphOf(s.group)}</span> ${s.label.toLowerCase()}${s.since ? " · " + ago(s.since, now) : ""}</span>
        ${ws ? html`<span class="sc-ws">${ws.label || ws.name}</span>` : null}
        ${s.branch ? html`<span>${s.branch}</span>` : null}
      </div>
      ${waiting
        ? html`<div class="sc-box sc-attn-box">
            <span class="sc-legend">${GLYPHS.needs_you} Needs you</span>
            <div class="sc-attn">${(s.wait && s.wait.reason) || "Needs you"}</div>
            ${s.wait && s.wait.detail ? html`<pre class="sc-detail-box">${s.wait.detail}</pre>` : null}
            ${s.remote_url
              ? html`<a class="sc-btn sc-primary sc-block" href=${s.remote_url} target="_blank" rel="noopener">Open in Claude</a>`
              : html`<p class="sc-dim sc-small">Remote Control is off for this session.</p>`}
          </div>`
        : null}
      <${Tiles} s=${s} snap=${snap} now=${now} />
      ${s.last_message ? html`<${Message} s=${s} tree=${tree} />` : null}
      ${s.agents && s.agents.length > 0 ? html`<${Agents} agents=${s.agents} />` : null}

      <section class="sc-actions">
        ${can.pause ? html`<button class="sc-btn" type="button" disabled=${off} onClick=${() => run("pause")}>pause</button>` : null}
        ${can.resume ? html`<button class="sc-btn sc-primary" type="button" disabled=${off} onClick=${() => run("resume")}>resume</button>` : null}
        ${can.push ? html`<button class="sc-btn" type="button" disabled=${off} onClick=${() => setSheet("push")}>push</button>` : null}
        ${can.merge ? html`<button class="sc-btn" type="button" disabled=${off} onClick=${openMerge}>merge</button>` : null}
        ${can.kill ? html`<button class="sc-btn sc-danger-text" type="button" disabled=${off} onClick=${() => setSheet("kill")}>kill</button>` : null}
      </section>

      ${can.send
        ? html`<form class="sc-reply" onSubmit=${(e) => { e.preventDefault(); if (!off && reply && !tooLong) send(); }}>
            ${text === "" && replies.length > 0
              ? html`<div class="sc-quick" role="group" aria-label="Quick replies">
                  ${replies.map(
                    (q) => html`<button key=${q} class="sc-chip-btn" type="button" disabled=${off} onClick=${() => setText(q)}><span>${q}</span></button>`,
                  )}
                </div>`
              : null}
            <div class="sc-reply-row">
              <textarea
                rows="2"
                placeholder="reply, or /command…"
                aria-label="Reply"
                value=${text}
                onInput=${(e) => setText(e.target.value)}
              ></textarea>
              <button class="sc-btn sc-primary" type="submit" disabled=${off || !reply || tooLong}>send</button>
            </div>
            ${tooLong ? html`<div class="sc-small sc-err">Too long: a reply is at most 64 KiB.</div>` : null}
          </form>`
        : s.group === "paused"
          ? html`<p class="sc-dim sc-small">Resume the session to reply.</p>`
          : null}

      ${sheet === "kill"
        ? html`<${ConfirmSheet}
            title="Kill this session?"
            confirm="Kill"
            danger=${true}
            body=${html`<div>
              <p>${s.tmux ? html`It ends tmux session <code>${s.tmux}</code>.` : "It ends the session's tmux session."}</p>
              <p>${s.worktree ? html`It force-removes worktree <code>${s.worktree}</code>` : "It force-removes the session's worktree"}, discarding its uncommitted changes. Push first to keep them.</p>
              ${s.status === "Paused"
                ? html`<p>If an older loom paused it, its worktree is gone and its changes may be in a stash, which Kill drops.</p>`
                : null}
              <p>It deletes branch <code>${s.branch}</code> unless the session checked out an existing branch.</p>
            </div>`}
            onConfirm=${kill}
            onCancel=${() => setSheet("")}
          />`
        : null}
      ${sheet === "push"
        ? html`<${ConfirmSheet}
            title="Push this branch?"
            confirm="Commit and push"
            body=${html`<p>This commits the session's uncommitted changes first, then pushes <code>${s.branch}</code>.</p>`}
            onConfirm=${pushBranch}
            onCancel=${() => setSheet("")}
          />`
        : null}
      ${sheet === "merge"
        ? html`<${ConfirmSheet}
            title="Merge a branch in"
            confirm="Merge"
            body=${html`<p>Merge which session's branch into <code>${s.branch}</code>?</p>
              <select value=${source} onChange=${(e) => setSource(e.target.value)} aria-label="Session to merge in">
                ${candidates.map((o) => html`<option key=${o.id} value=${String(o.id)}>${o.title} (${o.branch})</option>`)}
              </select>`}
            onConfirm=${merge}
            onCancel=${() => setSheet("")}
          />`
        : null}
    </section>
  `;
}
```

- [ ] **Step 8: Settings, the new-session picker, and the app's events**

In `internal/web/static/js/settings.mjs`, add after `clock`:

```js
// replyLines is the editor's replies: one a line, blank lines dropped. The
// daemon trims each and drops a duplicate after its first.
export function replyLines(text) {
  return text
    .split("\n")
    .map((l) => l.trim())
    .filter((l) => l !== "");
}

// QuickReplies edits the list every paired phone offers above its reply box.
// The box shows the stored list until the person types; Save sends the
// lines, and Reset to defaults sends null. A refused list stays in the box,
// and act shows the daemon's reason.
function QuickReplies({ snap, stale, act }) {
  const [draft, setDraft] = useState(null);
  const [saved, setSaved] = useState(null);
  const [busy, setBusy] = useState(false);

  // A list from the stream (this phone's save, or another phone's) replaces
  // the last answer.
  useEffect(() => {
    setSaved(null);
  }, [snap && snap.quick_replies]);

  const current = saved || snap.quick_replies || [];
  const shown = draft === null ? current.join("\n") : draft;

  async function save(replies) {
    setBusy(true);
    try {
      const r = await act("PUT", "/api/v1/quick-replies", { replies });
      if (r.ok && r.data && Array.isArray(r.data.quick_replies)) {
        setSaved(r.data.quick_replies);
        setDraft(null);
      }
    } finally {
      setBusy(false);
    }
  }

  return html`
    <section class="sc-section">
      <h2 class="sc-group-title">Quick replies</h2>
      <p class="sc-dim sc-small">One a line, at most 12. Every paired phone offers them above its reply box.</p>
      <textarea
        class="sc-quick-edit"
        rows="6"
        aria-label="Quick replies"
        value=${shown}
        disabled=${stale || busy}
        onInput=${(e) => setDraft(e.target.value)}
      ></textarea>
      <div class="sc-buttons">
        <button class="sc-btn" type="button" disabled=${stale || busy} onClick=${() => save(null)}>Reset to defaults</button>
        <button class="sc-btn sc-primary" type="button" disabled=${stale || busy || draft === null} onClick=${() => save(replyLines(shown))}>Save</button>
      </div>
    </section>
  `;
}
```

In `Settings`'s markup, replace `<a class="sc-back" href="#">‹ Fleet</a>` with `<nav class="sc-topbar"><a class="sc-back" href="#">‹ fleet</a></nav>`, and add `<${QuickReplies} snap=${snap} stale=${stale} act=${act} />` after the Notifications `</section>`.

In `internal/web/static/js/newsession.mjs`, replace the `ws` state's initialiser so it starts on a shown workspace:

```js
  const [ws, setWs] = useState(() => {
    const want = pref("filter") || "";
    const hit = workspaces.find((w) => String(w.id) === want);
    const first = workspaces.find((w) => w.shown !== false) || workspaces[0];
    return String(hit ? hit.id : first ? first.id : "");
  });
```

after `const chosen = …`, add:

```js
  const active = workspaces.filter((w) => w.shown !== false);
  const others = workspaces.filter((w) => w.shown === false);
  const option = (w) => html`<option key=${w.id} value=${String(w.id)}>${w.label || w.name}</option>`;
```

replace the workspace `<select>`'s options:

```js
          <select value=${ws} onChange=${(e) => setWs(e.target.value)}>
            ${others.length === 0
              ? workspaces.map(option)
              : html`<optgroup label="Active">${active.map(option)}</optgroup>
                  <optgroup label="Other workspaces">${others.map(option)}</optgroup>`}
          </select>
```

and its back link with `<nav class="sc-topbar"><a class="sc-back" href="#">‹ fleet</a></nav>`.

In `internal/web/static/js/app.mjs`, add two handlers to `connect({…})`, after `device(d)`:

```js
      usage(d) {
        apply((s) => ({ ...s, usage: d.usage }));
      },
      quick_replies(d) {
        apply((s) => ({ ...s, quick_replies: d.quick_replies }));
      },
```

pass the stream's state to the screens, replacing the `props` line:

```js
    const link = !stale ? "live" : restarting ? "restarting" : "offline";
    const props = { snap, stale, link, route, go, act, toast, pending };
```

and name `link` in the file's first comment: `as the props {snap, stale, link, route, go, act, toast, pending}`. In `internal/web/static/js/screens.mjs`, the comment's props list becomes `{snap, stale, link, route, go, act, toast, pending}`.

- [ ] **Step 9: Check the modules and the static tests**

Run: `for f in internal/web/static/js/*.mjs; do node --check "$f" || echo "FAIL $f"; done; grep -n ' style=' internal/web/static/js/*.mjs; CGO_ENABLED=0 go test ./internal/web/ -run 'TestStatic|TestMux'`
Expected: no `FAIL` line, no `style=` match, and `ok` (every reference resolves, every named import is exported, no module sets a style attribute, the braces balance).

- [ ] **Step 10: Commit**

```bash
git add -- internal/web/static_test.go internal/web/static/index.html internal/web/static/app.css internal/web/static/js/skin.mjs internal/web/static/js/usage.mjs internal/web/static/js/markdown.mjs internal/web/static/js/fleet.mjs internal/web/static/js/session.mjs internal/web/static/js/settings.mjs internal/web/static/js/newsession.mjs internal/web/static/js/app.mjs internal/web/static/js/screens.mjs
git commit -F - -- internal/web/static_test.go internal/web/static/index.html internal/web/static/app.css internal/web/static/js/skin.mjs internal/web/static/js/usage.mjs internal/web/static/js/markdown.mjs internal/web/static/js/fleet.mjs internal/web/static/js/session.mjs internal/web/static/js/settings.mjs internal/web/static/js/newsession.mjs internal/web/static/js/app.mjs internal/web/static/js/screens.mjs <<'EOF'
feat(web): loom's skin on the phone, with tiles, usage, the message and quick replies

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_019icsHbrFq5LeZ4b4jFkSBe
EOF
```

### Task D: Docs, the full gates and the phone checklist

**Files:**
- Modify: `internal/web/README.md`, `internal/web/CLAUDE.md`, `docs/claude/web-layer.md`, `USAGE.md`

- [ ] **Step 1: The web layer's README**

In `internal/web/README.md`'s Files table, replace the `fleet.go` row and add a `markdown.go` row under it:

```markdown
| `fleet.go` | the projection: `sessionJSON`, `workspaceJSON` and which workspaces the fleet shows (`layout`), `usageJSON`, `deviceJSON`, `snapshotJSON`, `noticeJSON`, `groupOf`, `deviceOf`, `noticeOf` |
| `markdown.go` | the message tree: `buildTree` parses a last message with goldmark into the plain nodes the app draws (`mdNode`), with raw HTML as text and only `http(s)` links (`safeHref`) |
```

In the Routes table, add after the `merge` row:

```markdown
| `GET /api/v1/sessions/{id}/message` | `{text, blocks, truncated}`: the session's last message as a tree of plain nodes (`markdown.go`), built from the text the web client's replica holds; the app draws it as elements, and only while `text` is the message it shows |
```

and after the `PUT /api/v1/device` row:

```markdown
| `PUT /api/v1/quick-replies` | `{replies}`: the quick replies every paired phone offers above its reply box, or `null` (or no field) for the defaults (`core.DefaultQuickReplies`); a list `core.NormalizeQuickReplies` refuses is 400 with its words; answers `{"quick_replies"}`, the list as the app reads it |
```

In The stream, replace the paragraph's first three sentences (through "`bye` when the daemon stops.") with:

```markdown
`GET /api/v1/stream` is server-sent events. Every connect starts with a `snapshot` (the build, the host name, the workspaces in the order the app lists them, each flagged `shown` when it is open as a tab in loom or holds a session that isn't paused, or when no tab is open; the sessions by workspace; the device; `badge` (the sessions that need you across the served workspaces); the last `recentNotices` (20) notices; `usage`, each account's plan usage, with `usage_stale_after_s` (`core.UsageStaleAfter`), the age past which the app dims a sample; and `quick_replies`, the list the app offers), so nothing is replayed. After it: `sessions` (one workspace's whole list when it changes, at most four a second: `publishEvery`), `workspaces` (also when a `shown` flag or the order changes), `usage`, `quick_replies`, `device`, `notice`, `ping` every `pingEvery` (20 s, since Cloudflare closes a stream idle for 125 s) and `bye` when the daemon stops.
```

At the end of The app, add:

```markdown
The skin is loom's TUI on the phone (spec `docs/superpowers/specs/2026-10-23-pwa-skin-glance-design.md`): the afterglow colours as `app.css`'s tokens, dark only, `ui-monospace` type (SF Mono on the iPhone), glyph rows, and boxes whose label sits in their top border (`js/skin.mjs` holds the glyphs and icons). The fleet lists the `shown` workspaces under count tiles and the usage boxes (`js/usage.mjs` formats a window), with a status line at the bottom; a session's screen fetches its message's tree on demand and draws it with `js/markdown.mjs`, showing the plain text until it arrives, and fills its reply box from the quick-reply chips, edited in Settings.
```

- [ ] **Step 2: The web layer's rule, and the guide**

Check the budget first: `go run ./tools/claudemd -v 2>&1 | grep internal/web` (14,428 of 20,000 bytes on 2026-10-23). In `internal/web/CLAUDE.md`, add after the rule that starts "**Serve the app's shell public and free of data**":

```markdown
- **Draw a session's last message only from its tree (`buildTree` in `internal/web/markdown.go`, served by `GET /api/v1/sessions/{id}/message`), as elements, and keep the link check (`safeHref`) in Go.** The message is Claude's text and can hold markup; an HTML string put into the page renders it, links and forms included, and the CSP stops only its scripts. **Enforced** for the tree by `TestBuildTree_KeepsNoMarkupAndFollowsNoUnsafeLink`; that no module puts an HTML string into the page is **Convention** — a message that rewrites the phone's screen.
```

In `docs/claude/web-layer.md`, add to the end of "Changing the app":

```markdown
- The skin is dark only, its tokens on `app.css`'s `:root` (`TestStatic_TheSkinIsDarkWithEveryToken`). A form control's text is 16px: iOS zooms into a smaller one.
- A last message is drawn from its tree (`GET /api/v1/sessions/{id}/message`), never from an HTML string. A node the app needs is added in `internal/web/markdown.go`, with a case in `TestBuildTree_EachNodeType`.
```

- [ ] **Step 3: USAGE.md**

In "Use Loom from Your Phone", replace the **The fleet.** bullet with:

```markdown
- **The fleet.** Tiles count the sessions that **need you** (with the
  oldest wait), are **your turn** (with how many ended since this phone
  last opened them) and are **working** (with their running subagents); a
  tap jumps to the group. Below them, each account's plan usage: the
  5-hour and 7-day windows, how much is used and when each resets, dimmed
  once the sample is old. Sessions are listed by group: **Needs you**
  (longest wait first, with the reason and, for a permission request, the
  command, file or address), **Your turn**, **Working**, and **Paused**
  (folded away). The workspace chips are the workspaces open as tabs in
  loom, in tab order, plus any with a session that isn't paused (all of
  them while no tab is open); the phone remembers the chip. A session
  leads with why it waits, then tiles for its changes, its pull request
  (which opens on GitHub) and its account's usage, its last message as
  Claude formatted it, its subagents by name, the actions, and a reply box.
```

and add after the **Replying.** bullet:

```markdown
- **Quick replies.** While the reply box is empty, chips above it fill it
  with a reply you use often; Send sends it. They start as `yes`,
  `continue`, `/compact` and `/clear`; edit them in Settings, one a line,
  at most 12. Every paired phone shares the list.
```

- [ ] **Step 4: Run every gate**

Run, in order:

```bash
gofmt -l session/github core ui internal/web
CGO_ENABLED=0 go vet ./...
CGO_ENABLED=0 go test ./...
CC=clang CGO_ENABLED=1 go test -race ./internal/web/ ./core/
go run ./tools/claudemd -v
for f in internal/web/static/js/*.mjs; do node --check "$f" || echo "FAIL $f"; done
```

Expected: `gofmt` prints nothing; vet is quiet; every package is `ok` (`TestRepo_ClaudeMDStructure` among them, which checks the new rule's paths and test name); the race run passes; the CLAUDE.md budgets hold; no `FAIL`. Say plainly that golangci-lint was skipped (the dev shell's v2 rejects the repo's v1 config).

- [ ] **Step 5: Commit**

```bash
git add -- internal/web/README.md internal/web/CLAUDE.md docs/claude/web-layer.md USAGE.md
git commit -F - -- internal/web/README.md internal/web/CLAUDE.md docs/claude/web-layer.md USAGE.md <<'EOF'
docs(web): the skin, the message tree, usage and quick replies

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_019icsHbrFq5LeZ4b4jFkSBe
EOF
```

- [ ] **Step 6: The phone checklist (the owner's)**

Nothing here runs in a loom pane as loom itself: a dev build goes through the `loom-dev` skill, and the deployed daemon is the owner's to restart. On the iPhone, with the app installed and paired:

1. The fleet opens in the skin: dark, monospace, the three tiles, the usage boxes (with an account besides default), the status line saying `LIVE`.
2. A tile with a count scrolls to its group; a tile at 0 does nothing.
3. Open and close a workspace tab in the TUI: its chip comes and goes; a closed workspace with a running session keeps its chip until the session pauses.
4. A session whose last message has a heading, a list, a table and a code block shows them formatted; a link opens in a new tab; a message holding `<b>` shows the tag as text.
5. The PR tile opens the pull request on GitHub.
6. Quick-reply chips fill the empty reply box and vanish once you type; Send sends the reply. Edit the list in Settings, save, and see the chips change (on a second phone too, when there is one); Reset to defaults restores `yes`, `continue`, `/compact`, `/clear`; a 13th line is refused with the reason, and the text stays.
7. The checks of the web-layer guide still pass: notifications of both kinds, Open in Claude, a reply, kill with its confirmation, revoke.
8. Turn the phone's network off for five minutes: the status line says `OFFLINE`, the stale bar shows, the usage dims with its age; back online, all of it recovers.
