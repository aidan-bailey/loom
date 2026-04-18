package script

import (
	"claude-squad/log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain initializes the package-level loggers before tests run.
// The engine logs collision / parse errors through log.WarningLog,
// which is nil until log.Initialize populates it. Mirrors the
// pattern used in config_test.go.
func TestMain(m *testing.M) {
	log.Initialize("", false)
	exit := m.Run()
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
	matched, err := e.Dispatch("ctrl+h", h)
	require.NoError(t, err)
	assert.True(t, matched)
	assert.Equal(t, []string{"hi from script"}, h.notices)
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
	_, err := e.Dispatch("ctrl+y", h)
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

	matched, err := e.Dispatch("ctrl+z", &fakeHost{})
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

// TestEngineFirstScriptWinsForDuplicateKey verifies the first-wins
// policy for two scripts that both try to bind the same key.
func TestEngineFirstScriptWinsForDuplicateKey(t *testing.T) {
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
	matched, err := e.Dispatch("ctrl+x", h)
	require.NoError(t, err)
	assert.True(t, matched)
	assert.Equal(t, []string{"first"}, h.notices)

	regs := e.Registrations()
	require.Len(t, regs, 1)
	assert.Equal(t, "first", regs[0].Help)
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

	_, dispatchErr := e.Dispatch("ctrl+r", &fakeHost{})
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
	matched, err := e.Dispatch("ctrl+g", h)
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

	matched, runErr := e.Dispatch("ctrl+b", &fakeHost{})
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
	_, err = e.Dispatch("ctrl+o", h)
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

// TestEngineLogBuffer covers ctx:log() and DrainLogs(). The buffer
// must survive multiple dispatches and drain cleanly on each read.
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

	_, err := e.Dispatch("ctrl+l", &fakeHost{})
	require.NoError(t, err)
	logs := e.DrainLogs()
	require.Len(t, logs, 2)
	assert.Equal(t, "info", logs[0].Level)
	assert.Equal(t, "first", logs[0].Message)

	// Second drain empty.
	assert.Nil(t, e.DrainLogs())
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
// silent. Users who never create ~/.claude-squad/scripts must not
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
