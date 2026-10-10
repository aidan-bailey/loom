# Contributing

Thank you for considering contributing to Loom! This document outlines the process for contributing.

## Development Setup

1. Fork the repository
2. Clone your fork: `git clone https://github.com/YOUR-USERNAME/loom.git`
3. Add the upstream repository: `git remote add upstream https://github.com/aidan-bailey/loom.git`
4. Install dependencies: `go mod download`

The Go version is the `go` directive in `go.mod`. A Nix flake (`flake.nix`) provides a dev shell with Go, golangci-lint, tmux, git and gh (`nix develop`), and `nix run .` builds and runs loom without one.

### Build and test

```bash
# Build (CGO is off for builds)
CGO_ENABLED=0 go build -o loom

# Run the tests; CGO off, since go test otherwise wants gcc
CGO_ENABLED=0 go test ./...

# One package's tests
CGO_ENABLED=0 go test -v ./config
CGO_ENABLED=0 go test -v ./session/git

# The race detector needs CGO and a C compiler (CC=clang where gcc is absent); CI runs it
CC=clang CGO_ENABLED=1 go test -race ./...

# The end-to-end suite: real loom builds in a sandbox (needs tmux)
go test -tags e2e ./e2e/...

# Install a release binary into ~/.local/bin
./install.sh
```

### Dev loop

Run your build through the dev sandbox rather than directly — especially from inside loom, where a bare `./loom` is refused:

```bash
go run ./tools/loomdev up                 # create + build a sandbox named after your branch
go run ./tools/loomdev run                # try it interactively (the sandbox's daemon keeps running after)
go run ./tools/loomdev start              # or headless, then: wait --text toy / keys … / shot
go run ./tools/loomdev stop               # quit the headless TUI and stop the sandbox's daemon (--keep-daemon keeps it)
go run ./tools/loomdev daemon             # show the sandbox's daemon; --stop / --kill it with its TUIs left open (they wait / reconnect)
go run ./tools/loomdev down               # stop the sandbox's daemon, delete the sandbox and its tmux server
go run ./tools/loomdev down --force       # same, SIGKILLing a lock holder that won't stop if it runs a build from the sandbox's bin dir
```

### Cleanup

These wipe real state, and both refuse to run inside a loom-managed tmux session:

```bash
./clean.sh        # kill the tmux server, remove worktrees and ~/.loom/
./clean_hard.sh   # the same, plus git worktree prune
```

## Code Standards

### Format and lint

```bash
# Format (CI enforces it). Never plain `gofmt -w .`: it rewrites vendor/
gofmt -w $(git ls-files '*.go' | grep -v '^vendor/')

# Lint, as CI runs it (the version is pinned in .github/workflows/lint.yml)
golangci-lint run --timeout=3m --fast

# Where your golangci-lint is v2, which can't read this repo's v1 config, run vet instead
CGO_ENABLED=0 go vet ./...

# The docs' structure: CLAUDE.md budgets, links, guides. `go test ./...` fails on it
# (TestRepo_ClaudeMDStructure), and CI's Docs workflow runs it on every push and PR to main.
# -v adds each file's headroom; -idents reports the symbols no source file holds.
go run ./tools/claudemd -v
go run ./tools/claudemd -idents
```

### Testing

Please include tests for new features or bug fixes.

### Sign-off

Commits should be signed off with `git commit -s`, certifying the DCO in the commit trailer.

## Questions?

Feel free to open an issue for any questions about contributing.
