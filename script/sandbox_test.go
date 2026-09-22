package script

import (
	"bytes"
	"github.com/aidan-bailey/loom/log"
	"io"
	"log/slog"
	"os"
	"testing"

	lua "github.com/yuin/gopher-lua"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOpenSandbox_StripsEscapeHatches pins the sandbox's allow-list
// contract (docs/specs/scripting.md §security): every global gopher-lua's
// base library could use to escape the sandbox — read arbitrary files,
// execute or serialize/deserialize arbitrary code, walk the calling
// environment, or reach a library we never open in the first place — must
// come back nil after openSandbox runs. Names that openSandbox doesn't
// explicitly nil (io, os, debug, package) are covered too: they read nil
// simply because we never open those libraries, but a future change that
// opens one for a legitimate reason must not silently leave it reachable
// without this test failing. _printregs is base's lower-level twin of
// print (see TestOpenSandbox_PrintRoutesToScriptLog for print itself) and
// gets the same nil treatment.
func TestOpenSandbox_StripsEscapeHatches(t *testing.T) {
	L := lua.NewState(lua.Options{SkipOpenLibs: true})
	defer L.Close()
	openSandbox(L, &Engine{})

	globals := []string{
		"dofile", "loadfile", "load", "loadstring", "require",
		"collectgarbage", "setfenv", "getfenv", "newproxy", "_printregs",
		"io", "os", "debug", "package",
	}
	for _, name := range globals {
		assert.Equal(t, lua.LNil, L.GetGlobal(name), "global %q must be nil after openSandbox", name)
	}

	strLib, ok := L.GetGlobal("string").(*lua.LTable)
	if assert.True(t, ok, "string library must be open") {
		assert.Equal(t, lua.LNil, strLib.RawGetString("dump"), "string.dump must be nil after openSandbox")
	}
}

// TestOpenSandbox_PrintRoutesToScriptLog pins the print replacement:
// gopher-lua's base print writes straight to process stdout via fmt.Print,
// which would corrupt the TUI's alt-screen the moment a user script called
// print(...). openSandbox must replace it with a function that (a) never
// touches the real os.Stdout and (b) forwards to the engine's script log —
// the same sink cs.log/ctx:log use (Engine.logScript), which writes
// straight to log.For("script") — at info level, joining arguments with
// tabs via tostring exactly as Lua's own print does. Checks both ends of
// that sink: the test-only bounded capture DrainLogs reads, and the real
// structured logger production actually reads from.
func TestOpenSandbox_PrintRoutesToScriptLog(t *testing.T) {
	var logBuf bytes.Buffer
	prevStructured := log.Structured
	log.Structured = slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	t.Cleanup(func() { log.Structured = prevStructured })

	L := lua.NewState(lua.Options{SkipOpenLibs: true})
	defer L.Close()
	e := &Engine{}
	openSandbox(L, e)

	r, w, err := os.Pipe()
	require.NoError(t, err)
	origStdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = origStdout }()
	doErr := L.DoString(`print("a", "b", 3)`)
	require.NoError(t, w.Close())
	require.NoError(t, doErr)

	var captured bytes.Buffer
	_, err = io.Copy(&captured, r)
	require.NoError(t, err)
	assert.Empty(t, captured.String(), "print must not write to process stdout")

	logs := e.DrainLogs()
	if assert.Len(t, logs, 1, "print must emit exactly one script log entry") {
		assert.Equal(t, "info", logs[0].Level)
		assert.Equal(t, "a\tb\t3", logs[0].Message, "print must tab-join tostring'd args, like Lua's own print")
	}

	out := logBuf.String()
	assert.Contains(t, out, "subsystem=script", "print must reach the structured logger, tagged with the script subsystem")
	assert.Contains(t, out, `level=INFO msg="a\tb\t3"`, "print must reach the structured logger at info level with the tab-joined message")
}
