-- Sample script: push the selected session's branch with a
-- timestamped commit message.
-- Copy to ~/.claude-squad/scripts/ to activate.

cs.register_action{
  key = "ctrl+shift+p",
  help = "Push selected branch (timestamped)",
  precondition = function(ctx)
    local inst = ctx:selected()
    return inst ~= nil and inst:started()
  end,
  run = function(ctx)
    local inst = ctx:selected()
    if not inst then
      ctx:notify("no session selected")
      return
    end
    local wt = inst:worktree()
    if not wt then
      ctx:notify("session has no worktree")
      return
    end
    local msg = cs.sprintf("auto-push %d", cs.now())
    wt:push(msg, false)
    ctx:notify(cs.sprintf("pushed %s", wt:branch_name()))
  end,
}
