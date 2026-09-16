package ui

import "fmt"

// SubagentRow is one live subagent or teammate shown on a card.
type SubagentRow struct {
	Name        string
	Description string
	Idle        bool
}

// agentSummary is the rail's count suffix, e.g. " · 3 agents (1 idle)".
// Empty when the card has no live agents.
func (d CardData) agentSummary() string {
	n := len(d.Subagents)
	if n == 0 {
		return ""
	}
	idle := 0
	for _, r := range d.Subagents {
		if r.Idle {
			idle++
		}
	}
	switch {
	case n == 1 && idle == 1:
		return " · 1 agent (idle)"
	case n == 1:
		return " · 1 agent"
	case idle == 0:
		return fmt.Sprintf(" · %d agents", n)
	default:
		return fmt.Sprintf(" · %d agents (%d idle)", n, idle)
	}
}
