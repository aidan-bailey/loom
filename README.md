# Loom [![CI](https://github.com/aidan-bailey/loom/actions/workflows/build.yml/badge.svg)](https://github.com/aidan-bailey/loom/actions/workflows/build.yml) [![GitHub Release](https://img.shields.io/github/v/release/aidan-bailey/loom)](https://github.com/aidan-bailey/loom/releases/latest)

Loom is a terminal app that manages multiple [Claude Code](https://github.com/anthropics/claude-code), [Codex](https://github.com/openai/codex), [Gemini](https://github.com/google-gemini/gemini-cli) (and other local agents including [Aider](https://github.com/Aider-AI/aider)) in separate workspaces, allowing you to work on multiple tasks simultaneously.

### Origin

> Loom was forked from [smtg-ai/claude-squad](https://github.com/smtg-ai/claude-squad) at v1.0.17 (April 2026) and has diverged substantially since. See [NOTICE.md](NOTICE.md) for details.

### Highlights
- Complete tasks in the background (including yolo / auto-accept mode!)
- Manage instances and tasks in one terminal window
- Review changes before applying them, checkout changes before pushing them
- Each task gets its own isolated git workspace, so no conflicts
- Lua-scripted keymap and a workspace registry for multi-repo flows

### Installation

Loom installs as the `loom` binary.

```bash
curl -fsSL https://raw.githubusercontent.com/aidan-bailey/loom/main/install.sh | bash
```

This puts the `loom` binary in `~/.local/bin`.

To use a custom name for the binary:

```bash
curl -fsSL https://raw.githubusercontent.com/aidan-bailey/loom/main/install.sh | bash -s -- --name <your-binary-name>
```

### Prerequisites

- [tmux](https://github.com/tmux/tmux/wiki/Installing)
- [gh](https://cli.github.com/)

### Usage

```
Usage:
  loom [directory] [flags]
  loom [command]

Available Commands:
  completion  Generate the autocompletion script for the specified shell
  debug       Print debug information like config paths
  help        Help about any command
  reset       Reset all stored instances
  version     Print the version number of loom
  workspace   Manage workspaces

Flags:
  -y, --autoyes            [experimental] If enabled, all instances will automatically accept prompts
  -h, --help               help for loom
      --log-level string   Override log level for the Structured logger (debug|info|warn|error). Takes precedence over LOOM_LOG_LEVEL.
      --no-scripts         Skip loading ~/.loom/scripts (embedded defaults still load). Use to recover from a broken user script.
  -p, --program string     Program to run in new instances (e.g. 'aider --model ollama_chat/gemma3:1b')
  -v, --version            version for loom
  -w, --workspace string   Select workspace by name (bypasses auto-detection)
```

Run the application with:

```bash
loom
```

NOTE: The default program is `claude` and we recommend using the latest version.

<b>Using Loom with other AI assistants:</b>
- For [Codex](https://github.com/openai/codex): Set your API key with `export OPENAI_API_KEY=<your_key>`
- Launch with specific assistants:
   - Codex: `loom -p "codex"`
   - Aider: `loom -p "aider ..."`
   - Gemini: `loom -p "gemini"`
- Make this the default, by modifying the config file (locate with `loom debug`)

#### Menu
The menu at the bottom of the screen shows available commands:

##### Instance/Session Management
- `n` - Create a new session
- `N` - Create a new session with a prompt
- `D` - Kill (delete) the selected session
- `↑/j`, `↓/k` - Navigate between sessions

##### Actions
- `↵/o` - Attach to the selected session to reprompt
- `ctrl-q` - Detach from session
- `s` - Commit and push branch to github
- `c` - Checkout. Commits changes and pauses the session
- `r` - Resume a paused session
- `?` - Show help menu

##### Navigation
- `tab` - Switch between preview tab and diff tab
- `q` - Quit the application
- `shift-↓/↑` - scroll in diff view

### Configuration

Loom stores its configuration in `~/.loom/config.json`. You can find the exact path by running `loom debug`.

#### Migration from claude-squad

On first launch, Loom renames `~/.claude-squad/` → `~/.loom/` atomically so your in-flight instances, worktrees, and scripts continue to work. Live tmux sessions with the legacy `claudesquad_` prefix are renamed to `loom_` before reconciliation so running agents keep their panes.

The `CLAUDE_SQUAD_HOME`, `CLAUDE_SQUAD_LOG_LEVEL`, and `CLAUDE_SQUAD_LOG_FORMAT` environment variables are still honored as deprecated fallbacks with a one-time warning; the preferred names are `LOOM_HOME`, `LOOM_LOG_LEVEL`, and `LOOM_LOG_FORMAT`.

#### Profiles

Profiles let you define multiple named program configurations and switch between them when creating a new session. When more than one profile is defined, the session creation overlay shows a profile picker that you can navigate with `←`/`→`.

To configure profiles, add a `profiles` array to your config file and set `default_program` to the name of the profile to select by default:

```json
{
  "default_program": "claude",
  "profiles": [
    { "name": "claude", "program": "claude" },
    { "name": "codex", "program": "codex" },
    { "name": "aider", "program": "aider --model ollama_chat/gemma3:1b" }
  ]
}
```

Each profile has two fields:

| Field     | Description                                              |
|-----------|----------------------------------------------------------|
| `name`    | Display name shown in the profile picker                 |
| `program` | Shell command used to launch the agent for that profile  |

If no profiles are defined, Loom uses `default_program` directly as the launch command (the default is `claude`).

### FAQs

#### Failed to start new session

If you get an error like `failed to start new session: timed out waiting for tmux session`, update the underlying program (ex. `claude`) to the latest version.

### How It Works

1. **tmux** to create isolated terminal sessions for each agent
2. **git worktrees** to isolate codebases so each session works on its own branch
3. A simple TUI interface for easy navigation and management

### License

[AGPL-3.0](LICENSE.md)
