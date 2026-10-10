# Cutting a release

**Symptoms when a step goes wrong:** the new `CHANGELOG.md` section swallows the sections of earlier releases; the release workflow fails in GoReleaser on Windows after every CI check passed; a merged version bump produces no release; `nix build` throws "failed to find `version = \"X.Y.Z\"` in main.go".

**Cause.** A release is driven by one line, `version = "X.Y.Z"` in `main.go`. `.github/workflows/release.yml` runs when the Build workflow succeeds on `main` (or on `workflow_dispatch`), extracts that line with `grep -E '^[[:space:]]*version[[:space:]]*='`, skips if a GitHub release `v$VERSION` exists, and otherwise tags the commit, writes the release notes with git-cliff (`cliff.toml`, `--latest`) and runs GoReleaser (`.goreleaser.yaml`: darwin, linux and windows, amd64 and arm64). `flake.nix` reads the same line with its own pattern. Tags exist only for versions whose bump reached `main` on GitHub, so local tags routinely lag the versions already released, and git-cliff draws release boundaries from tags, not from the headings in `CHANGELOG.md`.

**Rule:** `main.go`'s `version` is the only manifest; prepend a changelog section for the commits since the previous release, never regenerate the file; cross-build for Windows before merging.

## Checklist

1. **Bump `version` in `main.go`.** Nothing else carries the version: `flake.nix` and the release workflow read it, and `README.md` and `USAGE.md` name none. Keep the line's shape exactly (indented, `version = "X.Y.Z"`, digits and dots only): the flake's pattern accepts no `-rc1` suffix and throws without a match.
2. **Find the previous release commit:** `git log --oneline --grep '^chore(release)'`. Its subject is `chore(release): vX.Y.Z`.
3. **Prepend this release's section:** `git cliff <prev>..HEAD --tag vX.Y.Z --prepend CHANGELOG.md`, with `<prev>` the commit from step 2.
4. **Re-insert the blank line** that `--prepend` drops before the previous `## [x.y.z]` heading, to match the file.
5. **Read the new section.** It should hold only this release's commits, grouped by type as `cliff.toml` maps them (`chore(release)` and `chore(deps…)` commits are skipped).
6. **Cross-build for Windows:** `GOOS=windows CGO_ENABLED=0 go build ./...`, and `GOOS=windows CGO_ENABLED=0 go vet` the packages that touch syscalls, processes or sockets. CI's matrix in `.github/workflows/build.yml` builds only linux and darwin.
7. **Commit `main.go` and `CHANGELOG.md` as `chore(release): vX.Y.Z`**, merge to `main` and push. The bump touches a Go file, so Build runs, and its success triggers the release.

## Traps

- **Full regeneration merges releases.** `git cliff -o CHANGELOG.md --tag v$VERSION`, the command this repo once documented, rebuilds every section from tags. On 2026-08-21 the newest tag was v0.8.1 while 0.9.0, 0.10.0 and 0.11.0 had shipped, so it would have folded all three into one section, without an error. Release commit 7995510 shows the safe shape: insertions only.
- **The GitHub release notes have the same blind spot.** The release workflow runs git-cliff with `--latest`, which covers the commits since the previous tag; with that tag missing, the notes span several releases. Check `git tag` against `git log --grep '^chore(release)'` before pushing a bump.
- **A Unix-only call passes CI and breaks the release.** `syscall.Setsid`, `syscall.Stat_t` and `syscall.Kill` built green until GoReleaser built Windows; the daemon's stage 3B did this, fixed by splitting `internal/daemon/proc_unix.go` and `internal/daemon/proc_windows.go`. Use `os.SameFile` rather than raw inode numbers, and put the rest behind `_unix.go`/`_windows.go` files.
- **No Go change, no release.** Build is path-scoped to Go files, `go.mod`, `go.sum` and its own workflow, and the release waits on Build. A docs-only commit never releases; `workflow_dispatch` runs the release by hand.

## What the gates won't tell you

Nothing compares `CHANGELOG.md` with the tags or the release commits, and no CI job builds Windows before the release does. Adding windows to the build matrix would close the second gap; that is the maintainer's call.
