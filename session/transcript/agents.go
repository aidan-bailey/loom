package transcript

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/aidan-bailey/loom/session/subagent"
)

// metaLimit is the most a sidecar may hold; a larger one is not read.
const metaLimit = 64 << 10

// readAgent reads q's page of the subagent's file beside the conversation's
// transcript at path. The sidecar lands before the file, and Claude's
// cleanup can delete the file, so a file that does not exist is a page with
// no turns and AgentInfo.Missing set: only core's own errors survive the
// wire, and a caller could not tell "not yet written" from a failure.
func readAgent(path, conversation string, q Query) (Page, error) {
	file := filepath.Join(subagent.SubagentsDir(path), "agent-"+q.Agent+".jsonl")
	f, size, err := openFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		info := agentInfo(path, q.Agent, nil, 0)
		return Page{Conversation: conversation, Agent: &info}, nil
	}
	if err != nil {
		return Page{}, err
	}
	defer f.Close()
	page, err := read(f, size, conversation, q)
	if err != nil {
		return Page{}, fmt.Errorf("transcript %s: %w", file, err)
	}
	page.Conversation = conversation
	linkAgents(path, page.Turns)
	info := agentInfo(path, q.Agent, f, size)
	page.Agent = &info
	return page, nil
}

// agentInfo describes agent id of the conversation whose transcript is at
// path. file is the agent's open transcript of size bytes, nil when it does
// not exist. A missing or unreadable sidecar leaves Type and Description
// empty (the brief's first line stands in for Description) and State
// unknown.
func agentInfo(path, id string, file io.ReaderAt, size int64) AgentInfo {
	info := AgentInfo{ID: id, State: AgentUnknown, Missing: file == nil}
	if m, ok := readMeta(subagent.MetaPath(path, id)); ok {
		info.Type, info.Description, info.Teammate = m.AgentType, m.Description, m.IsTeammate()
		switch {
		case m.Finished == nil:
		case *m.Finished:
			info.State = AgentFinished
		default:
			info.State = AgentRunning
		}
	}
	if file != nil {
		started, brief := firstRecord(file, size)
		info.Started = started
		if info.Description == "" {
			info.Description = brief
		}
	}
	return info
}

// readMeta reads the sidecar at path: a regular file of at most metaLimit
// bytes that parses. It reports false for anything else, so a bad sidecar
// leaves its agent unlinked and the page still answers.
func readMeta(path string) (subagent.Meta, bool) {
	if path == "" {
		return subagent.Meta{}, false
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > metaLimit {
		return subagent.Meta{}, false
	}
	f, err := os.Open(path)
	if err != nil {
		return subagent.Meta{}, false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, metaLimit+1))
	if err != nil || len(data) > metaLimit {
		return subagent.Meta{}, false
	}
	m, err := subagent.ParseMeta(data)
	return m, err == nil
}

// firstRecord is the timestamp of the file's first record and the first
// line of the brief it holds, both zero when the first line is longer than
// lineLimit, unfinished or not a prompt.
func firstRecord(r io.ReaderAt, size int64) (time.Time, string) {
	line, err := bufio.NewReader(io.LimitReader(io.NewSectionReader(r, 0, size), lineLimit+1)).ReadBytes('\n')
	if err != nil {
		return time.Time{}, ""
	}
	e := decode(0, bytes.TrimSuffix(line, []byte("\n")), true)
	brief := ""
	if e.opens {
		brief = hooks.CapSummary(e.turn.Text)
	}
	return e.at, brief
}

// isAgentTool reports whether tool launches a subagent: Claude calls the
// tool Agent, older versions Task.
func isAgentTool(tool string) bool { return tool == "Agent" || tool == "Task" }

func hasAgentStep(turns []Turn) bool {
	for _, t := range turns {
		for _, it := range t.Items {
			for _, s := range it.Steps {
				if isAgentTool(s.Tool) {
					return true
				}
			}
		}
	}
	return false
}

// metaIndex is what a subagents folder's sidecars say.
type metaIndex struct {
	byAgent map[string]subagent.Meta
	// byTool maps a launching tool_use id to its agent; "" marks an id two
	// sidecars claim, which links neither.
	byTool map[string]string
}

// listMeta reads the sidecars in dir. An unreadable folder or sidecar is
// left out: its steps stay unlinked.
func listMeta(dir string) metaIndex {
	idx := metaIndex{byAgent: map[string]subagent.Meta{}, byTool: map[string]string{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return idx
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".meta.json") {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".meta.json")
		if !subagent.ValidAgentID(id) {
			continue
		}
		m, ok := readMeta(filepath.Join(dir, name))
		if !ok {
			continue
		}
		idx.byAgent[id] = m
		if m.ToolUseID == "" {
			continue
		}
		if prev, claimed := idx.byTool[m.ToolUseID]; claimed && prev != id {
			idx.byTool[m.ToolUseID] = ""
		} else {
			idx.byTool[m.ToolUseID] = id
		}
	}
	return idx
}

// linkAgents settles the Agent and Finished of the Agent and Task steps of
// turns, in place, from the sidecars of the conversation whose transcript is
// at path. The builder has set each step's Agent and Finished from its
// result; a sidecar naming the step's tool_use id overrides the agent, and
// the finished key of the step's agent's sidecar overrides Finished. The
// folder is listed once, and only when a step needs it. A nested agent's
// sidecar sits in the same folder as the conversation's.
func linkAgents(path string, turns []Turn) {
	if !hasAgentStep(turns) {
		return
	}
	idx := listMeta(subagent.SubagentsDir(path))
	for ti := range turns {
		for ii := range turns[ti].Items {
			steps := turns[ti].Items[ii].Steps
			for si := range steps {
				s := &steps[si]
				if !isAgentTool(s.Tool) {
					continue
				}
				if id := idx.byTool[s.ID]; id != "" {
					s.Agent = id
				}
				if m, ok := idx.byAgent[s.Agent]; ok && m.Finished != nil {
					s.Finished = *m.Finished
				}
			}
		}
	}
}
