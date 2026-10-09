# Adding or changing a launch option

**Symptom:** the source diff is small and green, then two clusters of tests break, or worse, keep passing. Tests that walk an overlay's rows by pressing `j` N times land on the wrong row. A change to the composed command line breaks assertions across `app`, `session` and `session/agent`, and a bulk find-and-replace turns an idempotency test green while breaking the guarantee it pins.

**Cause:** an option crosses config, `launch.Options`, an agent adapter, two overlays, the instance, the model and the wire, and its tests assert row positions and composed strings that a grep of the source for the option's name never finds.

**Rule:** classify the option, walk every touch point below, and grep the tests before estimating:

```sh
grep -rn -e '--<flag> ' --include='*_test.go' .
grep -rn 'i < [0-9]; i++' ui/overlay/*_test.go app/*_test.go
```

## Classify it

| Kind | Today | How it reaches the session |
|---|---|---|
| Command line | remote control, permission mode, model, 1M context, effort | `launch.Compose` bakes it into the instance's program; `launch.Parse` decodes it back. Persisted only as that program string |
| Session environment | Headroom proxy, 1h cache TTL | Never in the program: `session.LaunchEnv` and `InstanceEnv` turn it into tmux `-e` variables. Persisted as its own `InstanceData` field |
| Recorded on the instance | branch prefix, account | `Instance.SetBranchPrefix` (consumed by worktree setup, never persisted) and `Instance.SetAccount` (persisted) |

## Touch points

**Compiler** means the build fails until it's done, **enforced** that a named test does, and **silent** that nothing fails if you miss it.

1. **Config.** A `config.Settings` field and getter (`config/settings.go`) and its default in `DefaultConfig` (`config/config.go`): silent. A pointer field (nil stands for a `config.json` that predates it) also needs a deep copy in `Settings.Clone` (silent: `TestSettings_CloneSharesNothing` checks two pointers) and a line in `TestSettings_ConfigJSONIsUnchanged`'s fixture (silent).
2. **Options.** A `launch.Options` field, seeded in `FromSettings` (`session/launch/launch.go`): silent. `TestLaunchOptionsFromConfig` covers only the fields it names.
3. **Command-line kind.** An `Apply…Flag` method on the `Adapter` interface (`session/agent/adapter.go`): compiler, for every adapter in `session/agent/`. A `Build…Command` wrapper (`session/agent_restart.go`), a step in `Compose` (order matters), and a case in `Parse` that strips the flag and its value from the base program: silent. `TestParseLaunchOptions_RoundTrip` covers only the cases it lists. A flag `Parse` doesn't strip stays in the base program, and since each `Apply…Flag` returns its input unchanged when the flag is already there, the `R` flow silently drops the user's new choice.
4. **Environment kind.** A `LaunchEnv` field and an `…Env` helper folded into `InstanceEnv` (`session/agent_restart.go`); an `Instance` field, accessor and a parameter of `SetLaunchOptions` (`session/instance.go`); an `InstanceOptions` field; an `InstanceData` field (`session/storage.go`), which is a schema change: follow [`changing-the-instance-schema.md`](changing-the-instance-schema.md). Also an `InstanceView` field (`core/view.go`), since the `R` flow seeds the option from the row: silent.
5. **The overlays.** A row in `ui/overlay/sessionLaunchOptions.go` (`toggleCursor`, `Render`, `sessionLaunchOptionsRowCount`, `sessionLaunchOptionsBranchPrefixRow`; the Account row is appended after Branch Prefix so nothing moves when it appears) and, for the global default, in `ui/overlay/claudePreferences.go` (`claudePrefsRowCount`): silent, because rows are positional `case` indices.
6. **The `R` flow.** `app/intents.go:runRestartWithOptionsSelected` seeds from the row everything `Parse` can't recover (the environment and recorded kinds): silent. A missed one resets to its zero value on every restart with options.
7. **The model.** `core/requests.go:applyLaunch` composes and records the options for `Create` and `ResumeWith`, and `core/load.go:ensureTerminal` composes the workspace terminal's from config, passing environment options into `session.InstanceOptions`: silent.
8. **The wire.** `launch.Options` travels in `Create` and `ResumeWith`, so a field changes `docs/specs/protocol.md`: rewrite it with `go test ./core/rpc -run TestProtocolReference -update` (enforced: `TestProtocolReference` fails until you do). An added field is compatible, so `rpc.Protocol` stays.
9. **Exclusive options.** Remote control and the Headroom proxy exclude each other in both overlays' toggles and again in `Compose`, through `EffectiveRemoteControl`, which every `RemoteControlBlocked` caller uses too: a new exclusion goes in all three places, silently otherwise.

## Traps

- **Rows renumber.** Inserting a row mid-list shifts every `case` below it, and tests that press `j` N times (`ui/overlay/sessionLaunchOptions_test.go`, `ui/overlay/claudePreferences_test.go`, `app/branch_prefix_test.go`, `app/state_settings_test.go`) land on the wrong row. `TestClaudePreferencesRowNavigationClamps` and `TestSessionLaunchOptionsRowNavigationClamps` over-scroll on purpose to assert clamping: bump their counts too, or they pass on the last row while testing nothing.
- **Input or output.** `--model X` appears in tests as loom's output (it changes with the format) and as input standing in for a user-typed or legacy program (it must not). `TestBuildModelCommand_Idempotent` is the trap: its input `claude --model opus` comes back unquoted because the adapter returns early when the flag is present, so a bulk replace breaks the guarantee and stays green. Read each hit and classify it.
- **Old programs decode forever.** Every persisted program is decoded through `Parse` on load, so a format change keeps accepting the old shape: `parseModelValue` strips the single quotes `ApplyModelFlag` adds (tmux runs the program through a shell, and zsh would glob the `[1m]` suffix) and still takes unquoted values (`TestParseLaunchOptions_LegacyUnquotedModel`).
- **Bottom-up edits.** When rewriting many indexed assertions with a script, go from the highest index down: rewriting 4→5 before 5→6 makes a duplicate anchor and the edit aborts.
- **`launch` stays a leaf.** It imports only `config` and `session`, so the model composes a program without importing the UI.

## What the gates won't tell you

- No test lines up `launch.Options` fields against `FromSettings`, `Parse`, the overlays and the `R` flow: a field missed in any of them compiles and passes.
- An alias that doesn't take `[1m]` composes to the bare alias, so `{haiku, Context1M: true}` re-decodes with `Context1M` false and the checkbox reverts on resume. That is deliberate: the alternative is a pane that dies at launch.
