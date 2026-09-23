package script

import (
	"bytes"
	"context"
	"github.com/aidan-bailey/loom/internal/testenv"
	"github.com/aidan-bailey/loom/log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain initializes the package-level loggers before tests run.
// The engine logs collision / parse errors through log.For("script"),
// which returns a no-op until log.Initialize populates log.Structured.
// Mirrors the pattern used in config_test.go. The loom config dirs point
// at throwaway directories so nothing a script test resolves (a new
// instance's config dir, the workspace registry) reaches the developer's
// ~/.loom.
func TestMain(m *testing.M) {
	_ = log.Initialize("", false)
	cleanup := testenv.MustIsolateLoomDirs()
	exit := m.Run()
	cleanup()
	log.Close()
	os.Exit(exit)
}

// TestEngineRegistersAndDispatches covers the happy path: a script
// declares one action, the engine dispatches on the matching key,
// and the run body runs with a valid ctx.
func TestEngineRegistersAndDispatches(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()

	err := e.LoadFromString("hello.lua", `
		local called = 0
		cs.register_action{
			key = "ctrl+h",
			help = "say hi",
			run = function(ctx)
				ctx:notify("hi from script")
				_G.__called = (_G.__called or 0) + 1
			end,
		}
	`)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"ctrl+h"}, e.actionKeys())

	h := &fakeHost{}
	matched, err := e.Dispatch(context.Background(), "ctrl+h", h)
	require.NoError(t, err)
	assert.True(t, matched)
	assert.Equal(t, []string{"hi from script"}, h.notices)
}

// TestCtxNewInstancePassesPromptThroughConstructor pins ctx:new_instance's
// options reaching the queued instance: prompt and program go through
// session.InstanceOptions, and instance:program() reads the same value
// back through the locked getter.
func TestCtxNewInstancePassesPromptThroughConstructor(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	require.NoError(t, e.LoadFromString("new.lua", `
		cs.register_action{
			key = "ctrl+n",
			run = function(ctx)
				local inst = ctx:new_instance{title = "scripted", program = "aider", prompt = "fix the build"}
				ctx:notify(inst:program())
				ctx:new_instance{title = "no-prompt"}
			end,
		}
	`))

	h := &fakeHost{repoPath: t.TempDir(), defaultProgram: "claude"}
	matched, err := e.Dispatch(context.Background(), "ctrl+n", h)
	require.NoError(t, err)
	require.True(t, matched)
	require.Len(t, h.queuedInstances, 2)

	assert.Equal(t, "fix the build", h.queuedInstances[0].Prompt())
	assert.Equal(t, "aider", h.queuedInstances[0].Program())
	assert.Equal(t, []string{"aider"}, h.notices, "instance:program() reads the constructor's program")
	assert.Empty(t, h.queuedInstances[1].Prompt(), "prompt is optional")
	assert.Equal(t, "claude", h.queuedInstances[1].Program(), "program defaults to the host's")
}

// TestEngineNotifyStandaloneRoutesToHost confirms cs.notify (the
// no-ctx form) reaches the live Host during a dispatch, matching
// ctx:notify's behavior. Before curHost was threaded through Engine,
// this path silently downgraded to a log entry.
func TestEngineNotifyStandaloneRoutesToHost(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()

	require.NoError(t, e.LoadFromString("bare.lua", `
		cs.register_action{
			key = "ctrl+y",
			run = function(ctx) cs.notify("bare-notify") end,
		}
	`))

	h := &fakeHost{}
	_, err := e.Dispatch(context.Background(), "ctrl+y", h)
	require.NoError(t, err)
	assert.Equal(t, []string{"bare-notify"}, h.notices)
}

// TestEngineNotifyAtLoadTimeFallsBackToLog confirms cs.notify called
// from a top-level load (no active dispatch, so e.curHost is nil)
// downgrades to a buffered log entry rather than crashing. Locks in
// the documented fallback so a future refactor doesn't silently
// swallow these messages.
func TestEngineNotifyAtLoadTimeFallsBackToLog(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()

	require.NoError(t, e.LoadFromString("topnotify.lua", `cs.notify("load-time-ping")`))

	logs := e.DrainLogs()
	require.Len(t, logs, 1)
	assert.Equal(t, "info", logs[0].Level)
	assert.Contains(t, logs[0].Message, "load-time-ping")
}

// TestEngineDispatchReturnsFalseForUnboundKey verifies the raw-key
// fall-through contract: keys no script claims return matched=false
// so the app layer can move on.
func TestEngineDispatchReturnsFalseForUnboundKey(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()

	matched, err := e.Dispatch(context.Background(), "ctrl+z", &fakeHost{})
	require.NoError(t, err)
	assert.False(t, matched)
}

// TestEngineRejectsReservedKey verifies the built-in collision rule:
// scripts cannot override built-in bindings, even if they try.
func TestEngineRejectsReservedKey(t *testing.T) {
	reserved := map[string]bool{"n": true}
	e := NewEngine(reserved)
	defer e.Close()

	err := e.LoadFromString("shadow.lua", `
		cs.register_action{
			key = "n",
			run = function(ctx) end,
		}
	`)
	require.NoError(t, err) // load succeeds; registration is silently skipped
	assert.Empty(t, e.actionKeys(), "reserved key must not be bound")
}

// TestEngineLaterBindOverwritesForDuplicateKey verifies the new
// last-wins policy: a second cs.register_action (or cs.bind) on the
// same key replaces the earlier binding. Scripts compose the stock
// keymap by cs.unbind + cs.bind, so overrides are first-class.
func TestEngineLaterBindOverwritesForDuplicateKey(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()

	err := e.LoadFromString("first.lua", `
		cs.register_action{
			key = "ctrl+x",
			help = "first",
			run = function(ctx) ctx:notify("first") end,
		}
	`)
	require.NoError(t, err)

	err = e.LoadFromString("second.lua", `
		cs.register_action{
			key = "ctrl+x",
			help = "second",
			run = function(ctx) ctx:notify("second") end,
		}
	`)
	require.NoError(t, err)

	h := &fakeHost{}
	matched, err := e.Dispatch(context.Background(), "ctrl+x", h)
	require.NoError(t, err)
	assert.True(t, matched)
	assert.Equal(t, []string{"second"}, h.notices)

	regs := e.Registrations()
	require.Len(t, regs, 1)
	assert.Equal(t, "second", regs[0].Help)
}

// TestEngineRejectsRuntimeRegister ensures cs.register_action only
// works at load time. A script that tries to register from inside a
// dispatched action must get a Lua error.
func TestEngineRejectsRuntimeRegister(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()

	err := e.LoadFromString("self.lua", `
		cs.register_action{
			key = "ctrl+r",
			run = function(ctx)
				cs.register_action{key = "ctrl+q", run = function() end}
			end,
		}
	`)
	require.NoError(t, err)

	_, dispatchErr := e.Dispatch(context.Background(), "ctrl+r", &fakeHost{})
	require.Error(t, dispatchErr)
	assert.Contains(t, dispatchErr.Error(), "load time")
}

// TestEnginePrecondition exercises the precondition→run flow and
// confirms a falsy precondition skips the body without error.
func TestEnginePrecondition(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()

	err := e.LoadFromString("gated.lua", `
		cs.register_action{
			key = "ctrl+g",
			precondition = function(ctx) return false end,
			run = function(ctx) ctx:notify("should not fire") end,
		}
	`)
	require.NoError(t, err)

	h := &fakeHost{}
	matched, err := e.Dispatch(context.Background(), "ctrl+g", h)
	require.NoError(t, err)
	assert.True(t, matched)
	assert.Empty(t, h.notices, "run body skipped by precondition")
}

// TestEngineRuntimeLuaError confirms script runtime errors surface
// back to the caller without breaking the engine.
func TestEngineRuntimeLuaError(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()

	err := e.LoadFromString("broken.lua", `
		cs.register_action{
			key = "ctrl+b",
			run = function(ctx) error("boom") end,
		}
	`)
	require.NoError(t, err)

	matched, runErr := e.Dispatch(context.Background(), "ctrl+b", &fakeHost{})
	assert.True(t, matched)
	require.Error(t, runErr)
	assert.Contains(t, runErr.Error(), "boom")

	// Engine recovers: a subsequent valid dispatch still works.
	err = e.LoadFromString("ok.lua", `
		cs.register_action{
			key = "ctrl+o",
			run = function(ctx) ctx:notify("ok") end,
		}
	`)
	require.NoError(t, err)
	h := &fakeHost{}
	_, err = e.Dispatch(context.Background(), "ctrl+o", h)
	require.NoError(t, err)
	assert.Equal(t, []string{"ok"}, h.notices)
}

// TestEngineRegistrationsOrdered verifies Registrations returns the
// actions in the order they were bound, making the help panel
// deterministic across TUI launches with the same script set.
func TestEngineRegistrationsOrdered(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()

	require.NoError(t, e.LoadFromString("a.lua", `cs.register_action{key="ctrl+a", run=function() end}`))
	require.NoError(t, e.LoadFromString("b.lua", `cs.register_action{key="ctrl+b", run=function() end}`))
	require.NoError(t, e.LoadFromString("c.lua", `cs.register_action{key="ctrl+c", run=function() end}`))

	regs := e.Registrations()
	require.Len(t, regs, 3)
	assert.Equal(t, "ctrl+a", regs[0].Key)
	assert.Equal(t, "ctrl+b", regs[1].Key)
	assert.Equal(t, "ctrl+c", regs[2].Key)
}

// TestEngineLogBuffer covers ctx:log() and DrainLogs(). DrainLogs backs
// only a small bounded test capture now — logScript's real destination
// is the structured logger, exercised separately by
// TestLogScript_ReachesStructuredLogger — but the capture must still
// survive multiple dispatches and drain cleanly on each read, since
// tests rely on it to assert what a script logged.
func TestEngineLogBuffer(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()

	require.NoError(t, e.LoadFromString("log.lua", `
		cs.register_action{
			key = "ctrl+l",
			run = function(ctx)
				ctx:log("info", "first")
				ctx:log("warn", "second")
			end,
		}
	`))

	_, err := e.Dispatch(context.Background(), "ctrl+l", &fakeHost{})
	require.NoError(t, err)
	logs := e.DrainLogs()
	require.Len(t, logs, 2)
	assert.Equal(t, "info", logs[0].Level)
	assert.Equal(t, "first", logs[0].Message)

	// Second drain empty.
	assert.Nil(t, e.DrainLogs())
}

// TestLogScript_ReachesStructuredLogger pins the actual production sink
// (nothing calls DrainLogs outside tests): cs.log/ctx:log must reach
// log.For("script") — i.e. log.Structured — synchronously, tagged with
// the source file when Load knows one, and an unrecognized level must
// fall back to info rather than being dropped or panicking.
func TestLogScript_ReachesStructuredLogger(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Structured
	log.Structured = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	t.Cleanup(func() { log.Structured = prev })

	e := NewEngine(nil)
	defer e.Close()

	require.NoError(t, e.LoadFromString("logtest.lua", `
		cs.log("warn", "from cs.log")
		cs.log("bogus-level", "from unknown level")
	`))

	out := buf.String()
	assert.Contains(t, out, "subsystem=script", "must be tagged with the script subsystem")
	assert.Contains(t, out, "from cs.log", "message must reach the structured logger")
	assert.Contains(t, out, "file=logtest.lua", "must be tagged with the source file Load knows")
	assert.Contains(t, out, `level=WARN`, "warn must map to the WARN level")
	assert.Contains(t, out, "from unknown level", "an unrecognized level must still be logged")
	assert.Contains(t, out, `level=INFO msg="from unknown level"`, "an unrecognized level must fall back to info")
}

// logLineContaining returns the first line of out that contains msg, or
// "" when none does.
func logLineContaining(out, msg string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, msg) {
			return line
		}
	}
	return ""
}

// TestLogScript_RuntimeLinesCarryTheActionFile: curFile names only the
// file being compiled, so a line a handler logs while it runs, at
// dispatch or after a resume, must take its file from the action that
// is running instead.
func TestLogScript_RuntimeLinesCarryTheActionFile(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Structured
	log.Structured = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	t.Cleanup(func() { log.Structured = prev })

	e := NewEngine(nil)
	defer e.Close()
	require.NoError(t, e.LoadFromString("runtime.lua", `
		cs.bind("x", function(ctx)
			cs.log("info", "logged at dispatch")
			cs.actions.show_help()
			ctx:log("info", "logged after resume")
		end)
	`))

	h := &fakeHost{}
	_, err := e.Dispatch(context.Background(), "x", h)
	require.NoError(t, err)
	require.Len(t, h.enqueuedIDs, 1)
	require.NoError(t, e.ResumeWithHost(context.Background(), h.enqueuedIDs[0], &fakeHost{}))

	out := buf.String()
	assert.Contains(t, logLineContaining(out, "logged at dispatch"), "file=runtime.lua")
	assert.Contains(t, logLineContaining(out, "logged after resume"), "file=runtime.lua")

	buf.Reset()
	require.NoError(t, e.L.DoString(`cs.log("info", "logged outside any action")`))
	assert.NotContains(t, logLineContaining(buf.String(), "logged outside any action"), "file=",
		"the action file must not outlive its dispatch")
}

// TestLoaderWalksDirectory exercises Load() against a real directory
// with a mix of valid, broken, and non-lua files. Broken files must
// not prevent valid ones from loading.
func TestLoaderWalksDirectory(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0644))
	}
	write("ok.lua", `cs.register_action{key="ctrl+1", run=function(ctx) ctx:notify("ok") end}`)
	write("broken.lua", `cs.register_action{` /* deliberate syntax error */)
	write("ignored.txt", "not a script")

	e := NewEngine(nil)
	defer e.Close()
	e.Load(dir)

	keys := e.actionKeys()
	assert.Contains(t, keys, "ctrl+1", "valid script loaded despite broken sibling")
	assert.NotContains(t, strings.Join(keys, ","), "broken", "broken script must not bleed state")
}

// TestLoaderMissingDirectoryIsNoop confirms the "no scripts" path is
// silent. Users who never create ~/.loom/scripts must not
// see warnings.
func TestLoaderMissingDirectoryIsNoop(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	e.Load("/nonexistent/definitely/not/a/path")
	assert.Empty(t, e.actionKeys())
}

// TestSandboxBlocksEscapeHatches verifies that scripts cannot call
// the ways-out that would break the sandbox: file I/O, package
// access, bytecode loading. A failure here is a security regression.
func TestSandboxBlocksEscapeHatches(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()

	cases := []struct {
		name string
		src  string
	}{
		{"io is absent", `return io.open("/etc/passwd", "r")`},
		{"os is absent", `return os.execute("echo pwned")`},
		{"debug is absent", `return debug.getinfo(1)`},
		{"package is absent", `return package.path`},
		{"require is nil", `require("os")`},
		{"dofile is nil", `dofile("/etc/passwd")`},
		{"loadfile is nil", `return loadfile("/etc/passwd")`},
		{"load is nil", `return load("return 1")`},
		{"loadstring is nil", `return loadstring("return 1")`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := e.LoadFromString("probe.lua", tc.src)
			require.Error(t, err, "sandbox leak: %s succeeded", tc.name)
		})
	}
}

// TestSandboxAllowsSafeLibs verifies the positive side of the
// allow-list: scripts can still use string, table, math, coroutine
// normally.
func TestSandboxAllowsSafeLibs(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()

	err := e.LoadFromString("safe.lua", `
		local s = string.upper("abc")
		local t = table.concat({"a", "b"}, "-")
		local n = math.floor(3.7)
		local co = coroutine.create(function() end)
		assert(s == "ABC")
		assert(t == "a-b")
		assert(n == 3)
		assert(co ~= nil)
	`)
	require.NoError(t, err)
}

// blockingHost parks the handler inside ConfigDir until release closes,
// standing in for slow Go-side work (inst:pause(), send_terminal_keys'
// probe) that runs while Dispatch holds e.mu.
type blockingHost struct {
	*fakeHost
	entered chan struct{}
	release chan struct{}
}

func (b *blockingHost) ConfigDir() string {
	close(b.entered)
	<-b.release
	return ""
}

// TestBindingQueriesDoNotWaitForRunningHandler pins that HasAction and
// Registrations answer while a handler is mid-run. The app calls both on
// the Update goroutine (every keypress, the help screen), so waiting for
// e.mu there would freeze the UI for as long as the handler runs.
func TestBindingQueriesDoNotWaitForRunningHandler(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	require.NoError(t, e.LoadFromString("slow.lua",
		`cs.bind("x", function(ctx) ctx:config_dir() end, {help = "slow"})`))

	h := &blockingHost{fakeHost: &fakeHost{}, entered: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(h.release) }) }
	defer release()

	done := make(chan error, 1)
	go func() {
		_, err := e.Dispatch(context.Background(), "x", h)
		done <- err
	}()
	<-h.entered

	within := func(name string, f func()) {
		t.Helper()
		ch := make(chan struct{})
		go func() {
			f()
			close(ch)
		}()
		select {
		case <-ch:
		case <-time.After(time.Second):
			release()
			t.Fatalf("%s blocked while a handler was running", name)
		}
	}
	var has bool
	var regs []Registration
	within("HasAction", func() { has = e.HasAction("x") })
	within("Registrations", func() { regs = e.Registrations() })
	assert.True(t, has)
	assert.Equal(t, []Registration{{Key: "x", Help: "slow"}}, regs)

	release()
	require.NoError(t, <-done)
}

// TestBindingSnapshotTracksMutations covers every path that changes the
// action table: the embedded defaults, a user script's cs.unbind of a
// default plus cs.bind / cs.register_action, and a runtime cs.unbind
// from inside a handler.
func TestBindingSnapshotTracksMutations(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	e.LoadDefaults()
	require.True(t, e.HasAction("q"))
	require.True(t, hasRegistration(e, "q"))

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "user.lua"), []byte(`
		cs.unbind("q")
		cs.bind("X", function() cs.unbind("Y") end, {help = "x"})
		cs.register_action{key = "Y", help = "y", run = function() end}
	`), 0o644))
	e.Load(dir)

	assert.False(t, e.HasAction("q"), "a user unbind of a default must reach HasAction")
	assert.False(t, hasRegistration(e, "q"), "a user unbind of a default must reach Registrations")
	assert.True(t, e.HasAction("X"))
	assert.True(t, e.HasAction("Y"))
	regs := e.Registrations()
	assert.Equal(t, Registration{Key: "X", Help: "x"}, regs[len(regs)-2])
	assert.Equal(t, Registration{Key: "Y", Help: "y"}, regs[len(regs)-1])

	_, err := e.Dispatch(context.Background(), "X", &fakeHost{})
	require.NoError(t, err)
	assert.False(t, e.HasAction("Y"), "a runtime cs.unbind must reach HasAction")
	assert.False(t, hasRegistration(e, "Y"))
}

func hasRegistration(e *Engine, key string) bool {
	for _, r := range e.Registrations() {
		if r.Key == key {
			return true
		}
	}
	return false
}
