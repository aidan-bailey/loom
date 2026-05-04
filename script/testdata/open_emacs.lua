-- Sample script: open emacs on the selected session's worktree as a
-- separate (graphical) process. The trailing & backgrounds emacs from
-- the terminal pane's shell so the TUI keeps running.
-- Copy to ~/.loom/scripts/ to activate.

cs.register_action{
  key = "e",
  help = "Open emacs",
  precondition = function(ctx)
    local inst = ctx:selected()
    return inst ~= nil and inst:started() and not inst:paused()
  end,
  run = function(ctx)
    local inst = ctx:selected()
    if not inst then
      ctx:notify("no session selected")
      return
    end
    local wt = inst:worktree()
    local target
    if wt then
      target = wt:path()
    else
      target = inst:path()
    end
    inst:send_terminal_keys("emacs " .. target .. " >/dev/null 2>&1 &")
  end,
}
