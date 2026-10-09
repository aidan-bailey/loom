# Adding a full-screen modal state

**Symptoms when a step is missed:** typing fast into the new modal's text field reorders characters ("toy" renders "oyt"); or the modal takes keys but never appears on screen; or keys meant for the modal act on the hidden focus layout.

**Cause.** A modal state is an `m.state` value (`app/app.go`), and several places in `app/` enumerate states by hand. Nothing ties them together: the compiler accepts a missing entry, and no test lists every state, so a new state passes every existing test.

## Checklist

Each step says whether the compiler or a test catches a miss.

1. **The state constant.** Add it to the `state` iota block in `app/app.go`, with a comment saying what is on screen. Silent.
2. **The overlay.** Build the dialog in `ui/overlay/` against `overlay.Overlay`, add an `overlayKind` and a typed accessor in `app/overlay_host.go`, open it with `setOverlay` and close it with `dismissOverlay`. The window-size handler sizes `m.activeOverlay` generically. Compiler-checked only for the interface.
3. **The key handler.** Write `handleState<Name>Key` in `app/state_<name>.go` and route it in `handleKeyPress`'s switch (`app/app.go`). Silent: a missing case falls to `handleStateDefaultKey`, so the default keymap acts behind the modal.
4. **The menu-highlighting exclusion list.** Add the state to the list in `handleMenuHighlighting` (`app/app.go`), alongside `statePrompt`, `stateNew`, `stateLaunchOptions` and the rest. Silent: that function defers a key matching a built-in binding (`a`, `t`, `d`, `n`, …) to a later Update through an async replay, tracked by one flag (`m.keySent`) that assumes the next key press is the replay. In a modal with a text field, fast typing or a paste steals the flag and the deferred key lands later, wherever the cursor moved: "toy" became "oyt" in the settings overlay's name fields until `stateSettings` joined the list. Write a burst test like `TestHandleKeyPress_SettingsProfilesNameBurstNotScrambled` (`app/menu_highlighting_settings_test.go`).
5. **The overlay placement list.** If the modal draws through `m.activeOverlay`, add the state to the `switch m.state` in `View()` (`app/app.go`) that places the overlay over the screen. Silent: the state takes keys but nothing draws it; the issue picker (`I`) shipped invisible this way. Write a test like `TestView_DrawsTheIssuePicker` (`app/state_issue_picker_test.go`).
6. **Cancel paths.** If the flow holds a creation `draft`, every cancel path calls `discardDraft` (`discardPendingLaunchOptionsCancel` for the launch-options closure). Silent: a draft row left in the rail.
7. **Background messages.** `m.state` gates only key routing: completions, late issue fetches and script results still land while the modal is open. Snapshot at open time whatever its confirm will act on (as the merge picker does with `pendingMergeSourceItems`), and let the request re-validate it. Completions already move the focused selection only in `stateDefault`, late issue results drop themselves outside it, and `deferFocusMutation` applies only there, so a new state is safe from those without further work. Silent.
8. **Opening it from overview.** A key that opens the modal from overview must be in `overviewKeyAllowed` (`app/state_default.go`), or drop to focus first as `n`/`N` do. Pinned for existing keys by `TestOverviewKeyWhitelist_BlocksFocusOnlyKeys`.
9. **Mouse.** Mouse messages reach Update in every state; selection runs only in `stateDefault` and `stateInlineAttach`. A modal that wants the mouse routes it explicitly in the mouse cases of `update`. Silent.
10. **Docs.** A new key goes in `script/defaults.lua`, `keys.GlobalkeyBindings` (pinned by `TestKeymapParity`), `USAGE.md`'s Keyboard Reference and the root keybinding table.

## What the gates still won't tell you

No test enumerates `m.state` values, so steps 3 to 5 for a brand-new state are guarded only by the tests you write for it. Write both tests from steps 4 and 5 for every new modal state.
