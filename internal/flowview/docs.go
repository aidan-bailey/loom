package flowview

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// DocLimit is the most of a document ReadFlowDoc reads: 1 MiB.
const DocLimit = 1 << 20

// Doc is one of a flow's documents as ReadFlowDoc answers it: its path
// from the top level (or absolute, outside it), its text, and whether the
// text was cut at DocLimit.
type Doc struct {
	Path string
	Text string
	Cut  bool
}

// ReadDoc reads the document doc of plan's flow under top: DocSpec, the
// spec; DocSkeleton, the plan above its marker; DocPlan, the plan;
// DocRecord, the run's record; DocDraftsState, the drafts' STATE.md; or
// DocReport+<name>, the report of an agent run of one of the plan's runs.
// The path is built here from the flow, never taken from the caller, and
// any other doc is refused.
func ReadDoc(top, base, recordsDir, plan, doc string) (Doc, error) {
	f, err := flowOf(top, base, recordsDir, plan)
	if err != nil {
		return Doc{}, err
	}
	path, err := f.docPath(doc)
	if err != nil {
		return Doc{}, err
	}
	text, err := readCapped(path, DocLimit+1)
	if err != nil {
		return Doc{}, err
	}
	d := Doc{Path: rel(top, path), Text: text}
	if len(text) > DocLimit {
		d.Text, d.Cut = text[:DocLimit], true
	}
	if doc == DocSkeleton {
		d.Text = skeletonOf(d.Text)
	}
	return d, nil
}

// docPath is the file doc names for f.
func (f *flow) docPath(doc string) (string, error) {
	refuse := func(why string) (string, error) {
		return "", fmt.Errorf("the flow %s has no %s: %s", f.plan, doc, why)
	}
	switch {
	case doc == DocSpec:
		if !f.specExists {
			return refuse("no spec file")
		}
		return f.specAbs, nil
	case doc == DocSkeleton, doc == DocPlan:
		if f.design {
			return refuse("it has no plan yet")
		}
		return f.planAbs, nil
	case doc == DocRecord:
		if f.record == "" {
			return refuse("no record yet")
		}
		return f.record, nil
	case doc == DocDraftsState:
		if !f.draftState {
			return refuse("no drafts STATE.md")
		}
		return filepath.Join(f.draftsDir, "STATE.md"), nil
	case strings.HasPrefix(doc, DocReport):
		name := strings.TrimPrefix(doc, DocReport)
		for _, r := range f.runs {
			if r.st != nil && slices.Contains(r.st.Agents, name) {
				p := filepath.Join(f.stateDir, "agents", name, "report.md")
				if _, err := os.Stat(p); err != nil {
					return refuse("the agent run has no report")
				}
				return p, nil
			}
		}
		return refuse("no agent run of that name in its runs")
	}
	return "", fmt.Errorf("%q is no document of a flow", doc)
}
