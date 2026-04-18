-- defaults.lua: stock keymap baked into the binary via go:embed.
-- Loaded before user scripts in ~/.claude-squad/scripts/; users can
-- override any binding with cs.unbind + cs.bind (or just cs.bind,
-- which overwrites). Handlers are wrapped in lambdas so deferred
-- primitives yield inside a Lua-level coroutine frame.

-- Navigation
cs.bind("up",   function() cs.actions.cursor_up() end,   { help = "up" })
cs.bind("k",    function() cs.actions.cursor_up() end)
cs.bind("down", function() cs.actions.cursor_down() end, { help = "down" })
cs.bind("j",    function() cs.actions.cursor_down() end)
cs.bind("d",    function() cs.actions.toggle_diff() end, { help = "diff" })

-- Lifecycle
cs.bind("n", function() cs.actions.new_instance{} end,              { help = "new" })
cs.bind("N", function() cs.actions.new_instance{prompt=true} end,   { help = "new with prompt" })
cs.bind("D", function() cs.actions.kill_selected{} end,             { help = "kill" })
cs.bind("p", function() cs.actions.push_selected{} end,             { help = "push branch" })
cs.bind("c", function() cs.actions.checkout_selected{} end,         { help = "checkout" })
cs.bind("r", function() cs.actions.resume_selected() end,           { help = "resume" })
cs.bind("?", function() cs.actions.show_help() end,                 { help = "help" })
cs.bind("q", function() cs.actions.quit() end,                      { help = "quit" })

-- Workspace
cs.bind("W", function() cs.actions.open_workspace_picker() end, { help = "workspace" })
cs.bind("[", function() cs.actions.workspace_prev() end,        { help = "prev ws" })
cs.bind("l", function() cs.actions.workspace_prev() end)
cs.bind("]", function() cs.actions.workspace_next() end,        { help = "next ws" })
cs.bind(";", function() cs.actions.workspace_next() end)

-- Attach
cs.bind("alt+a",  function() cs.actions.fullscreen_attach_agent() end,    { help = "fullscreen agent" })
cs.bind("alt+t",  function() cs.actions.fullscreen_attach_terminal() end, { help = "fullscreen terminal" })
cs.bind("ctrl+a", function() cs.actions.inline_attach_agent() end,        { help = "attach agent" })
cs.bind("ctrl+t", function() cs.actions.inline_attach_terminal() end,     { help = "attach terminal" })

-- Quick input
cs.bind("a", function() cs.actions.quick_input_agent() end,    { help = "input to agent" })
cs.bind("t", function() cs.actions.quick_input_terminal() end, { help = "input to terminal" })
