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

// MetaPath returns where Claude writes agentID's sidecar for the session
// whose transcript is at transcriptPath. It returns "" when either is
// empty, or when agentID could escape the subagents folder.
func MetaPath(transcriptPath, agentID string) string {
	if transcriptPath == "" || agentID == "" || agentID == ".." ||
		strings.ContainsAny(agentID, `/\`) {
		return ""
	}
	sessionDir := strings.TrimSuffix(transcriptPath, ".jsonl")
	return filepath.Join(sessionDir, "subagents", "agent-"+agentID+".meta.json")
}
