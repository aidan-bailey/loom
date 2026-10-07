package script

import (
	"context"
	"testing"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	lua "github.com/yuin/gopher-lua"
)

// lifecycleScript calls each lifecycle method on the selected instance,
// one key each, and notifies what the call returned once it returns. The
// call sits on line 3 of each handler's chunk, so a raise names it.
const lifecycleScript = `cs.bind("kill", function(ctx)
  local inst = ctx:selected()
  local r = inst:kill()
  ctx:notify("returned " .. tostring(r))
end)
cs.bind("pause", function(ctx)
  local inst = ctx:selected()
  local r = inst:pause()
  ctx:notify("returned " .. tostring(r))
end)
cs.bind("resume", function(ctx)
  local inst = ctx:selected()
  local r = inst:resume()
  ctx:notify("returned " .. tostring(r))
end)
cs.bind("send_prompt", function(ctx)
  local inst = ctx:selected()
  local r = inst:send_prompt("fix it")
  ctx:notify("returned " .. tostring(r))
end)
`

// lifecycleLine is the line each handler's lifecycle call sits on.
var lifecycleLine = map[string]string{"kill": ":3:", "pause": ":8:", "resume": ":13:", "send_prompt": ":18:"}

// TestLifecycleMethods_YieldUntilTheHostResumes: inst:kill(), :pause(),
// :resume() and :send_prompt() used to act on the instance from the
// script's goroutine. Each enqueues an InstanceOpIntent naming the
// instance by ID and yields; resumed with nothing it returns nothing, and
// resumed with an error message it raises "<op>: …" at the script's line,
// as it raised before.
func TestLifecycleMethods_YieldUntilTheHostResumes(t *testing.T) {
	for _, op := range lifecycleMethods {
		t.Run(op, func(t *testing.T) {
			e := NewEngine(nil)
			defer e.Close()
			require.NoError(t, e.LoadFromString("lc.lua", lifecycleScript))
			h := &fakeHost{selected: &core.InstanceView{ID: 3, Title: "x"}}
			text := ""
			if op == "send_prompt" {
				text = "fix it"
			}

			_, err := e.Dispatch(context.Background(), op, h)
			require.NoError(t, err)
			require.Len(t, h.enqueued, 1, "the call yields on an intent")
			assert.Equal(t, InstanceOpIntent{ID: 3, Title: "x", Op: op, Text: text}, h.enqueued[0])
			assert.Empty(t, h.notices, "nothing after the call ran yet")
			require.NoError(t, e.ResumeWithHost(context.Background(), h.enqueuedIDs[0], h, ResumeValue{}))
			assert.Equal(t, []string{"returned nil"}, h.notices, "resumed with nothing, the call returns nothing")

			_, err = e.Dispatch(context.Background(), op, h)
			require.NoError(t, err)
			require.Len(t, h.enqueuedIDs, 2)
			err = e.ResumeWithHost(context.Background(), h.enqueuedIDs[1], h, ResumeValue{Err: op + ": boom"})
			require.Error(t, err)
			assert.Contains(t, err.Error(), op+": boom")
			assert.Contains(t, err.Error(), lifecycleLine[op], "raised at the script's line, not the wrapper's")
			assert.Len(t, h.notices, 1, "the raise ends the handler")
			assert.Empty(t, e.coroutines, "and its coroutine")
		})
	}
}

// TestLifecycleMethods_BadArgumentsRaiseAtTheCall: an error a yielding
// method finds before it yields (a bad argument) raises at the script's
// line under the method's name, as L.ArgError did when the methods were
// plain Go functions, and enqueues nothing.
func TestLifecycleMethods_BadArgumentsRaiseAtTheCall(t *testing.T) {
	for _, tc := range []struct {
		name, src, want string
	}{
		{"not an instance", "cs.bind(\"Z\", function(ctx)\n  ctx:selected().kill(42)\nend)", "bad argument #1 to kill (userdata expected, got number)"},
		{"the ctx", "cs.bind(\"Z\", function(ctx)\n  ctx:selected().kill(ctx)\nend)", "bad argument #1 to kill (instance expected)"},
		{"no text", "cs.bind(\"Z\", function(ctx)\n  ctx:selected():send_prompt()\nend)", "bad argument #2 to send_prompt (string expected, got nil)"},
		{"no title", "cs.bind(\"Z\", function(ctx)\n  ctx:new_instance{}\nend)", "bad argument #2 to new_instance (new_instance: title is required)"},
		{"no table", "cs.bind(\"Z\", function(ctx)\n  ctx:new_instance()\nend)", "bad argument #2 to new_instance (table expected, got nil)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEngine(nil)
			defer e.Close()
			require.NoError(t, e.LoadFromString("bad.lua", tc.src))
			h := &fakeHost{selected: &core.InstanceView{ID: 3, Title: "x"}}

			_, err := e.Dispatch(context.Background(), "Z", h)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.Contains(t, err.Error(), ":2: ", "raised at the script's line")
			assert.Empty(t, h.enqueued)
		})
	}
}

// TestLifecycleMethods_WithoutAHostRaise: a lifecycle call outside a
// dispatch (no host to run it) raises "<op>: no host context", as
// send_terminal_keys does, and so does ctx:new_instance.
func TestLifecycleMethods_WithoutAHostRaise(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	L := e.L
	idx := L.GetField(L.GetTypeMetatable(instanceTypeName), "__index").(*lua.LTable)
	for _, op := range lifecycleMethods {
		L.Push(idx.RawGetString(op))
		L.Push(pushInstance(L, &core.InstanceView{ID: 1, Title: "x"}))
		L.Push(lua.LString("text"))
		err := L.PCall(2, 0, nil)
		require.Error(t, err, op)
		assert.Contains(t, err.Error(), op+": no host context")
	}

	ctxIdx := L.GetField(L.GetTypeMetatable(ctxTypeName), "__index").(*lua.LTable)
	ctx, _ := pushCtx(L, e, &fakeHost{})
	opts := L.NewTable()
	opts.RawSetString("title", lua.LString("made"))
	L.Push(ctxIdx.RawGetString("new_instance"))
	L.Push(ctx)
	L.Push(opts)
	err := L.PCall(2, 0, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "new_instance: no host context")
}

// TestCtxNewInstance_RaisesTheHostsError: ctx:new_instance resumed with an
// error message raises it at the script's line ("new_instance: …", the
// host's prefix); resumed with an instance, it returns it.
func TestCtxNewInstance_RaisesTheHostsError(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	require.NoError(t, e.LoadFromString("new.lua", "cs.bind(\"Z\", function(ctx)\n  local inst = ctx:new_instance{title = \"made\"}\n  ctx:notify(tostring(inst))\nend)"))
	h := &fakeHost{}

	_, err := e.Dispatch(context.Background(), "Z", h)
	require.NoError(t, err)
	require.Len(t, h.enqueuedIDs, 1)
	err = e.ResumeWithHost(context.Background(), h.enqueuedIDs[0], h, ResumeValue{Err: "new_instance: workspace changed while a script ran; not creating made here"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), ":2: new_instance: workspace changed while a script ran; not creating made here")
	assert.Empty(t, h.notices)

	_, err = e.Dispatch(context.Background(), "Z", h)
	require.NoError(t, err)
	require.Len(t, h.enqueuedIDs, 2)
	require.NoError(t, e.ResumeWithHost(context.Background(), h.enqueuedIDs[1], h, ResumeValue{Instance: &core.InstanceView{ID: 9, Title: "made", Status: session.Ready}}))
	assert.Equal(t, []string{"instance(made, Ready)"}, h.notices, "the call returns the created instance")
}

// TestLifecycleWrapper_SurvivesAScriptReassigningItsGlobals: the wrapper
// keeps its own type and error, so a script that reassigns either global
// still gets its call's raise.
func TestLifecycleWrapper_SurvivesAScriptReassigningItsGlobals(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	require.NoError(t, e.LoadFromString("g.lua", `
type = function() return "nope" end
error = function() end
cs.bind("Z", function(ctx) ctx:selected():kill() end)
`))
	h := &fakeHost{selected: &core.InstanceView{ID: 3, Title: "x"}}
	_, err := e.Dispatch(context.Background(), "Z", h)
	require.NoError(t, err)
	require.Len(t, h.enqueuedIDs, 1)

	err = e.ResumeWithHost(context.Background(), h.enqueuedIDs[0], h, ResumeValue{Err: "kill: boom"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "kill: boom")
}

// TestInstanceUserdata_ReadsTheView: the read methods report the view the
// host snapshot (diff_stats nil until there is one), and the pane methods
// keep AgentPane's guards: an unstarted or paused session has no preview,
// takes no keys and no Enter, without touching tmux.
func TestInstanceUserdata_ReadsTheView(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	require.NoError(t, e.LoadFromString("read.lua", `
cs.bind("Z", function(ctx)
  for _, inst in ipairs(ctx:instances()) do
    local d = inst:diff_stats()
    ctx:notify(table.concat({inst:title(), inst:status(), inst:branch(), inst:path(), inst:program(),
      tostring(inst:started()), tostring(inst:paused()), d and (d.added .. "/" .. d.removed) or "nodiff",
      inst:preview(), tostring(inst:worktree())}, "|"))
    inst:tap_enter()
    local ok, err = pcall(inst.send_keys, inst, "x")
    ctx:notify(tostring(ok) .. " " .. tostring(err))
  end
end)
`))
	unstarted := core.InstanceView{ID: 1, Title: "new", Status: session.Ready, Branch: "b", RepoPath: "/repo", Program: "claude"}
	paused := core.InstanceView{ID: 2, Title: "zz", Status: session.Paused, Started: true, TmuxSession: "loom_zz", RepoPath: "/repo", Program: "aider", HasDiff: true}
	paused.Diff.Added, paused.Diff.Removed = 4, 2
	h := &fakeHost{instances: []core.InstanceView{unstarted, paused}}

	_, err := e.Dispatch(context.Background(), "Z", h)

	require.NoError(t, err)
	require.Len(t, h.notices, 4)
	assert.Equal(t, "new|Ready|b|/repo|claude|false|false|nodiff||nil", h.notices[0])
	assert.Contains(t, h.notices[1], "cannot send keys to instance that has not been started or is paused")
	assert.Equal(t, "zz|Paused||/repo|aider|true|true|4/2||nil", h.notices[2], "a paused session's worktree reads nil without a path")
	assert.Contains(t, h.notices[3], "send_keys: cannot send keys to instance that has not been started or is paused")
}

// TestInstanceWorktree_BuiltFromTheView: worktree() rebuilds the handle
// from the view: its repository is the one git resolved for the worktree
// (WorktreeRepoPath), which RepoPath may spell differently. A workspace
// terminal has none.
func TestInstanceWorktree_BuiltFromTheView(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	require.NoError(t, e.LoadFromString("wt.lua", `
cs.bind("Z", function(ctx)
  for _, inst in ipairs(ctx:instances()) do
    local wt = inst:worktree()
    if wt then
      ctx:notify(wt:branch_name() .. "|" .. wt:path() .. "|" .. wt:repo_path())
    else
      ctx:notify("none")
    end
  end
end)
`))
	wt := core.InstanceView{ID: 1, Title: "a", Started: true, Branch: "u/a", RepoPath: "/link/repo", WorktreeRepoPath: "/real/repo", WorktreePath: "/wt/a"}
	term := core.InstanceView{ID: 2, Title: "term", Started: true, IsWorkspaceTerminal: true, RepoPath: "/real/repo", WorktreePath: "/real/repo"}
	h := &fakeHost{instances: []core.InstanceView{wt, term}}

	_, err := e.Dispatch(context.Background(), "Z", h)

	require.NoError(t, err)
	assert.Equal(t, []string{"u/a|/wt/a|/real/repo", "none"}, h.notices)
}
