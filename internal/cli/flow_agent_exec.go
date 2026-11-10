package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/flow"
	"github.com/aidan-bailey/loom/internal/flowrun"
)

// headlessPreamble goes before every agent run's first prompt (flow in loom
// part 3a, spec §5.4 and §2a, 9), so the agent definitions stay as they
// are. `--agent` turns `--json-schema` off, so the report ends in status
// lines instead.
const headlessPreamble = "You run headless as a loom agent job: nobody can message you, and SendMessage reaches no one. " +
	"Your final answer is your report. Its last line is its status: `STATUS: DONE`, `STATUS: DONE_WITH_CONCERNS`, `STATUS: BLOCKED` " +
	"or `STATUS: NEEDS_CONTEXT`. With NEEDS_CONTEXT, put a line `QUESTION: <your question>` just before it; the answer resumes you. " +
	"Run long steps through `loom flow wait`, as your rules say."

// reviewPreamble follows headlessPreamble for a reviewer's run (flow in loom
// part 3b, spec §5.6): loom flow run routes a review by these lines. The
// wording is the one the part 3b probe saw followed (2026-11-06).
const reviewPreamble = "Before your STATUS line, end your report with one line `FINDING: <severity> <file>:<line> <the fix>` per issue, " +
	"the severity being Critical, Important or Minor, followed by the word design when the fix needs a decision the spec doesn't make " +
	"(`FINDING: Important design a.go:12 <the fix>`), one line `PLAN: <the problem>` " +
	"per problem in the plan's own code, and then `VERDICT: APPROVED` or `VERDICT: CHANGES_REQUESTED`. Write no FINDING or PLAN line when there is none."

// developerPreamble follows headlessPreamble for a developer's run (part 3b,
// planning amendment 6): the run's record counts these lines.
const developerPreamble = "Before your STATUS line, list each deviation from the plan on its own line `DEVIATION: <what and why>`."

// rolePreamble is headlessPreamble with role's own lines.
func rolePreamble(role string) string {
	switch role {
	case "story-reviewer", "code-quality-reviewer":
		return headlessPreamble + " " + reviewPreamble
	case "junior-dev", "intermediate-dev", "senior-dev":
		return headlessPreamble + " " + developerPreamble
	}
	return headlessPreamble
}

// agentTurnCap is an agent run's status when its job stopped at its turn
// cap (part 3b, spec §6): no report, but its session resumes.
const agentTurnCap = "TURN_CAP"

// envAgentRole names the flow agent an agent job runs. The guard doesn't
// need it (the hook's agent_type names the agent, spec §2a, 12); it is the
// job's own record of its role.
const envAgentRole = "LOOM_FLOW_AGENT"

// maxBrief bounds a brief and an answer: a job's argv is capped at 64 KiB.
const maxBrief = 48 << 10

// agentStatuses are the statuses a report can end with.
var agentStatuses = []string{"DONE", "DONE_WITH_CONCERNS", "NEEDS_CONTEXT", "BLOCKED"}

// agentCommand is the argv of an agent job (spec §5.4): a first run when
// session is "", else a resume of it with prompt as the answer. settings is
// agentSettings' JSON. maxTurns, when positive, is passed as --max-turns:
// `--agent` doesn't apply a definition's maxTurns (part 3b's probe,
// 2026-11-06), so each job carries it.
func agentCommand(program, role, effort string, maxTurns int, mode, root, settings, prompt, session string) []string {
	argv := []string{program, "-p", prompt, "--agent", "flow:" + role,
		"--plugin-dir", filepath.Join(root, "plugin"), "--settings", settings}
	if session != "" {
		argv = append(argv, "--resume", session)
	}
	if maxTurns > 0 {
		argv = append(argv, "--max-turns", strconv.Itoa(maxTurns))
	}
	if mode != "" && mode != "default" {
		argv = append(argv, "--permission-mode", mode)
	}
	if effort != "" {
		argv = append(argv, "--effort", effort)
	}
	return append(argv, "--output-format", "stream-json", "--verbose")
}

// agentSettings is the --settings an agent job runs with: loom's git guard
// on Bash, Write and Edit, and every plugin in off turned off (spec §2a, 11).
func agentSettings(guard string, off map[string]bool) (string, error) {
	doc := map[string]any{"hooks": map[string]any{"PreToolUse": []any{map[string]any{
		"matcher": "Bash|Write|Edit",
		"hooks":   []any{map[string]any{"type": "command", "command": guard, "timeout": 10}},
	}}}}
	if len(off) > 0 {
		doc["enabledPlugins"] = off
	}
	b, err := json.Marshal(doc)
	return string(b), err
}

// pluginsOff is every plugin the account's settings (configDir's, else
// ~/.claude's) and the project's (cwd's .claude/settings.json and
// settings.local.json) enable, each mapped to false (spec §2a, 11). A
// settings file that exists but can't be read or decoded is an error: an
// agent never runs with plugins it was meant not to have.
func pluginsOff(configDir, cwd string) (map[string]bool, error) {
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("the account's settings: %w", err)
		}
		configDir = filepath.Join(home, ".claude")
	}
	off := map[string]bool{}
	for _, p := range []string{
		filepath.Join(configDir, "settings.json"),
		filepath.Join(cwd, ".claude", "settings.json"),
		filepath.Join(cwd, ".claude", "settings.local.json"),
	} {
		b, err := os.ReadFile(p)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		var s struct {
			EnabledPlugins map[string]bool `json:"enabledPlugins"`
		}
		if err := json.Unmarshal(b, &s); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		for name, on := range s.EnabledPlugins {
			if on {
				off[name] = false
			}
		}
	}
	return off, nil
}

// scrubbedVar reports whether an agent job drops the variable name from
// its submitter's environment (spec §2a, 4): the parent Claude session's
// own, which tie a process to that session (its messaging bridge, its mod
// link, its effort).
func scrubbedVar(name string) bool {
	switch name {
	case "CLAUDECODE", "AI_AGENT", "CLAUDE_PID", "CLAUDE_EFFORT", "CLAUDE_JOB_DIR", "LOOM_MOD_SOCKET",
		"CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_BRIDGE_SESSION_ID",
		"CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_MESSAGING_SOCKET", "CLAUDE_CODE_MESSAGING_TOKEN",
		"CLAUDE_CODE_SESSION_ATTENDED", "CLAUDE_CODE_EXECPATH":
		return true
	}
	return false
}

// agentEnv is an agent job's environment: env without the scrubbed
// variables, with LOOM_FLOW_AGENT set to role.
func agentEnv(env []string, role string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if !scrubbedVar(name) && name != envAgentRole {
			out = append(out, kv)
		}
	}
	return append(out, envAgentRole+"="+role)
}

// agentProgram is the Claude CLI agent jobs run: the first word of the
// global config's program, as sessions launch it, else "claude".
func agentProgram() string {
	if words := strings.Fields(claudeProgram()); len(words) > 0 {
		return words[0]
	}
	return "claude"
}

// agentPermissionMode is the global config's Claude permission mode (plan
// amendment 8).
func agentPermissionMode() string {
	dir, err := config.GetGlobalConfigDir()
	if err != nil {
		return "default"
	}
	return config.LoadConfigFrom(dir).Snapshot().PermissionMode()
}

// agentGuard is the guard hook's command: this loom binary's `flow guard`.
func agentGuard() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if strings.Contains(exe, "'") {
		return "", fmt.Errorf("the loom binary's path %s holds a quote", exe)
	}
	return "'" + exe + "' flow guard", nil
}

// agentDefinition is role's definition in root's flow plugin, or false.
func agentDefinition(root, role string) ([]byte, bool) {
	if !flowName.MatchString(role) {
		return nil, false
	}
	b, err := os.ReadFile(filepath.Join(root, "plugin", "agents", role+".md"))
	return b, err == nil
}

// preloadedSkills is what a first prompt carries for each skill role's
// definition preloads (spec §2a, 10): `--agent`'s main thread doesn't
// preload them as a subagent would. A skill outside the flow plugin is an
// error.
func preloadedSkills(root, role string, definition []byte) (string, error) {
	var out strings.Builder
	for _, name := range frontmatterSkills(definition) {
		short, ok := strings.CutPrefix(name, "flow:")
		if !ok || !flowName.MatchString(short) {
			return "", fmt.Errorf("flow:%s preloads %s, which is not in the flow plugin", role, name)
		}
		body, err := os.ReadFile(filepath.Join(root, "plugin", "skills", short, "SKILL.md"))
		if err != nil {
			return "", fmt.Errorf("flow:%s preloads %s: %w", role, name, err)
		}
		fmt.Fprintf(&out, "Your preloaded skill %s:\n\n%s\n\n", name, strings.TrimSpace(withoutFrontmatter(body)))
	}
	return out.String(), nil
}

// frontmatterSkills is the `skills:` list of a definition's frontmatter, in
// either form: `skills: [a, b]` or a block of `  - a` lines.
func frontmatterSkills(definition []byte) []string {
	front, ok := frontmatter(definition)
	if !ok {
		return nil
	}
	var skills []string
	block := false
	for _, line := range strings.Split(front, "\n") {
		if block {
			item, ok := strings.CutPrefix(strings.TrimSpace(line), "- ")
			if ok && strings.HasPrefix(line, " ") {
				skills = append(skills, strings.Trim(strings.TrimSpace(item), `"'`))
				continue
			}
			block = false
		}
		value, ok := strings.CutPrefix(line, "skills:")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if inline, ok := strings.CutPrefix(value, "["); ok {
			for _, item := range strings.Split(strings.TrimSuffix(inline, "]"), ",") {
				if item = strings.Trim(strings.TrimSpace(item), `"'`); item != "" {
					skills = append(skills, item)
				}
			}
			continue
		}
		block = value == ""
	}
	return skills
}

// frontmatterMaxTurns is a definition's `maxTurns:` value, 0 when it has none.
func frontmatterMaxTurns(definition []byte) int {
	front, ok := frontmatter(definition)
	if !ok {
		return 0
	}
	for _, line := range strings.Split(front, "\n") {
		if v, ok := strings.CutPrefix(line, "maxTurns:"); ok {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err == nil && n > 0 {
				return n
			}
		}
	}
	return 0
}

// agentArgv is the argv of a first agent job of role with brief, run in cwd
// from the flow root root (spec §5.4), or an *ExitError saying why not: an
// unknown role, an empty or oversized brief, a preloaded skill or the
// settings it can't read.
func agentArgv(root, role, effort, cwd string, brief []byte) ([]string, error) {
	definition, ok := agentDefinition(root, role)
	if !ok {
		return nil, &ExitError{Code: exitUsage, Msg: fmt.Sprintf("loom flow agent: the flow plugin has no agent %s", role)}
	}
	switch {
	case strings.TrimSpace(string(brief)) == "":
		return nil, &ExitError{Code: exitUsage, Msg: "loom flow agent: the brief is empty; give it on stdin"}
	case len(brief) > maxBrief:
		return nil, &ExitError{Code: exitUsage, Msg: "loom flow agent: the brief is at most 48 KiB"}
	}
	skills, err := preloadedSkills(root, role, definition)
	if err != nil {
		return nil, &ExitError{Code: exitNotRun, Msg: "loom flow agent: " + err.Error()}
	}
	settings, err := runSettings(cwd)
	if err != nil {
		return nil, err
	}
	return agentCommand(agentProgram(), role, effort, frontmatterMaxTurns(definition), agentPermissionMode(), root, settings,
		rolePreamble(role)+"\n\n"+skills+string(brief), ""), nil
}

// frontmatter is the text between a file's opening `---` line and the next.
func frontmatter(b []byte) (string, bool) {
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		return "", false
	}
	front, _, ok := strings.Cut(rest, "\n---")
	return front, ok
}

// withoutFrontmatter is a file's text past its frontmatter.
func withoutFrontmatter(b []byte) string {
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		return text
	}
	if _, body, ok := strings.Cut(rest, "\n---"); ok {
		_, body, _ = strings.Cut(body, "\n")
		return body
	}
	return text
}

// statusLines is a report's last STATUS line's value, markdown's wrapper
// taken off (spec §2a, 9; flowrun.TagLine), or, when it has none, its last
// line that is a status word alone; and for NEEDS_CONTEXT its last QUESTION
// line, wherever it stands. Real runs wrote the question before the status
// line, and once a bare NEEDS_CONTEXT (2026-11-06), and lost the question.
func statusLines(report string) (status, question string) {
	bare := ""
	for _, line := range strings.Split(report, "\n") {
		if v, ok := flowrun.TagLine(line, "STATUS:"); ok {
			status = v
		} else if v, ok := flowrun.TagLine(line, "QUESTION:"); ok {
			question = v
		} else if word := strings.Trim(strings.TrimSpace(line), "*`"); slices.Contains(agentStatuses, word) {
			bare = word
		}
	}
	if status == "" {
		status = bare
	}
	if status != "NEEDS_CONTEXT" {
		question = ""
	}
	return status, question
}

// agentResult is what an agent job's stream-json log says when it ended.
type agentResult struct {
	Session  string
	Status   string
	Report   string
	Question string
	CostUSD  float64
	Turns    int
}

// readResult reads an ended agent job's stream-json log: the session from
// its init event, and the report, cost and turns from its result event. An
// error says why the job left no usable report; what was read stays in res.
func readResult(log []byte) (agentResult, error) {
	var res agentResult
	var final []byte
	sc := bufio.NewScanner(bytes.NewReader(log))
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var ev struct {
			Type      string `json:"type"`
			Subtype   string `json:"subtype"`
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch {
		case ev.Type == "system" && ev.Subtype == "init":
			res.Session = ev.SessionID
		case ev.Type == "result":
			final = slices.Clone(sc.Bytes())
			if ev.SessionID != "" {
				res.Session = ev.SessionID
			}
		}
	}
	if err := sc.Err(); err != nil {
		return res, fmt.Errorf("its output can't be read: %w", err)
	}
	if final == nil {
		return res, errors.New("it ended with no result")
	}
	var result struct {
		Subtype string  `json:"subtype"`
		IsError bool    `json:"is_error"`
		Cost    float64 `json:"total_cost_usd"`
		Turns   int     `json:"num_turns"`
		Result  string  `json:"result"`
	}
	if err := json.Unmarshal(final, &result); err != nil {
		return res, fmt.Errorf("its result can't be read: %w", err)
	}
	res.CostUSD, res.Turns = result.Cost, result.Turns
	// A turn cap ends the job with an error result and no report, but its
	// session resumes (part 3b's probe, 2026-11-06).
	if result.Subtype == "error_max_turns" {
		res.Status = agentTurnCap
		return res, nil
	}
	if result.IsError {
		return res, errors.New("it ended with an error result")
	}
	status, question := statusLines(result.Result)
	switch {
	case status == "":
		return res, errors.New("its report has no STATUS line")
	case !slices.Contains(agentStatuses, status):
		return res, fmt.Errorf("its report has status %q", status)
	case status == "NEEDS_CONTEXT" && question == "":
		return res, errors.New("a question was asked but not given")
	}
	res.Status, res.Report, res.Question = status, result.Result, question
	return res, nil
}

// flowRootOfSession is the launch's flow root, refused outside a session.
func flowRootOfSession() (string, error) {
	root := os.Getenv(flow.EnvRoot)
	switch {
	case root == "":
		return "", &ExitError{Code: exitNotRun, Msg: "loom flow agent: " + flow.EnvRoot + " is unset; run it inside a session loom launched"}
	case !flow.IsRoot(root):
		return "", &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow agent: %s=%s is not a flow root", flow.EnvRoot, root)}
	}
	return root, nil
}
