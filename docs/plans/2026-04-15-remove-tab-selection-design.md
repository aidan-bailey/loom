# Remove Tab-Based Pane Selection

## Summary

Remove `tab`, `i`, `enter`/`o`, and `shift+up`/`shift+down` keybindings since `a`, `t`, `ctrl+a`, `ctrl+t` already provide direct pane targeting. This simplifies the interaction model by eliminating indirect focus-then-act patterns in favor of direct targeting.

## Keys Removed

| Key | Current Function | Replacement |
|-----|-----------------|-------------|
| `tab` | Toggle focus between agent/terminal | `ctrl+a`/`ctrl+t` set focus explicitly |
| `i` | Quick input to focused pane | `a` (agent) / `t` (terminal) |
| `enter`/`o` | Inline attach (focus-dependent) | `ctrl+a` / `ctrl+t` |
| `shift+up`/`shift+down` | Scroll focused pane | Scroll while attached via `ctrl+a`/`ctrl+t` |

## Keys Unchanged

| Key | Function |
|-----|----------|
| `a` | Quick input to agent pane |
| `t` | Quick input to terminal pane |
| `ctrl+a` | Inline attach focused on agent |
| `ctrl+t` | Inline attach focused on terminal |
| `O` | Full-screen attach |
| `ctrl+q` | Detach from inline/full-screen |

## Focus Model Changes

- `focusedPane` state retained internally -- `ctrl+a`/`ctrl+t` set it, inline attach uses it to route keystrokes
- **Default state**: no visual focus indicator; both panes render with neutral/dim borders
- **Inline attach state**: focused pane gets purple highlight border
- Remove `ToggleFocus()` method -- focus only set explicitly via `SetFocusedPane()`
- Remove `QuickInputTargetFocused` -- only `QuickInputTargetAgent` and `QuickInputTargetTerminal` remain

## Cleanup Scope

- `keys/keys.go`: remove `KeyTab`, `KeyQuickInteract`, `KeyInlineAttach`, `KeyScrollUp`, `KeyScrollDown` definitions
- `app/app.go`: remove handlers for all removed keys
- `ui/split_pane.go`: remove `ToggleFocus()`, adjust render to not show focus in default state
- `ui/quick_input.go`: remove `QuickInputTargetFocused`
- `ui/menu.go`: remove tab from system group, remove `SetFocusedPane` if unused
- `app/app.go`: remove input routing default case for focused target
- Update CLAUDE.md, USAGE.md, help text
