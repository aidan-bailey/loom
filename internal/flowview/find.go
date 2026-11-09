// Package flowview reads a worktree's flows for the flow visualisation
// (docs/superpowers/specs/2026-11-09-flow-visualisation-design.md): its
// plans, specs in design, drafts, run states, agent and gate records, and
// the repo's run records, as plain values. It holds no model and no UI,
// writes nothing, and runs no process but git.
package flowview

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/internal/flowrun"
)

// Summary is one flow of a worktree, as the fleet chip and the Flows
// screen show it (spec §2).
type Summary struct {
	Plan       string    // the plan's path from the top level; the spec's for a flow in design
	Spec       string    // the plan's **Spec:** path, "" when it names none
	Title      string    // the spec's title, else the plan's
	Stage      string    // the furthest stage begun (StageDesign … StageRecord)
	Status     string    // the shown run's status, else StatusDesigning … StatusSupervised or flowrun.Done
	Run        string    // the shown run's id, "" for none
	Engine     int64     // the shown run's engine job, 0 for none
	Done       int       // stories done
	Total      int       // stories in the plan (the run's, once one exists)
	Drafted    int       // stories drafted, while drafting
	Asks       string    // the open question's kind, "" for none
	Phase      string    // the run's Phase, or a supervised run's or a draft's **Phase** line
	Updated    time.Time // the newest of the flow's files
	Unreadable string    // why a file of the flow can't be read, "" when every one can
}

// runFile is one run state of the state dir.
type runFile struct {
	path string
	st   *flowrun.State
	plan string // the plan it names, from the top level
	err  error
	mod  time.Time
}

// flow is everything a look gathered for one flow.
type flow struct {
	plan, planAbs string
	design        bool // a spec in design: plan is the spec
	header        planHeader
	text          string
	err           error // the plan's read error
	committed     bool  // HEAD's tree holds the plan

	spec, specAbs string
	specTitle     string
	specExists    bool
	specCommitted bool

	runs []runFile // the run states naming the plan, newest first

	supervised string // the supervisor's state file, "" for none
	supPhase   string
	supErr     error

	draftsDir   string
	draftsExist bool
	draftState  bool // D/STATE.md exists
	drafted     int
	draftPhase  string
	draftErr    error

	record string // the run's record, "" for none

	stateDir string
	profErr  error
	updated  time.Time
}

// run is f's shown run: its newest run state, nil for none.
func (f *flow) run() *runFile {
	if len(f.runs) == 0 {
		return nil
	}
	return &f.runs[0]
}

// specDesign matches a spec the brainstorming skill writes:
// specs/YYYY-MM-DD-<topic>-design.md under any directory.
var specDesign = regexp.MustCompile(`(^|/)specs/[^/]+-design\.md$`)

// leadingDate is a plan stem's date, which a record's name drops.
var leadingDate = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}-`)

// Find returns top's flows, newest first: the plans in branchFiles(top,
// base) holding the plan_join marker or a **Spec:** line, the plans the
// state dir's run states name, and the specs in design (spec §1).
// recordsDir holds the run records (<global dir>/flow/records).
func Find(top, base, recordsDir string) ([]Summary, error) {
	flows, err := gather(top, base, recordsDir)
	if err != nil {
		return nil, err
	}
	out := make([]Summary, 0, len(flows))
	for _, f := range flows {
		out = append(out, summaryOf(f, stagesOf(f)))
	}
	return out, nil
}

// summaryOf is f's summary over its stages.
func summaryOf(f *flow, stages []Stage) Summary {
	s := Summary{Plan: f.plan, Spec: f.spec, Title: f.specTitle, Stage: furthest(stages), Status: statusOf(f, stages), Updated: f.updated}
	if s.Title == "" {
		s.Title = f.header.Title
	}
	s.Total = len(f.header.storyIDs())
	if r := f.run(); r != nil && r.st != nil {
		s.Run, s.Engine, s.Phase, s.Total = r.st.ID, r.st.Engine, r.st.Phase, len(r.st.Stories)
		for _, st := range r.st.Stories {
			if st.Row.Status == "done" {
				s.Done++
			}
		}
		if r.st.Status == flowrun.Parked && r.st.Question != nil {
			s.Asks = r.st.Question.Kind
		}
	} else if f.supervised != "" {
		s.Phase = f.supPhase
	} else if f.draftsExist {
		s.Drafted, s.Phase = f.drafted, f.draftPhase
	}
	s.Unreadable = f.unreadable()
	return s
}

// unreadable is the first reason one of f's files can't be read.
func (f *flow) unreadable() string {
	var errs []error
	errs = append(errs, f.profErr, f.err)
	if r := f.run(); r != nil {
		errs = append(errs, r.err)
	}
	errs = append(errs, f.supErr, f.draftErr)
	for _, err := range errs {
		if err != nil {
			return err.Error()
		}
	}
	return ""
}

// gather reads top's flows, newest first.
func gather(top, base, recordsDir string) ([]*flow, error) {
	files, err := branchFiles(top, base)
	if err != nil {
		return nil, err
	}
	stateRel, draftsRel, profErr := profilePaths(top)
	state := ""
	if profErr == nil {
		if state, err = stateDir(top, stateRel); err != nil {
			return nil, err
		}
	}
	key, err := RepoKey(top)
	if err != nil {
		return nil, err
	}

	runs, torn := readRuns(top, state)

	byPlan := map[string]*flow{}
	var order []string
	add := func(plan string) *flow {
		if f, ok := byPlan[plan]; ok {
			return f
		}
		f := &flow{plan: plan, planAbs: filepath.Join(top, plan), stateDir: state, profErr: profErr}
		byPlan[plan] = f
		order = append(order, plan)
		return f
	}

	planTexts := map[string]string{} // every branch .md under a plans dir, for the specs-in-design check
	for _, p := range files {
		if !strings.HasSuffix(p, ".md") || specDesign.MatchString(p) {
			continue
		}
		text, err := readCapped(filepath.Join(top, p), maxPlanBytes)
		if err != nil {
			continue // deleted on the branch, or not a file: no plan
		}
		if inDir(p, "plans") {
			planTexts[p] = text
		}
		h := parsePlan(text)
		if h.isFlowPlan() {
			f := add(p)
			f.header, f.text = h, text
		}
	}
	for _, plan := range slices.Sorted(maps.Keys(runs)) {
		if _, ok := byPlan[plan]; ok {
			continue
		}
		f := add(plan)
		text, err := readCapped(f.planAbs, maxPlanBytes)
		if err != nil {
			f.err = err
			continue
		}
		f.header, f.text = parsePlan(text), text
	}

	named := map[string]bool{}
	for _, f := range byPlan {
		if f.header.Spec != "" {
			named[filepath.Clean(f.header.Spec)] = true
		}
	}
	for _, p := range files {
		if !specDesign.MatchString(p) || named[p] || mentioned(planTexts, p) || grepPlans(top, filepath.Base(p)) {
			continue
		}
		if _, err := os.Stat(filepath.Join(top, p)); err != nil {
			continue
		}
		f := add(p)
		f.design, f.spec = true, p
	}

	var paths []string
	for _, f := range byPlan {
		if !f.design {
			f.spec = cleanRel(top, f.header.Spec)
		}
		paths = append(paths, f.plan)
		if f.spec != "" {
			paths = append(paths, f.spec)
		}
	}
	committed, err := headTree(top, paths)
	if err != nil {
		return nil, err
	}

	flows := make([]*flow, 0, len(order))
	for _, plan := range order {
		f := byPlan[plan]
		f.committed = committed[f.plan]
		f.runs = runs[f.plan]
		f.readSpec(top, committed)
		f.readSupervised()
		f.readDrafts(top, draftsRel)
		f.findRecord(recordsDir, key)
		f.updated = f.newest()
		flows = append(flows, f)
	}
	for _, r := range torn {
		flows = append(flows, &flow{plan: rel(top, r.path), runs: []runFile{r}, stateDir: state, updated: r.mod})
	}
	sort.SliceStable(flows, func(i, j int) bool { return flows[i].updated.After(flows[j].updated) })
	return flows, nil
}

// readRuns reads the state dir's run states, by the plan each names, newest
// first; a run state whose plan can't even be read is torn, listed alone.
func readRuns(top, state string) (map[string][]runFile, []runFile) {
	byPlan := map[string][]runFile{}
	var torn []runFile
	if state == "" {
		return byPlan, nil
	}
	paths, _ := filepath.Glob(filepath.Join(state, "runs", "*.json"))
	for _, p := range paths {
		r := runFile{path: p}
		if info, err := os.Stat(p); err == nil {
			r.mod = info.ModTime()
		}
		var light struct {
			Plan string `json:"plan"`
		}
		b, err := os.ReadFile(p)
		if err == nil {
			err = json.Unmarshal(b, &light)
		}
		if err != nil || light.Plan == "" {
			if err == nil {
				err = errors.New(p + ": names no plan")
			}
			r.err = err
			torn = append(torn, r)
			continue
		}
		r.plan = cleanRel(top, light.Plan)
		r.st, r.err = flowrun.Load(p)
		byPlan[r.plan] = append(byPlan[r.plan], r)
	}
	for plan, rs := range byPlan {
		sort.SliceStable(rs, func(i, j int) bool {
			a, b := startOf(rs[i]), startOf(rs[j])
			if a != b {
				return a > b
			}
			return rs[i].mod.After(rs[j].mod)
		})
		byPlan[plan] = rs
	}
	return byPlan, torn
}

func startOf(r runFile) string {
	if r.st == nil {
		return ""
	}
	return r.st.Start
}

func (f *flow) readSpec(top string, committed map[string]bool) {
	if f.spec == "" {
		return
	}
	f.specAbs = filepath.Join(top, f.spec)
	f.specCommitted = committed[f.spec]
	text, err := readCapped(f.specAbs, 64<<10)
	if err != nil {
		return
	}
	f.specExists = true
	f.specTitle = parsePlan(text).Title
}

func (f *flow) readSupervised() {
	if f.stateDir == "" || f.design {
		return
	}
	p := filepath.Join(f.stateDir, "agent-reports", "supervisor-"+stemOf(f.plan)+".md")
	text, err := readCapped(p, 1<<20)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	f.supervised = p
	if err != nil {
		f.supErr = err
		return
	}
	f.supPhase = phaseOf(text)
}

func (f *flow) readDrafts(top, draftsRel string) {
	if f.design || f.err != nil {
		return
	}
	f.draftsDir = draftsDir(top, draftsRel, stemOf(f.plan))
	info, err := os.Stat(f.draftsDir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			f.draftErr = err
		}
		return
	}
	if !info.IsDir() {
		f.draftErr = errors.New(f.draftsDir + ": not a directory")
		return
	}
	f.draftsExist = true
	for _, id := range f.header.Entries {
		if _, err := os.Stat(filepath.Join(f.draftsDir, "S"+id+".md")); err == nil {
			f.drafted++
		}
	}
	text, err := readCapped(filepath.Join(f.draftsDir, "STATE.md"), 1<<20)
	switch {
	case err == nil:
		f.draftState, f.draftPhase = true, phaseOf(text)
	case !errors.Is(err, fs.ErrNotExist):
		f.draftErr = err
	}
}

// findRecord finds the plan's record: <records>/<key>/<YYYY-MM-DD>-<stem
// without its date>.md, the newest when several match.
func (f *flow) findRecord(recordsDir, key string) {
	if f.design || recordsDir == "" {
		return
	}
	s := leadingDate.ReplaceAllString(stemOf(f.plan), "")
	matches, _ := filepath.Glob(filepath.Join(recordsDir, key, "[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]-"+globEscape(s)+".md"))
	if len(matches) == 0 {
		return
	}
	slices.Sort(matches)
	f.record = matches[len(matches)-1]
}

// newest is the newest mtime of f's files.
func (f *flow) newest() time.Time {
	var t time.Time
	for _, p := range []string{f.planAbs, f.supervised, f.record, filepath.Join(f.draftsDir, "STATE.md")} {
		if p == "" {
			continue
		}
		if info, err := os.Stat(p); err == nil && info.ModTime().After(t) {
			t = info.ModTime()
		}
	}
	for _, r := range f.runs {
		if r.mod.After(t) {
			t = r.mod
		}
	}
	return t
}

// mentioned reports whether a plan's text names spec, by its path or its
// file name: the spec has gone on to a plan that is no flow's.
func mentioned(texts map[string]string, spec string) bool {
	base := filepath.Base(spec)
	for _, text := range texts {
		if strings.Contains(text, spec) || strings.Contains(text, base) {
			return true
		}
	}
	return false
}

// grepPlans reports whether a tracked file under a plans dir of top names
// word (git grep): a spec whose plan was committed before the base. A grep
// git can't run counts as a mention, so a doubt adds no flow.
func grepPlans(top, word string) bool {
	cmd := internalexec.GitCommand(context.Background(), top, "--no-optional-locks", "grep", "-q", "-F", "-e", word, "--", ":(glob)**/plans/**")
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false
	}
	return true
}

// inDir reports whether p has a directory named dir among its parents.
func inDir(p, dir string) bool {
	return slices.Contains(strings.Split(filepath.ToSlash(filepath.Dir(p)), "/"), dir)
}

// stemOf is a path's file name without its extension.
func stemOf(p string) string { return strings.TrimSuffix(filepath.Base(p), filepath.Ext(p)) }

// cleanRel is p from the top level: a relative p cleaned, an absolute one
// made relative when it lies below top; "" stays "".
func cleanRel(top, p string) string {
	if p == "" {
		return ""
	}
	if filepath.IsAbs(p) {
		return rel(top, p)
	}
	return filepath.Clean(p)
}

// rel is an absolute p from top, or p itself when it lies outside.
func rel(top, p string) string {
	if r, err := filepath.Rel(top, p); err == nil && r != ".." && !strings.HasPrefix(r, "../") {
		return r
	}
	return p
}

// globEscape escapes filepath.Match's metacharacters in s.
func globEscape(s string) string {
	var b strings.Builder
	for _, c := range s {
		if strings.ContainsRune(`*?[\`, c) {
			b.WriteByte('\\')
		}
		b.WriteRune(c)
	}
	return b.String()
}
