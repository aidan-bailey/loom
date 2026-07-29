# NOTICE

Loom is a fork of [smtg-ai/claude-squad](https://github.com/smtg-ai/claude-squad)
at v1.0.17 (April 2026). Substantial changes have been made since that fork
point — including workspace registry, Lua scripting, structured logging,
and a full rename of the project — so Loom is now maintained independently
at [aidan-bailey/loom](https://github.com/aidan-bailey/loom).

The original project is © 2024–2025 smtg-ai contributors and is distributed
under the GNU Affero General Public License v3.0. Loom retains that license
(see `LICENSE.md`); this notice and the README's "Origin" section satisfy
the AGPL §5 requirement to carry prominent notices of modification.

## Vendored code: crit

The review subsystem (`review/`, `review/gitdiff/`, `ui/review/`) is derived
from [kevindutra/crit](https://github.com/kevindutra/crit) at commit
`e9e5d19`, © kevindutra, distributed under the MIT License (declared in the
upstream README's License section). Substantial modifications were made
during absorption: paths are threaded through an explicit worktree root
instead of the process working directory, the CLI / detached tmux mode /
legacy JSON migration were dropped, and the TUI was re-styled onto loom's
theme system and embedded as a workbench panel.
