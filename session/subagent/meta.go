package subagent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// taskKindTeammate marks an agent-team teammate in a metadata sidecar.
const taskKindTeammate = "in_process_teammate"

// Meta is the part of Claude's agent-<id>.meta.json sidecar loom reads.
// Several key sets exist across CLI versions; every field is optional.
type Meta struct {
	AgentType   string `json:"agentType"`
	Name        string `json:"name"`
	Description string `json:"description"`
	TaskKind    string `json:"taskKind"`
	// ToolUseID is the id of the tool_use block that launched the agent, in
	// its parent's transcript (the conversation's, or the parent agent's
	// own file when nested); "" where the CLI version writes none.
	ToolUseID string `json:"toolUseId"`
	// Finished is the sidecar's finished key: nil when the key is absent,
	// which says nothing about whether the agent ended.
	Finished *bool `json:"finished"`
}

// IsTeammate reports whether the sidecar describes an agent-team teammate,
// which goes idle after a turn rather than ending.
func (m Meta) IsTeammate() bool { return m.TaskKind == taskKindTeammate }

// DisplayName is the label shown for the agent: its name when it has one,
// else its agent type.
func (m Meta) DisplayName() string {
	if m.Name != "" {
		return m.Name
	}
	return m.AgentType
}

// ParseMeta decodes a metadata sidecar.
func ParseMeta(data []byte) (Meta, error) {
	var m Meta
	if err := json.Unmarshal(data, &m); err != nil {
		return Meta{}, fmt.Errorf("subagent: parse meta: %w", err)
	}
	return m, nil
}

// MetaRef locates one agent's sidecar.
type MetaRef struct {
	AgentID string
	Path    string
}

// ValidAgentID reports whether id can name an agent's files: it is not
// blank, is not "." or "..", and holds no path separator, so
// "agent-"+id+".jsonl" stays inside the subagents folder. Agent IDs come
// from hook payloads, transcripts and HTTP queries; check one with this
// before it joins a path.
func ValidAgentID(id string) bool {
	return strings.TrimSpace(id) != "" && id != "." && id != ".." &&
		!strings.ContainsAny(id, `/\`)
}

// SubagentsDir returns the folder Claude keeps a session's subagent files
// in (agent-<id>.jsonl and agent-<id>.meta.json): the transcript's path
// without ".jsonl", then "subagents". It returns "" for an empty path.
func SubagentsDir(transcriptPath string) string {
	if transcriptPath == "" {
		return ""
	}
	return filepath.Join(strings.TrimSuffix(transcriptPath, ".jsonl"), "subagents")
}

// MetaPath returns where Claude writes agentID's sidecar for the session
// whose transcript is at transcriptPath. It returns "" when either is
// empty, or when agentID could escape the subagents folder.
func MetaPath(transcriptPath, agentID string) string {
	dir := SubagentsDir(transcriptPath)
	if dir == "" || !ValidAgentID(agentID) {
		return ""
	}
	return filepath.Join(dir, "agent-"+agentID+".meta.json")
}
