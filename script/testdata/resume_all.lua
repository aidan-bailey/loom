-- Sample script: resume every paused session.
-- Copy to ~/.claude-squad/scripts/ to activate.

cs.register_action{
  key = "ctrl+shift+r",
  help = "Resume all paused sessions",
  run = function(ctx)
    local resumed = 0
    for _, inst in ipairs(ctx:instances()) do
      if inst:paused() then
        inst:resume()
        resumed = resumed + 1
      end
    end
    ctx:notify(cs.sprintf("resumed %d session(s)", resumed))
  end,
}
