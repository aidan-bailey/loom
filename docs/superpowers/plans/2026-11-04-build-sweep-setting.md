# Build Sweep Setting Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Build Sweep row in the settings menu that sets the global `build_sweep_after_hours`, whichever workspace is focused.

**Architecture:** The overlay turns a valid edit into a pending request; the app sends it to a new `Core.ConfigureBuildSweep` request, which rewrites only that field of the global `config.json`; a new local query `Core.BuildSweep()` serves the value from `ModelView`, and `SaveSettings` stops changing the field. Spec: [`../specs/2026-11-04-build-sweep-setting-design.md`](../specs/2026-11-04-build-sweep-setting-design.md).

**Tech Stack:** Go, Bubble Tea, testify; the `core/rpc` generator.

Rules in force: commit with explicit `-- <paths>`; `CGO_ENABLED=0 go test`; never edit `core/rpc/methods_gen.go` or `docs/specs/protocol.md` by hand; no test touches the real `~/.loom` (each package's `TestMain` isolates it, and tests that write the global `config.json` restore it).

---

### Task 1: config — a strict reader and the threshold's range

**Files:**
- Modify: `config/config.go` (add `ReadConfigFrom` after `LoadConfigFrom`)
- Modify: `config/settings.go` (add `MaxBuildSweepAfterHours`, `ValidateBuildSweepAfterHours` after `GetBuildSweepAfterHours`)
- Test: `config/config_test.go`, `config/settings_test.go`

- [ ] **Step 1: Write the failing tests**

`config/config_test.go`:

```go
func TestReadConfigFrom_MissingIsTheDefaults(t *testing.T) {
	cfg, err := ReadConfigFrom(t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, DefaultConfig().BranchPrefix, cfg.BranchPrefix)
}

func TestReadConfigFrom_ReadsTheFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ConfigFileName), []byte(`{"branch_prefix": "x/", "build_sweep_after_hours": 12}`), 0o644))
	cfg, err := ReadConfigFrom(dir)
	require.NoError(t, err)
	assert.Equal(t, "x/", cfg.BranchPrefix)
	assert.Equal(t, 12, cfg.GetBuildSweepAfterHours())
	assert.Equal(t, DefaultConfig().DefaultProgram, cfg.DefaultProgram, "a partial file backfills the defaults")
}

// TestReadConfigFrom_ACorruptFileIsAnErrorAndStays: a caller that writes
// the file back must not start from defaults, and the file is not
// quarantined (LoadConfigFrom's way).
func TestReadConfigFrom_ACorruptFileIsAnErrorAndStays(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ConfigFileName)
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o644))
	_, err := ReadConfigFrom(dir)
	assert.ErrorContains(t, err, "parse")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "nothing quarantined")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "{not json", string(data))
}
```

`config/settings_test.go`:

```go
func TestValidateBuildSweepAfterHours(t *testing.T) {
	for _, ok := range []int{0, 1, 12, MaxBuildSweepAfterHours} {
		assert.NoError(t, ValidateBuildSweepAfterHours(ok), ok)
	}
	for _, bad := range []int{-1, MaxBuildSweepAfterHours + 1} {
		assert.Error(t, ValidateBuildSweepAfterHours(bad), bad)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `CGO_ENABLED=0 go test ./config -run 'ReadConfigFrom|ValidateBuildSweep'`
Expected: build failure, `undefined: ReadConfigFrom` and `undefined: ValidateBuildSweepAfterHours`.

- [ ] **Step 3: Implement**

`config/config.go`, after `LoadConfigFrom` (add `errors` and `io/fs` imports):

```go
// ReadConfigFrom reads dir's config.json strictly, for a caller that writes
// it back: a missing file is the defaults, as in LoadConfigFrom, but a file
// that can't be read or parsed is an error and stays where it is.
// LoadConfigFrom would quarantine it and start from the defaults, which a
// write would then put in its place.
func ReadConfigFrom(dir string) (*Config, error) {
	path := filepath.Join(dir, ConfigFileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return DefaultConfig(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	cfg := DefaultConfig()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}
```

`config/settings.go`, after `GetBuildSweepAfterHours`:

```go
// MaxBuildSweepAfterHours is the largest build sweep threshold: a year.
const MaxBuildSweepAfterHours = 8760

// ValidateBuildSweepAfterHours refuses a build sweep threshold outside 0
// (off) to MaxBuildSweepAfterHours.
func ValidateBuildSweepAfterHours(hours int) error {
	if hours < 0 || hours > MaxBuildSweepAfterHours {
		return fmt.Errorf("the build sweep threshold must be 0 (off) to %d hours, not %d", MaxBuildSweepAfterHours, hours)
	}
	return nil
}
```

- [ ] **Step 4: Run them to see them pass**

Run: `CGO_ENABLED=0 go test ./config`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(config): a strict config reader and the build sweep threshold's range" -- config/config.go config/settings.go config/config_test.go config/settings_test.go
```

### Task 2: core and rpc — `ConfigureBuildSweep` and `BuildSweep`

**Files:**
- Modify: `core/iface.go` (declare both after `SweepBuilds`)
- Modify: `core/build_sweep.go` (view type, query, request, boot read, job read, delivery, error text)
- Modify: `core/model.go` (fields after `buildSweepWarned`)
- Modify: `core/load.go` (`Boot` calls `initBuildSweep` after `initWeb`)
- Modify: `core/state_views.go` (`ModelView.BuildSweep`), `core/state_publish.go` (`modelView`)
- Modify: `core/settings.go` (`settingsDir` helper; `SaveSettings` carries the field)
- Modify: `core/loop_core.go` (two forwarders), `core/rpc/replica.go` (one method), `core/events.go` (`ModelChanged`'s comment)
- Regenerate: `core/rpc/methods_gen.go`, `docs/specs/protocol.md`
- Test: `core/build_sweep_test.go`, `core/settings_test.go`, `core/state_views_test.go`, `core/rpc/client_test.go`

- [ ] **Step 1: Write the failing tests**

`core/build_sweep_test.go` (add `os`, `path/filepath` imports):

```go
// withGlobalConfig writes body as the isolated global config.json and
// restores what was there when the test ends; it returns the file's path.
func withGlobalConfig(t *testing.T, body string) string {
	t.Helper()
	dir, err := config.GetConfigDir()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, config.ConfigFileName)
	prev, readErr := os.ReadFile(path)
	t.Cleanup(func() {
		if readErr != nil {
			_ = os.Remove(path)
			return
		}
		_ = os.WriteFile(path, prev, 0o644)
	})
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}

func modelChanged(events []Event) *ModelChanged {
	for _, ev := range events {
		if mc, ok := ev.(ModelChanged); ok {
			return &mc
		}
	}
	return nil
}

func TestConfigureBuildSweep_SetsOnlyThatFieldOfTheGlobalConfig(t *testing.T) {
	path := withGlobalConfig(t, `{"default_program": "aider", "branch_prefix": "x/"}`)
	m := NewForTest(Options{})
	m.Sync()

	require.NoError(t, m.ConfigureBuildSweep(12))

	cfg, err := config.ReadConfigFrom(filepath.Dir(path))
	require.NoError(t, err)
	assert.Equal(t, 12, cfg.GetBuildSweepAfterHours())
	assert.Equal(t, "aider", cfg.DefaultProgram, "the other fields are kept")
	assert.Equal(t, "x/", cfg.BranchPrefix)
	assert.Equal(t, BuildSweepView{AfterHours: 12}, m.BuildSweep())
	mc := modelChanged(m.Sync().Events)
	require.NotNil(t, mc, "the change republishes")
	assert.Equal(t, 12, mc.View.BuildSweep.AfterHours)
}

func TestConfigureBuildSweep_RefusesAnUnreadableGlobalConfig(t *testing.T) {
	path := withGlobalConfig(t, "{not json")
	m := NewForTest(Options{})

	assert.ErrorContains(t, m.ConfigureBuildSweep(12), "parse")

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "{not json", string(data), "left byte for byte")
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	for _, e := range entries {
		assert.NotContains(t, e.Name(), "corrupt", "nothing quarantined")
	}
	assert.Zero(t, m.BuildSweep().AfterHours)
}

func TestConfigureBuildSweep_RefusesAThresholdOutOfRange(t *testing.T) {
	path := withGlobalConfig(t, `{"build_sweep_after_hours": 6}`)
	m := NewForTest(Options{})
	for _, bad := range []int{-1, config.MaxBuildSweepAfterHours + 1} {
		assert.Error(t, m.ConfigureBuildSweep(bad), bad)
	}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, `{"build_sweep_after_hours": 6}`, string(data))
}

// TestConfigureBuildSweep_TheWorkspaceSavingThatFileFollows: a served
// workspace whose config is the global file takes the value in memory, so
// its next SaveSettings, from a copy made before the change, keeps it;
// another workspace's config is left alone.
func TestConfigureBuildSweep_TheWorkspaceSavingThatFileFollows(t *testing.T) {
	global, a := storedWorkspace(t, ""), storedWorkspace(t, "a")
	t.Setenv(config.EnvHome, global.ctx.ConfigDir)
	t.Setenv(config.EnvGlobalDir, global.ctx.ConfigDir)
	m := NewForTest(Options{})
	m.SetWorkspacesForTest(global, a)
	stale := global.cfg.Snapshot()

	require.NoError(t, m.ConfigureBuildSweep(12))
	assert.Equal(t, 12, global.cfg.Snapshot().BuildSweepAfterHours)
	assert.Zero(t, a.cfg.Snapshot().BuildSweepAfterHours)

	stale.DefaultProgram = "aider"
	require.NoError(t, m.SaveSettings(m.wsIDOf(global), stale))
	cfg, err := config.ReadConfigFrom(global.ctx.ConfigDir)
	require.NoError(t, err)
	assert.Equal(t, "aider", cfg.DefaultProgram, "fixture: the save landed")
	assert.Equal(t, 12, cfg.GetBuildSweepAfterHours(), "the old copy didn't write the old threshold back")
}

func TestBuildSweepJob_ItsReadUpdatesTheView(t *testing.T) {
	withGlobalConfig(t, `{"build_sweep_after_hours": 6}`)
	m := NewForTest(Options{})
	job := buildSweepJob(buildsweep.Default(), nil, buildsweep.Options{DryRun: true}, m.buildSweepGen)

	m.Deliver(job())

	assert.Equal(t, 6, m.BuildSweep().AfterHours)
}

func TestBuildSweepJob_AReadBeforeAConfigureIsDropped(t *testing.T) {
	withGlobalConfig(t, `{"build_sweep_after_hours": 6}`)
	m := NewForTest(Options{})
	job := buildSweepJob(buildsweep.Default(), nil, buildsweep.Options{DryRun: true}, m.buildSweepGen)
	res := job() // read 6

	require.NoError(t, m.ConfigureBuildSweep(12))
	m.Deliver(res)

	assert.Equal(t, 12, m.BuildSweep().AfterHours)
}

func TestBuildSweepJob_AnUnreadableGlobalConfigFailsTheSweep(t *testing.T) {
	path := withGlobalConfig(t, "{not json")
	res := buildSweepJob(buildsweep.Default(), nil, buildsweep.Options{DryRun: true}, 0)().(buildSweepResult)

	assert.ErrorContains(t, res.err, "can't be read")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "{not json", string(data), "not quarantined")
	assert.Equal(t, 12, buildSweepJob(buildsweep.Default(), nil, buildsweep.Options{DryRun: true, AfterHours: 12}, 0)().(buildSweepResult).report.AfterHours, "a request's threshold still sweeps")
}

func TestInitBuildSweep_ReadsTheGlobalThreshold(t *testing.T) {
	withGlobalConfig(t, `{"build_sweep_after_hours": 24}`)
	m := NewForTest(Options{})

	m.initBuildSweep()

	assert.Equal(t, 24, m.BuildSweep().AfterHours)
}
```

In `TestBuildSweepJob_ReadsTheThresholdFromTheGlobalConfig`, change the helper's call to `buildSweepJob(buildsweep.Default(), targets, opts, 0)`.

`core/settings_test.go`:

```go
// TestSaveSettings_KeepsTheBuildSweepThreshold: the threshold is
// ConfigureBuildSweep's to change; a client's copy carrying another value
// writes nothing of it.
func TestSaveSettings_KeepsTheBuildSweepThreshold(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	m.SetWorkspacesForTest(ws)
	s := ws.cfg.Snapshot()
	s.BuildSweepAfterHours = 99
	s.BranchPrefix = "y/"

	require.NoError(t, m.SaveSettings(m.wsIDOf(ws), s))

	assert.Equal(t, "y/", ws.cfg.Snapshot().BranchPrefix, "fixture: the save landed")
	assert.Zero(t, ws.cfg.Snapshot().BuildSweepAfterHours)
}
```

`core/state_views_test.go`, in `TestModelView_CloneKeepsEveryField`: after `m.web.quickReplies = …` add `m.buildSweepHours = 12`; after the quick-replies `require` add `require.Equal(t, 12, v.BuildSweep.AfterHours, "fixture: and the build sweep")`; after the web-view `assert` add `assert.Equal(t, m.BuildSweep(), c.BuildSweep, "and the build sweep")`.

`core/rpc/client_test.go`, in `TestReplica_AnswersAsTheModel`: before `loop := core.StartForTest(model)` add

```go
	t.Setenv(config.EnvHome, webDir)
	require.NoError(t, model.ConfigureBuildSweep(12))
```

and after the `same("Web", …)` line add

```go
	require.Equal(t, 12, loop.BuildSweep().AfterHours, "fixture: the threshold is in the view")
	same("BuildSweep", func(k core.Core) []any { return []any{k.BuildSweep()} })
```

and a new test:

```go
// TestConfigureBuildSweep_ReachesTheModelOverTheWire: the request's
// ModelChanged reaches the replica before its reply, and its refusal
// crosses as an error.
func TestConfigureBuildSweep_ReachesTheModelOverTheWire(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())
	model := core.NewForTest(core.Options{})
	c := pair(t, core.StartForTest(model))

	require.NoError(t, c.ConfigureBuildSweep(12))
	assert.Equal(t, 12, c.BuildSweep().AfterHours)
	assert.ErrorContains(t, c.ConfigureBuildSweep(-1), "0 (off)")
}
```

(If `pair` or the fixture constructors are named differently, use the ones `TestWebRequests_ReachTheModelOverTheWire` uses.)

- [ ] **Step 2: Run them to see them fail**

Run: `CGO_ENABLED=0 go test ./core ./core/rpc`
Expected: build failure: `m.ConfigureBuildSweep undefined`, `BuildSweepView undefined`, `too many arguments in call to buildSweepJob`.

- [ ] **Step 3: Implement**

`core/iface.go`, after `SweepBuilds(…)`:

```go
	// The build sweep's setting, the global config's threshold (0 for
	// off), and the request that sets it whichever workspace asks.
	BuildSweep() BuildSweepView // rpc:local
	ConfigureBuildSweep(afterHours int) error
```

`core/model.go`, after `buildSweepWarned`:

```go
	// buildSweepHours is the global config's build sweep threshold as the
	// model last read or wrote it (BuildSweep). buildSweepGen counts
	// ConfigureBuildSweep's writes: a sweep job made before one carries an
	// older generation, and its read is dropped. Loop-goroutine only.
	buildSweepHours int
	buildSweepGen   uint64
```

`core/build_sweep.go`:
- `errBuildSweepOff` becomes `errors.New("the build sweep is off: turn it on in Settings (S) → Build Sweep, or pass --after")`.
- `buildSweepResult` gains:

```go
	// configured is the global config's threshold as the job read it,
	// when read is true; gen is buildSweepGen when the job was made.
	configured int
	read       bool
	gen        uint64
```

- `maybeBuildSweep` and `SweepBuilds` pass `m.buildSweepGen` as the new last argument.
- `buildSweepJob` becomes:

```go
// buildSweepJob runs a sweep off the loop: it reads the global config's
// threshold strictly (globalBuildSweepHours), sweeps targets with
// opts.AfterHours or else that threshold, and carries the read back for
// the view. gen is the model's buildSweepGen when the job was made.
func buildSweepJob(s buildsweep.Sweeper, targets []buildsweep.Target, opts buildsweep.Options, gen uint64) Job {
	return func() any {
		r := buildSweepResult{gen: gen}
		configured, err := globalBuildSweepHours()
		r.configured, r.read = configured, err == nil
		hours := opts.AfterHours
		if hours == 0 {
			if err != nil {
				r.err = fmt.Errorf("build sweep: the global config can't be read: %w", err)
				return r
			}
			hours = configured
		}
		if hours <= 0 {
			r.err = errBuildSweepOff
			return r
		}
		r.report = s.Run(targets, time.Duration(hours)*time.Hour, opts.DryRun)
		return r
	}
}
```

- `deliverBuildSweep` starts with:

```go
	if r.read && r.gen == m.buildSweepGen {
		m.buildSweepHours = r.configured
	}
```

- new code:

```go
// BuildSweepView is the build sweep's setting as a client sees it
// (Core.BuildSweep): the global config's build_sweep_after_hours, 0 for
// off.
type BuildSweepView struct {
	AfterHours int
}

// BuildSweep is the build sweep's setting (Core.BuildSweep).
func (m *Model) BuildSweep() BuildSweepView { return BuildSweepView{AfterHours: m.buildSweepHours} }

// globalBuildSweepHours reads build_sweep_after_hours from the global
// config.json, strictly (config.ReadConfigFrom).
func globalBuildSweepHours() (int, error) {
	dir, err := config.GetConfigDir()
	if err != nil {
		return 0, err
	}
	cfg, err := config.ReadConfigFrom(dir)
	if err != nil {
		return 0, err
	}
	return cfg.GetBuildSweepAfterHours(), nil
}

// initBuildSweep reads the build sweep's threshold at boot. One that
// can't be read leaves it 0 until a sweep job or ConfigureBuildSweep
// reads it.
func (m *Model) initBuildSweep() {
	hours, err := globalBuildSweepHours()
	if err != nil {
		log.For("build_sweep").Warn("build_sweep.config_unreadable", "err", err)
		return
	}
	m.buildSweepHours = hours
}

// ConfigureBuildSweep sets the build sweep's threshold to afterHours (0
// turns the sweep off) in the global config.json, whichever workspace asks
// (Core.ConfigureBuildSweep). The file is read strictly and only that
// field changes. A served workspace whose settings are that file takes the
// value in memory too, so its next SaveSettings keeps it. When the value
// is out of range or the file can't be read or written, nothing changes.
func (m *Model) ConfigureBuildSweep(afterHours int) error {
	if err := config.ValidateBuildSweepAfterHours(afterHours); err != nil {
		return err
	}
	dir, err := config.GetConfigDir()
	if err != nil {
		return fmt.Errorf("configure build sweep: %w", err)
	}
	cfg, err := config.ReadConfigFrom(dir)
	if err != nil {
		return fmt.Errorf("configure build sweep: %w", err)
	}
	set := func(c *config.Config) { c.BuildSweepAfterHours = afterHours }
	cfg.Mutate(set)
	if err := config.SaveConfigTo(cfg, dir); err != nil {
		return fmt.Errorf("configure build sweep: %w", err)
	}
	for _, ws := range m.workspaces {
		if ws.cfg != nil && canonicalDir(settingsDir(ws)) == canonicalDir(dir) {
			ws.cfg.Mutate(set)
		}
	}
	m.buildSweepHours = afterHours
	m.buildSweepGen++
	return nil
}
```

`core/load.go`, in `Boot`, after `m.initWeb()`: `m.initBuildSweep()`.

`core/state_views.go`: `ModelView` gains `BuildSweep BuildSweepView` (and its doc comment names the build sweep's setting). `core/state_publish.go`: `modelView` sets `BuildSweep: m.BuildSweep()`.

`core/settings.go`: add

```go
// settingsDir is the dir SaveSettings writes ws's config.json to: its
// context's config dir, or the default config dir for a bare context; ""
// when neither resolves.
func settingsDir(ws *Workspace) string {
	if dir := ws.configDir(); dir != "" {
		return dir
	}
	if globalDir, err := config.GetConfigDir(); err == nil {
		return globalDir
	}
	return ""
}
```

and in `SaveSettings` replace the dir computation with `dir := settingsDir(ws)`, and before `ws.cfg.ReplaceSettings(s)`:

```go
	// The build sweep's threshold is ConfigureBuildSweep's to change: a
	// client's copy made before a change must not write the old one back.
	s.BuildSweepAfterHours = ws.cfg.Snapshot().BuildSweepAfterHours
```

`core/loop_core.go`:

```go
// BuildSweep runs Model.BuildSweep on the loop.
func (l *Loop) BuildSweep() BuildSweepView { return get(l, (*Model).BuildSweep) }

// ConfigureBuildSweep runs Model.ConfigureBuildSweep on the loop.
func (l *Loop) ConfigureBuildSweep(afterHours int) error {
	return get(l, func(m *Model) error { return m.ConfigureBuildSweep(afterHours) })
}
```

`core/rpc/replica.go`:

```go
// BuildSweep is the build sweep's setting, from the published model view.
func (r *replica) BuildSweep() core.BuildSweepView { return r.model.BuildSweep }
```

`core/events.go`: `ModelChanged`'s comment says it carries the build sweep's setting too, and that the TUI refreshes an open settings overlay from it.

Then regenerate: `go generate ./core/rpc` and `CGO_ENABLED=0 go test ./core/rpc -run TestProtocolReference -update`.

- [ ] **Step 4: Run them to see them pass**

Run: `CGO_ENABLED=0 go test ./core/... ./config/...`
Expected: `ok` for every package.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(core,rpc): ConfigureBuildSweep sets the global build sweep threshold; BuildSweep serves it" -- core/iface.go core/build_sweep.go core/model.go core/load.go core/state_views.go core/state_publish.go core/settings.go core/loop_core.go core/events.go core/rpc/replica.go core/rpc/methods_gen.go docs/specs/protocol.md core/build_sweep_test.go core/settings_test.go core/state_views_test.go core/rpc/client_test.go
```

### Task 3: ui/overlay — the Build Sweep row

**Files:**
- Modify: `ui/overlay/settingsOverlay.go`
- Test: `ui/overlay/settingsOverlay_test.go`

- [ ] **Step 1: Write the failing tests**

```go
func TestSettingsOverlay_BuildSweepRowShowsTheGlobalSetting(t *testing.T) {
	so := NewSettingsOverlay(newTestSettingsCfg(), false, "")
	assert.Contains(t, so.Render(), "off (all workspaces)")
	so.SetBuildSweep(12)
	assert.Contains(t, so.Render(), "12h (all workspaces)")
}

func TestSettingsOverlay_BuildSweepEditStartsFromTheCurrentValue(t *testing.T) {
	so := NewSettingsOverlay(newTestSettingsCfg(), false, "")
	so.SetBuildSweep(24)
	so.cursor = int(settingsFieldBuildSweep)
	so.HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Equal(t, "24", so.editing.GetValue())
}

// TestSettingsOverlay_BuildSweepEditIsARequestNotAWorkspaceSetting: the
// threshold is global, so the edit goes to the caller and the workspace's
// settings neither change nor save.
func TestSettingsOverlay_BuildSweepEditIsARequestNotAWorkspaceSetting(t *testing.T) {
	for in, want := range map[string]int{" 12 ": 12, "0": 0} {
		t.Run(in, func(t *testing.T) {
			cfg := newTestSettingsCfg()
			so := NewSettingsOverlay(cfg, false, "")
			so.SetBuildSweep(6)

			_, changed := submitText(t, so, settingsFieldBuildSweep, in)

			assert.False(t, changed, "the workspace's settings are not saved")
			assert.Zero(t, cfg.BuildSweepAfterHours, "nor changed")
			assert.NoError(t, so.TakeError())
			hours, ok := so.TakeBuildSweepRequest()
			assert.True(t, ok)
			assert.Equal(t, want, hours)
			_, ok = so.TakeBuildSweepRequest()
			assert.False(t, ok, "taken once")
		})
	}
}

func TestSettingsOverlay_BuildSweepRefusesABadValue(t *testing.T) {
	for _, bad := range []string{"abc", "-1", "8761", "1.5", ""} {
		t.Run(bad, func(t *testing.T) {
			so := NewSettingsOverlay(newTestSettingsCfg(), false, "")
			submitText(t, so, settingsFieldBuildSweep, bad)
			assert.Error(t, so.TakeError())
			_, ok := so.TakeBuildSweepRequest()
			assert.False(t, ok)
		})
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `CGO_ENABLED=0 go test ./ui/overlay -run BuildSweep`
Expected: build failure, `undefined: settingsFieldBuildSweep`.

- [ ] **Step 3: Implement**

- `settingsFieldBuildSweep` between `settingsFieldTheme` and `settingsFieldAccounts`; its label `"Build Sweep"`.
- `SettingsOverlay` fields:

```go
	// buildSweepHours is the build sweep threshold the Build Sweep row
	// shows (SetBuildSweep), and buildSweepReq a valid edit waiting for
	// the caller (TakeBuildSweepRequest). The threshold is global, so the
	// row never touches cfg.
	buildSweepHours int
	buildSweepReq   *int
```

- `activateRow`: `case settingsFieldBuildSweep: s.startTextEdit(settingsFieldBuildSweep, "Build Sweep: hours unused (0 = off, all workspaces)", strconv.Itoa(s.buildSweepHours))` (add `strconv`).
- `applyTextEdit`:

```go
	case settingsFieldBuildSweep:
		hours, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			s.lastErr = fmt.Errorf("the build sweep threshold must be a whole number of hours, not %q", value)
			return false
		}
		if err := config.ValidateBuildSweepAfterHours(hours); err != nil {
			s.lastErr = err
			return false
		}
		s.buildSweepReq = &hours
		return false
```

- `valueFor`:

```go
	case settingsFieldBuildSweep:
		if s.buildSweepHours <= 0 {
			return "off (all workspaces)"
		}
		return fmt.Sprintf("%dh (all workspaces)", s.buildSweepHours)
```

- methods:

```go
// SetBuildSweep supplies the threshold the Build Sweep row shows: the
// global setting, which the caller reads from the model.
func (s *SettingsOverlay) SetBuildSweep(hours int) { s.buildSweepHours = hours }

// TakeBuildSweepRequest returns and clears the threshold a Build Sweep
// edit asked for. Callers poll it after HandleKeyPress, like TakeError,
// and send it to the model (Core.ConfigureBuildSweep).
func (s *SettingsOverlay) TakeBuildSweepRequest() (int, bool) {
	if s.buildSweepReq == nil {
		return 0, false
	}
	hours := *s.buildSweepReq
	s.buildSweepReq = nil
	return hours, true
}
```

- [ ] **Step 4: Run them to see them pass**

Run: `CGO_ENABLED=0 go test ./ui/...`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(ui): a Build Sweep row in the settings overlay" -- ui/overlay/settingsOverlay.go ui/overlay/settingsOverlay_test.go
```

### Task 4: app — send the row's request and follow the view

**Files:**
- Modify: `app/intents.go` (`runOpenSettings`), `app/state_settings.go` (`handleStateSettingsKey`), `app/core_glue.go` (`applyCoreEvent`)
- Test: `app/state_settings_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// openSettingsForTest opens the settings overlay as runOpenSettings does,
// with the Build Sweep row's value, and resets the global threshold when
// the test ends.
func openSettingsForTest(t *testing.T, m *home) *overlay.SettingsOverlay {
	t.Helper()
	t.Cleanup(func() { _ = loopOf(m).ConfigureBuildSweep(0) })
	so := settingsOverlayForTest(m)
	so.SetBuildSweep(m.core.BuildSweep().AfterHours)
	m.setOverlay(so, overlaySettings)
	m.state = stateSettings
	return so
}

// editBuildSweep types value into the Build Sweep row.
func editBuildSweep(m *home, value string) {
	press := func(msg tea.KeyPressMsg) { handleStateSettingsKey(m, msg) }
	for i := 0; i < 20; i++ {
		press(tea.KeyPressMsg{Code: tea.KeyDown}) // the list clamps at its last row, Accounts
	}
	press(tea.KeyPressMsg{Code: tea.KeyUp}) // Build Sweep
	press(tea.KeyPressMsg{Code: tea.KeyEnter})
	for i := 0; i < 16; i++ {
		press(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	for _, r := range value {
		press(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	press(tea.KeyPressMsg{Code: tea.KeyEnter})
}

// TestSettingsBuildSweep_SetsTheGlobalThreshold: the row writes the global
// config.json, not the focused workspace's, and shows the new value.
func TestSettingsBuildSweep_SetsTheGlobalThreshold(t *testing.T) {
	m := newTestHomeWithWsCtx(t)
	so := openSettingsForTest(t, m)

	editBuildSweep(m, "12")

	assert.Equal(t, 12, m.core.BuildSweep().AfterHours)
	dir, err := config.GetConfigDir()
	require.NoError(t, err)
	cfg, err := config.ReadConfigFrom(dir)
	require.NoError(t, err)
	assert.Equal(t, 12, cfg.GetBuildSweepAfterHours(), "the global config.json")
	assert.Zero(t, m.settings().BuildSweepAfterHours, "not the workspace's settings")
	assert.Contains(t, so.Render(), "12h (all workspaces)")
}

func TestSettingsBuildSweep_ShowsARefusal(t *testing.T) {
	m := newTestHomeWithWsCtx(t)
	dir, err := config.GetConfigDir()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, config.ConfigFileName)
	prev, readErr := os.ReadFile(path)
	t.Cleanup(func() {
		if readErr != nil {
			_ = os.Remove(path)
		} else {
			_ = os.WriteFile(path, prev, 0o644)
		}
	})
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o644))
	openSettingsForTest(t, m)

	editBuildSweep(m, "12")

	assert.Contains(t, m.errBox.String(), "configure build sweep")
	assert.Zero(t, m.core.BuildSweep().AfterHours)
}

// TestSettingsBuildSweep_AChangeElsewhereRefreshesTheRow: another client's
// change reaches an open overlay with its ModelChanged.
func TestSettingsBuildSweep_AChangeElsewhereRefreshesTheRow(t *testing.T) {
	m := newTestHomeWithWsCtx(t)
	so := openSettingsForTest(t, m)

	require.NoError(t, loopOf(m).ConfigureBuildSweep(24))
	_ = m.drainCore()

	assert.Contains(t, so.Render(), "24h (all workspaces)")
}
```

(`os` and `path/filepath` join the test file's imports.)

- [ ] **Step 2: Run them to see them fail**

Run: `CGO_ENABLED=0 go test ./app -run SettingsBuildSweep`
Expected: FAIL: the threshold stays 0 (no request is sent) and the row doesn't refresh.

- [ ] **Step 3: Implement**

`app/intents.go`, `runOpenSettings`, after `so.SetAccountNotice(…)`:

```go
	so.SetBuildSweep(m.core.BuildSweep().AfterHours)
```

`app/state_settings.go`, `handleStateSettingsKey`, after the account request:

```go
	if hours, ok := so.TakeBuildSweepRequest(); ok {
		// The threshold is global: the model writes it to the global
		// config.json, whichever workspace this overlay edits.
		if err := m.core.ConfigureBuildSweep(hours); err != nil {
			cmds = append(cmds, m.handleError(err))
		}
		so.SetBuildSweep(m.core.BuildSweep().AfterHours)
	}
```

`app/core_glue.go`, `applyCoreEvent`:

```go
	case core.ModelChanged:
		if so := m.settingsOverlay(); so != nil {
			so.SetBuildSweep(m.core.BuildSweep().AfterHours)
		}
```

- [ ] **Step 4: Run them to see them pass**

Run: `CGO_ENABLED=0 go test ./app`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(app): the settings menu's Build Sweep row sets the global threshold" -- app/intents.go app/state_settings.go app/core_glue.go app/state_settings_test.go
```

### Task 5: docs and the full gate

**Files:**
- Modify: `USAGE.md` (the `S` row in the key table; the Build Sweep section's opening example)
- Modify: `config/CLAUDE.md` (a rule)
- Modify: `config/README.md`, `core/README.md` or `docs/ARCHITECTURE.md` where they list `Core`'s settings methods or the build sweep (grep `SweepBuilds` and `build_sweep_after_hours`)

- [ ] **Step 1: Docs**

`USAGE.md`: the key table's `S` row lists Build Sweep; the Build Sweep section says it is set in Settings (`S`) → Build Sweep, for all workspaces, or by hand in the global `config.json`.

`config/CLAUDE.md`, a rule:

```markdown
- **Let only `Core.ConfigureBuildSweep` change `build_sweep_after_hours`, and only in the global `config.json`.** The sweep reads the global file alone, so a value saved into a workspace's file does nothing; `SaveSettings` carries the field over from the workspace's own config, since a client's copy made before a change would write the old threshold back. Read a file you will write back with `ReadConfigFrom`, which refuses one it can't parse; `LoadConfigFrom` quarantines it and returns defaults, which the write would put in its place. **Enforced** by `TestSaveSettings_KeepsTheBuildSweepThreshold`, `TestConfigureBuildSweep_RefusesAnUnreadableGlobalConfig` and `TestSettingsOverlay_BuildSweepEditIsARequestNotAWorkspaceSetting`.
```

- [ ] **Step 2: The full gate**

Run, in order:
- `gofmt -l $(git ls-files '*.go' | grep -v '^vendor/')` → no output
- `CGO_ENABLED=0 go vet ./...` → no output
- `CGO_ENABLED=0 go test ./...` → every package `ok`
- `CC=clang CGO_ENABLED=1 go test -race ./core/... ./app/... ./ui/... ./config/...` → `ok`
- `go run ./tools/claudemd` → passes

- [ ] **Step 3: Commit**

```bash
git commit -m "docs: the settings menu's Build Sweep row" -- USAGE.md config/CLAUDE.md <the READMEs touched>
```
