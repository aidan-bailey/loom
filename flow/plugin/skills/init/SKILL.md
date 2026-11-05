---
name: init
description: Draft this repo's flow profile (prefix, gates, landmines, idioms, design registries) for the flow roster, then check it with doctor. Owner-started, /flow:init.
disable-model-invocation: true
---

Draft a flow profile for the repository the session is in, so that
`/flow:supervise` can run plans here with the flow roster.

1. Run `git rev-parse --show-toplevel`. Outside a git repo, stop. If
   `.claude/agents/plan-execution-supervisor.md` exists at the top level, the
   repo has its own roster: stop and say so, naming the repo's own commands
   (look under its `.claude/commands/` and `.claude/skills/`).
2. Run `loom flow conf status`. If it names a profile, show it and
   ask the owner whether to draft a new one; stop if not. A new draft replaces
   it only once the owner agrees, in step 6.
3. Dispatch one `flow:recon`, so the reading stays in its context and out of this
   session's, with this brief:

   > Draft a flow profile (schema 1) for the repository at <top level>. Read
   > its manifests (`Cargo.toml`, `package.json`, `pyproject.toml`, `go.mod`),
   > `flake.nix`, every CI workflow and every CLAUDE.md file, and find its
   > generated files and the tests that count registered items. Return one
   > TOML block with these keys, each value followed by a comment naming the
   > `file:line` it came from, then a list of what you couldn't settle:
   > - `schema = 1`;
   > - `prefix`: when `flake.nix` defines a dev shell that CI or CLAUDE.md runs
   >   builds in, `direnv exec .` if the main checkout's `.envrc` loads it
   >   (`use flake`; a worktree inside the checkout finds that `.envrc` above
   >   it), else the nix command that runs a command inside it (its
   >   `develop --command` form, which leaves a `/tmp/nix-shell.*` directory
   >   behind on every run); else `""`;
   > - `services`: commands CLAUDE.md says must run before tests, such as a
   >   dev database's start script;
   > - `generated`: globs for files the build regenerates that a plan would
   >   never write by hand;
   > - `[gates] story`: the checks to run after each change, check-only — the
   >   formatter's check form, the linter, the tests — with `{packages}` in
   >   place of the package selection in each cargo command
   >   (`cargo clippy {packages} --all-targets -- -D warnings`). `{packages}`
   >   expands only in a Cargo workspace; elsewhere a gate runs the whole tree
   >   (`go test ./...`). A check that reports by printing and exits 0, as
   >   `gofmt -l` does, needs a wrapper that fails on output:
   >   `bash -c 'out=$(gofmt -l .) || exit 1; test -z "$out" || { echo "$out"; exit 1; }'`;
   > - `[gates] final`: one command per CI job, without the steps that only
   >   set up CI (checkout, toolchain installs, caches);
   > - `[gates] vacuous`: regular expressions for output that exits 0 but
   >   tested nothing, for the repo's test runners. Each is matched against
   >   every line of a gate's output, and one matching line makes the gate
   >   VACUOUS, so a runner that prints a summary per test binary needs a
   >   pattern only a name filter can produce: for cargo test,
   >   `'\b0 passed; 0 failed; \d+ ignored; 0 measured; [1-9]\d* filtered out'`.
   >   A pattern that also matches a binary with no tests (`0 filtered out`)
   >   makes every unfiltered run VACUOUS. With it, a name-filtered cargo test
   >   must name its binary (`--lib`, `--test <name>`); say so in the
   >   pattern's comment. For go test, `'\[no tests to run\]'`, and
   >   `'^testing: warning: no tests to run'` under `-v`: a `-run` filter that
   >   matches nothing in a package prints it, but so does an unfiltered run
   >   over a package whose test files define no Test func, so first check
   >   that `go test ./...` prints neither. `[no test files]` must never
   >   match. Write each regex as a TOML literal string, in single quotes: in
   >   a basic string `\d` is an invalid escape;
   > - `[build] exclusive`: the build tools whose runs share a build directory
   >   or the machine's memory (`cargo`);
   > - `[landmines] markers`: substrings that mark a shape CLAUDE.md calls
   >   dangerous, one each. A story whose code holds a marker must be deep,
   >   with the full review, or name the marker in its Review line, so a
   >   marker must appear only in code that has the shape (`unsafe {`,
   >   `.to_f64()`): never a crate, module, venue or type name that most
   >   stories' code mentions, and never a rule a story gate already enforces;
   > - `[landmines] notes`: those shapes in prose, as a list of strings, one
   >   per shape, each with why it is dangerous;
   > - `[review] idioms`: what CLAUDE.md asks reviewers to check that the
   >   gates don't, as a list of strings, one per idiom;
   > - `[design] registries`: the places outside a story's own files that
   >   list or count what stories add, as a list of strings, one per place,
   >   each naming its file path alone in its own backticks (any symbol in
   >   separate backticks) and what it lists or counts: a
   >   generated region with the command that regenerates it, a route or
   >   command table, a test that counts registered items, a document that
   >   states a count. Draw them from CLAUDE.md rules that name such lists,
   >   from the generated files and from the tests that count things. An
   >   empty list is valid;
   > - `[paths] state = ""` and `[paths] records = ""`.
   > Every gate is one command; a gate that needs shell syntax is written as
   > `bash -c '…'`. Cite only lines you read.

4. Write the draft to `.claude/flow.toml` at the top level (make the
   directory if it is missing; keep any profile already there as
   `.claude/flow.toml.old` until the owner decides). Show it to the owner, with
   what the recon couldn't settle, and let them edit it.
5. When the owner has finished, run `loom flow conf doctor`, and fix
   what it FAILs with them until it passes. Doctor checks each backticked
   token of a registry that holds a `/`, so a path with a directory and not a
   top-level `README.md`, and has no wildcard, space or URL scheme. It cuts a
   trailing `:<line>` or `:<start>-<end>` and a `#anchor` first, and skips
   absolute and `~` paths and placeholders (`<...>`, `{...}`) as well as URLs.
   A WARN that a registry names a path the worktree lacks is a registry to
   correct or drop.
6. When doctor passes and the owner agrees, remove any `.claude/flow.toml.old`
   and commit the profile in this repo by pathspec:
   `git add -- .claude/flow.toml`, then
   `git commit -m "<the repo's commit style>: its flow profile" -- .claude/flow.toml`.
   Every worktree cut after that commit finds it.
