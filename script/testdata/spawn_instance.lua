-- Sample script: spawn a new session with a pre-filled prompt.
-- Copy to ~/.claude-squad/scripts/ to activate.

cs.register_action{
  key = "ctrl+shift+n",
  help = "Spawn session with review prompt",
  run = function(ctx)
    local title = cs.sprintf("review-%d", cs.now())
    ctx:new_instance{
      title = title,
      prompt = "Review the last commit and propose improvements.",
    }
    ctx:notify(cs.sprintf("spawned %s", title))
  end,
}
