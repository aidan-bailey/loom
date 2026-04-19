package script

import (
	"github.com/aidan-bailey/loom/session/git"

	lua "github.com/yuin/gopher-lua"
)

const worktreeTypeName = "cs.worktree"

func registerWorktreeType(L *lua.LState) {
	mt := L.NewTypeMetatable(worktreeTypeName)
	L.SetField(mt, "__index", L.SetFuncs(L.NewTable(), worktreeMethods))
}

func pushWorktree(L *lua.LState, gw *git.GitWorktree) lua.LValue {
	if gw == nil {
		return lua.LNil
	}
	ud := L.NewUserData()
	ud.Value = gw
	L.SetMetatable(ud, L.GetTypeMetatable(worktreeTypeName))
	return ud
}

func checkWorktree(L *lua.LState, n int) *git.GitWorktree {
	ud := L.CheckUserData(n)
	if gw, ok := ud.Value.(*git.GitWorktree); ok {
		return gw
	}
	L.ArgError(n, "worktree expected")
	return nil
}

var worktreeMethods = map[string]lua.LGFunction{
	"branch_name":    worktreeBranchName,
	"path":           worktreePath,
	"repo_path":      worktreeRepoPath,
	"is_dirty":       worktreeIsDirty,
	"is_checked_out": worktreeIsCheckedOut,
	"commit":         worktreeCommit,
	"push":           worktreePush,
}

func worktreeBranchName(L *lua.LState) int {
	gw := checkWorktree(L, 1)
	L.Push(lua.LString(gw.GetBranchName()))
	return 1
}

func worktreePath(L *lua.LState) int {
	gw := checkWorktree(L, 1)
	L.Push(lua.LString(gw.GetWorktreePath()))
	return 1
}

func worktreeRepoPath(L *lua.LState) int {
	gw := checkWorktree(L, 1)
	L.Push(lua.LString(gw.GetRepoPath()))
	return 1
}

func worktreeIsDirty(L *lua.LState) int {
	gw := checkWorktree(L, 1)
	dirty, err := gw.IsDirty()
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	}
	L.Push(lua.LBool(dirty))
	return 1
}

func worktreeIsCheckedOut(L *lua.LState) int {
	gw := checkWorktree(L, 1)
	out, err := gw.IsBranchCheckedOut()
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	}
	L.Push(lua.LBool(out))
	return 1
}

func worktreeCommit(L *lua.LState) int {
	gw := checkWorktree(L, 1)
	msg := L.CheckString(2)
	if err := gw.CommitChanges(msg); err != nil {
		L.RaiseError("commit: %s", err.Error())
	}
	return 0
}

// worktreePush takes the commit message and an optional "open" bool
// (default false — scripts cannot pop a browser without asking). The
// open flag maps to GitWorktree.PushChanges' second parameter.
func worktreePush(L *lua.LState) int {
	gw := checkWorktree(L, 1)
	msg := L.CheckString(2)
	open := false
	if L.GetTop() >= 3 {
		open = L.CheckBool(3)
	}
	if err := gw.PushChanges(msg, open); err != nil {
		L.RaiseError("push: %s", err.Error())
	}
	return 0
}
