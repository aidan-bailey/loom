# Borrow Setting Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. The owner chose inline execution.

**Goal:** A `Job Borrowing` row in the settings menu (`S`) that turns the global `scheduler.borrow` on or off.

**Architecture:** A new request, `Core.ConfigureBorrow(on bool) error`, rewrites only that key of the queue's global `config.json` and runs an admission pass. The settings overlay's row turns Enter into a pending request the app sends, as Build Sweep's does. The row's value comes from `QueueView.Settings.Borrow`, which the app refreshes on `QueueChanged`. Spec: `docs/superpowers/specs/2026-11-06-borrow-setting-design.md`.

**Tech Stack:** Go 1.25, Bubble Tea v2, testify.

**Rules:**
- Run builds and tests through `loom job run -d … -- …`, using `CGO_ENABLED=0`.
- Commit by path.
- Never edit `core/rpc/methods_gen.go` or `docs/specs/protocol.md` by hand.

---

### Task 1: `Core.ConfigureBorrow`

**Files:**
- `core/iface.go` (after `SetSchedulerCaps`)
- `core/queue_ops.go` (after `setSchedulerCaps`)
- `core/loop_core.go` (after `ConfigureBuildSweep`'s forwarder)
- `core/rpc/methods_gen.go` and `docs/specs/protocol.md` (both generated)
- Tests: `core/queue_borrow_test.go`, `core/rpc/client_test.go`

- [ ] **Step 1: Failing tests.** Append to `core/queue_borrow_test.go` (it needs the `config` import):

```go
// TestConfigureBorrow_SetsOnlyThatKeyOfTheGlobalConfig: the request writes
// scheduler.borrow into the queue's global config.json, keeps every other
// key, and the policy follows (borrow setting spec).
func TestConfigureBorrow_SetsOnlyThatKeyOfTheGlobalConfig(t *testing.T) {
	m := wqModel(t)
	path := filepath.Join(m.queue.cfgDir, config.ConfigFileName)
	require.NoError(t, os.WriteFile(path, []byte(`{"theme": "nord", "scheduler": {"cpus": 4, "cpu_overcommit": 3}}`), 0o644))

	require.NoError(t, m.ConfigureBorrow(true))
	cfg, err := config.ReadConfigFrom(m.queue.cfgDir)
	require.NoError(t, err)
	s := cfg.GetScheduler()
	require.NotNil(t, s.Borrow)
	assert.True(t, *s.Borrow)
	assert.Equal(t, 4, s.CPUs)
	assert.Equal(t, 3, s.CPUOvercommit)
	assert.Equal(t, "nord", cfg.GetTheme())
	assert.True(t, m.queue.policy.Borrow, "the policy follows")
	assert.True(t, *m.queueView().Settings.Borrow, "and the view")

	require.NoError(t, m.ConfigureBorrow(false))
	assert.False(t, m.queue.policy.Borrow)
}

func TestConfigureBorrow_RefusesAnUnreadableGlobalConfig(t *testing.T) {
	m := wqModel(t)
	path := filepath.Join(m.queue.cfgDir, config.ConfigFileName)
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o644))
	assert.ErrorContains(t, m.ConfigureBorrow(true), "configure borrowing")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "{not json", string(data), "left byte for byte")
}

func TestConfigureBorrow_RefusesAQueueNotLoaded(t *testing.T) {
	m := NewForTest(Options{})
	assert.ErrorIs(t, m.ConfigureBorrow(true), errQueueNotLoaded)
}
```

Append to `core/rpc/client_test.go`:

```go
// TestConfigureBorrow_CrossesTheWire: the request reaches the model, and its
// refusal crosses as an error (a model with no queue refuses it).
func TestConfigureBorrow_CrossesTheWire(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())
	c := pair(t, core.StartForTest(core.NewForTest(core.Options{})))
	assert.ErrorContains(t, c.ConfigureBorrow(true), "the work queue isn't loaded")
}
```

- [ ] **Step 2:** Run `-run 'TestConfigureBorrow' ./core/ ./core/rpc/`. Expected: a compile failure.

- [ ] **Step 3: Implement.** In `core/iface.go`, after `SetSchedulerCaps(…)`:

```go
	// Turn job borrowing (scheduler.borrow) on or off in the global
	// config.json, whichever workspace asks (borrow setting spec).
	ConfigureBorrow(on bool) error
```

In `core/queue_ops.go`, after `setSchedulerCaps`:

```go
// ConfigureBorrow turns job borrowing on or off (scheduler.borrow, borrow
// setting spec) in the queue's global config.json, whichever workspace asks
// (Core.ConfigureBorrow). The file is read strictly and only that key
// changes; a served workspace on that dir takes the block in memory, and an
// admission pass rereads it. When the queue isn't loaded, or the file can't
// be read or written, nothing changes.
func (m *Model) ConfigureBorrow(on bool) error {
	q := m.queue
	if !q.ready() {
		return errQueueNotLoaded
	}
	cfg, err := readGlobalConfig(q.cfgDir)
	if err != nil {
		return fmt.Errorf("configure borrowing: %w", err)
	}
	s := cfg.GetScheduler()
	s.Borrow = &on
	cfg.SetScheduler(s)
	if err := config.SaveConfigTo(cfg, q.cfgDir); err != nil {
		return fmt.Errorf("configure borrowing: %w", err)
	}
	if ws := m.wsByConfigDir(q.cfgDir); ws != nil && ws.cfg != nil {
		ws.cfg.SetScheduler(s)
	}
	m.admitJobs()
	return nil
}
```

In `core/loop_core.go`, after `ConfigureBuildSweep`'s forwarder:

```go
// ConfigureBorrow runs Model.ConfigureBorrow on the loop.
func (l *Loop) ConfigureBorrow(on bool) error {
	return get(l, func(m *Model) error { return m.ConfigureBorrow(on) })
}
```

Then run `go generate ./core/rpc` and `go test ./core/rpc -run TestProtocolReference -update`.

- [ ] **Step 4:** Run `-run 'TestConfigureBorrow|TestMethods_CoverCore|TestLoopForwardsEachMethodToItsNamesake|TestGenerated_IsFresh|TestProtocolReference|TestCoreIsValueTyped' ./core/ ./core/rpc/`. Expected: PASS.

### Task 2: The settings row

**Files:**
- `ui/overlay/settingsOverlay.go`
- Tests: `ui/overlay/settingsOverlay_test.go`; `app/state_settings_test.go` (the `editBuildSweep` helper)

- [ ] **Step 1: Failing tests.** Append to `ui/overlay/settingsOverlay_test.go`:

```go
// borrowLine is the Job Borrowing row of so's render.
func borrowLine(t *testing.T, so *SettingsOverlay) string {
	t.Helper()
	for line := range strings.SplitSeq(so.Render(), "\n") {
		if strings.Contains(line, "Job Borrowing") {
			return line
		}
	}
	t.Fatal("no Job Borrowing row")
	return ""
}

func TestSettingsOverlay_BorrowRowShowsTheGlobalSetting(t *testing.T) {
	so := NewSettingsOverlay(newTestSettingsCfg(), false, "")
	assert.Contains(t, borrowLine(t, so), "off (all workspaces)")
	so.SetBorrow(true)
	assert.Contains(t, borrowLine(t, so), "on (all workspaces)")
}

// TestSettingsOverlay_BorrowToggleIsARequestNotAWorkspaceSetting: the
// setting is global, so Enter asks the caller for the opposite value, and
// the workspace's settings neither change nor save.
func TestSettingsOverlay_BorrowToggleIsARequestNotAWorkspaceSetting(t *testing.T) {
	for _, from := range []bool{false, true} {
		cfg := newTestSettingsCfg()
		so := NewSettingsOverlay(cfg, false, "")
		so.SetBorrow(from)
		so.cursor = int(settingsFieldBorrow)
		closed, changed := so.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
		assert.False(t, closed)
		assert.False(t, changed, "the workspace's settings are not saved")
		assert.Nil(t, cfg.Scheduler, "nor changed")
		on, ok := so.TakeBorrowRequest()
		require.True(t, ok)
		assert.Equal(t, !from, on)
		_, ok = so.TakeBorrowRequest()
		assert.False(t, ok, "taken once")
	}
}
```

In `app/state_settings_test.go`'s `editBuildSweep`, the row above Accounts is now Job Borrowing, so press Up twice:

```go
	press(tea.KeyPressMsg{Code: tea.KeyUp}) // Job Borrowing
	press(tea.KeyPressMsg{Code: tea.KeyUp}) // Build Sweep
```

- [ ] **Step 2:** Run `./ui/overlay/`. Expected: a compile failure.

- [ ] **Step 3: Implement.**
  - Add `settingsFieldBorrow` after `settingsFieldBuildSweep` in the `const` block, with the label `"Job Borrowing"`.
  - Add these fields, beside `buildSweepHours`:

```go
	// borrow is the global job borrowing setting the Job Borrowing row
	// shows (SetBorrow), and borrowReq a toggle waiting for the caller
	// (TakeBorrowRequest). The setting is global, so the row never touches
	// cfg.
	borrow    bool
	borrowReq *bool
```

  - In the Enter switch:

```go
	case settingsFieldBorrow:
		on := !s.borrow
		s.borrowReq = &on
```

  - In `valueFor`:

```go
	case settingsFieldBorrow:
		if s.borrow {
			return "on (all workspaces)"
		}
		return "off (all workspaces)"
```

  - Add the setter and the take:

```go
// SetBorrow supplies the setting the Job Borrowing row shows: the global
// scheduler.borrow, which the caller reads from the model's queue view.
func (s *SettingsOverlay) SetBorrow(on bool) { s.borrow = on }

// TakeBorrowRequest returns and clears the value a Job Borrowing toggle
// asked for. Callers poll it after HandleKeyPress and send it to the model
// (Core.ConfigureBorrow).
func (s *SettingsOverlay) TakeBorrowRequest() (bool, bool) {
	if s.borrowReq == nil {
		return false, false
	}
	on := *s.borrowReq
	s.borrowReq = nil
	return on, true
}
```

- [ ] **Step 4:** Run `./ui/overlay/`. Expected: PASS.

### Task 3: The app

**Files:**
- `app/intents.go` (`runOpenSettings`)
- `app/state_settings.go`
- `app/core_glue.go` (the `QueueChanged` case)
- Test: `app/state_settings_test.go`

- [ ] **Step 1: Failing tests.** Append to `app/state_settings_test.go`:

```go
// toggleBorrow presses Enter on the Job Borrowing row.
func toggleBorrow(m *home) {
	press := func(msg tea.KeyPressMsg) { handleStateSettingsKey(m, msg) }
	for i := 0; i < 20; i++ {
		press(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	press(tea.KeyPressMsg{Code: tea.KeyUp}) // Job Borrowing
	press(tea.KeyPressMsg{Code: tea.KeyEnter})
}

// TestSettingsBorrow_ReachesTheModel: the toggle is ConfigureBorrow, whose
// refusal (this package's models have no queue) reaches the error line.
func TestSettingsBorrow_ReachesTheModel(t *testing.T) {
	m := newTestHomeWithWsCtx(t)
	openSettingsForTest(t, m)
	toggleBorrow(m)
	assert.Contains(t, m.errBox.String(), "the work queue isn't loaded")
}

// TestSettingsBorrow_AQueueChangeRefreshesTheRow: another client's toggle
// reaches an open overlay with its QueueChanged.
func TestSettingsBorrow_AQueueChangeRefreshesTheRow(t *testing.T) {
	m := newTestHomeWithWsCtx(t)
	so := openSettingsForTest(t, m)
	on := true
	_ = m.applyCoreEvent(core.QueueChanged{View: core.QueueView{Settings: config.SchedulerSettings{Borrow: &on}}})
	assert.Contains(t, so.Render(), "on (all workspaces)")
}
```

- [ ] **Step 2:** Run `-run 'TestSettingsBorrow|TestSettingsBuildSweep' ./app/`. Expected: FAIL, since the row is not wired.

- [ ] **Step 3: Implement.** In `app/state_settings.go`, after the Build Sweep block:

```go
	if on, ok := so.TakeBorrowRequest(); ok {
		// Job borrowing is global: the model writes it to the global
		// config.json, whichever workspace this overlay edits.
		if err := m.core.ConfigureBorrow(on); err != nil {
			cmds = append(cmds, m.handleError(err))
		}
		so.SetBorrow(queueBorrows(m.core.Queue()))
	}
```

In `app/intents.go`, after `so.SetBuildSweep(…)`: `so.SetBorrow(queueBorrows(m.core.Queue()))`. In `app/core_glue.go`'s `case core.QueueChanged:`, after `m.queue = ev.View`:

```go
		// Job borrowing is global: another client's toggle reaches an
		// open settings overlay here.
		if so := m.settingsOverlay(); so != nil {
			so.SetBorrow(queueBorrows(ev.View))
		}
```

Add to `app/state_settings.go`:

```go
// queueBorrows reports whether the queue's scheduler block turns job
// borrowing on (borrow setting spec).
func queueBorrows(v core.QueueView) bool { return v.Settings.Borrow != nil && *v.Settings.Borrow }
```

- [ ] **Step 4:** Run `./app/`. Expected: PASS.

### Task 4: Docs, gates, commit

- [ ] **USAGE.md:** the `borrow` table row ends "…or the settings menu's Job Borrowing row (`S`)". Add a sentence on the row after the Build Sweep row's mention in the settings menu section.
- [ ] **Spec link:** in the memory borrowing spec's §9, add "and the settings menu's Job Borrowing row ([borrow setting](2026-11-06-borrow-setting-design.md))".
- [ ] **Gates:** `loom job run -d "borrow setting gates" -- sh -c 'CGO_ENABLED=0 go vet ./... && test -z "$(gofmt -l $(git ls-files "*.go" | grep -v ^vendor/))" && GOOS=windows CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test -count=1 ./...'`. Then race, then `-tags e2e ./e2e/...`. Expected: PASS.
- [ ] **Commit:** `feat(core,app,ui,rpc): a Job Borrowing row in the settings menu`, with the paths above.
