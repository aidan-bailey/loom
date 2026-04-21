-- defaults.lua: stock keymap baked into the binary via go:embed.
-- Loaded before user scripts in ~/.loom/scripts/; users can
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

-- File explorer
cs.bind("f", function() cs.actions.toggle_file_explorer() end, { help = "files" })

-- Scroll (active content pane: diff if visible, else agent or the
-- inline-attached terminal). Half-page bindings favor keyboard-centric
-- workflows that never reach for the mouse wheel.
cs.bind("pgup",       function() cs.actions.scroll_page_up() end,   { help = "scroll page up" })
cs.bind("pgdown",     function() cs.actions.scroll_page_down() end, { help = "scroll page down" })
cs.bind("home",       function() cs.actions.scroll_top() end,       { help = "scroll top" })
cs.bind("end",        function() cs.actions.scroll_bottom() end,    { help = "scroll bottom" })
cs.bind("ctrl+u",     function() cs.actions.scroll_page_up() end)
cs.bind("ctrl+d",     function() cs.actions.scroll_page_down() end)
cs.bind("shift+up",   function() cs.actions.scroll_line_up() end,   { help = "scroll line up" })
cs.bind("shift+down", function() cs.actions.scroll_line_down() end, { help = "scroll line down" })

-- Explicit terminal scroll — bypasses the active-pane rule so the user
-- can review terminal history while the agent pane stays focused. Matches
-- the alt+a / alt+t "alt = terminal-aware" convention.
cs.bind("alt+pgup",   function() cs.actions.scroll_terminal_page_up() end)
cs.bind("alt+pgdown", function() cs.actions.scroll_terminal_page_down() end)

-- List navigation. Capital K/J page the session list; g/G jump to ends.
cs.bind("K", function() cs.actions.list_page_up() end,   { help = "list page up" })
cs.bind("J", function() cs.actions.list_page_down() end, { help = "list page down" })
cs.bind("g", function() cs.actions.list_top() end,       { help = "list top" })
cs.bind("G", function() cs.actions.list_bottom() end,    { help = "list bottom" })
