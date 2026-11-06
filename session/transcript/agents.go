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
