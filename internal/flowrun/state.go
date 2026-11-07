// Package flowrun is flow in loom part 3b's run engine: it drives a plan's
// stories through headless flow agents and gate runs, one step at a time,
// and parks on every judgement for the manager. It replays: every step has a
// deterministic name and a memoized result, so a restarted engine runs the
// procedure again and only the first unfinished step does anything. Every
// side effect goes through Effects.
package flowrun

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/aidan-bailey/loom/config"
)

// Schema is the run state's version.
const Schema = 1

// A run's statuses.
const (
	Running   = "running" // an engine works on it, or is to
	Parked    = "parked"  // an open question waits for the manager
	Done      = "done"
	Stopped   = "stopped"   // the manager answered stop
	Cancelled = "cancelled" // loom flow run cancel
)

// Ended reports whether status is a run's end.
func Ended(status string) bool { return status == Done || status == Stopped || status == Cancelled }

// State is a run's state, <state>/runs/<id>.json (spec §4).
type State struct {
	Schema      int                        `json:"schema"`
	ID          string                     `json:"id"`
	Status      string                     `json:"status"`
	Plan        string                     `json:"plan"`
	Spec        string                     `json:"spec"`
	Base        string                     `json:"base"`
	Start       string                     `json:"start"` // UTC, RFC 3339
	End         string                     `json:"end,omitempty"`
	Roster      string                     `json:"roster"`
	Profile     string                     `json:"profile"`
	DesignStage string                     `json:"design_stage,omitempty"`
	RecordCopy  string                     `json:"record_copy,omitempty"` // the profile's in-repo record dir, "" for none
	Managers    []string                   `json:"managers"`
	Blocks      Blocks                     `json:"blocks"`
	Stories     []Story                    `json:"stories"`
	Engine      int64                      `json:"engine,omitempty"` // the latest engine job
	Agents      []string                   `json:"agents"`           // every agent run, in dispatch order
	Gates       []string                   `json:"gates"`            // every gate run, in start order
	Memo        map[string]json.RawMessage `json:"memo"`
	Question    *Question                  `json:"question,omitempty"`
	Answers     []Answer                   `json:"answers,omitempty"`
	Record      string                     `json:"record,omitempty"` // the record's path, once written
	Why         string                     `json:"why,omitempty"`
	// Rebuilt on every pass (planning amendment 2).
	Phase string    `json:"phase,omitempty"`
	Notes []string  `json:"notes,omitempty"`
	Kept  []Finding `json:"kept,omitempty"` // Minors kept for the final round
}

// Blocks are the profile's role blocks as `loom flow conf show --for
// supervisor` prints them, and its services.
type Blocks struct {
	Developer  string   `json:"developer"`
	Verifier   string   `json:"verifier"`
	Reviewer   string   `json:"reviewer"`
	Supervisor string   `json:"supervisor"`
	Services   []string `json:"services,omitempty"`
}

// Story is a plan's story as check_plan.py --json gives it, and its row.
type Story struct {
	ID         string   `json:"id"`
	Line       int      `json:"line"`
	End        int      `json:"end"`
	Developer  string   `json:"developer"`
	Tier       string   `json:"tier"`
	Review     string   `json:"review"`
	Landmines  []string `json:"landmines,omitempty"`
	Interfaces string   `json:"interfaces,omitempty"`
	Verify     string   `json:"verify,omitempty"`
	// BaseLine and BaseEnd are its range in the plan at the base, kept once
	// Restory has moved Line and End: premise flags are numbered by it.
	BaseLine int `json:"base_line,omitempty"`
	BaseEnd  int `json:"base_end,omitempty"`
	Row      Row `json:"row"`
}

// baseRange is s's range in the plan at the base.
func (s *Story) baseRange() (int, int) {
	if s.BaseLine > 0 {
		return s.BaseLine, s.BaseEnd
	}
	return s.Line, s.End
}

// Row is what the run made of a story, for show and the record.
type Row struct {
	Status      string   `json:"status,omitempty"` // developing, gating, reviewing, fixing, done
	Developer   string   `json:"developer,omitempty"`
	Base        string   `json:"base,omitempty"`
	Head        string   `json:"head,omitempty"`
	Commits     int      `json:"commits,omitempty"`
	Deviations  int      `json:"deviations,omitempty"`
	UpATier     int      `json:"up_a_tier,omitempty"`
	Escalated   int      `json:"escalated,omitempty"`
	Findings    [3]int   `json:"findings"` // Critical, Important, Minor of its first review
	TurnCap     bool     `json:"turn_cap,omitempty"`
	Corrections []string `json:"corrections,omitempty"`
}

// Question is a parked run's open question (spec §5.4).
type Question struct {
	Key   string   `json:"key"`
	Kind  string   `json:"kind"`
	Story string   `json:"story,omitempty"`
	Text  string   `json:"text"`
	Paths []string `json:"paths,omitempty"`
	// At is the head the engine last recorded when it parked on the
	// question, which loom flow run answer compares HEAD with.
	At string `json:"at,omitempty"`
}

// Where an answer came from (loom flow run answer --from): FromSpec when
// the manager settled it without the owner (from the spec, the plan or the
// code), FromOwner when it asked the owner. The record counts them, so a
// run says how many questions a judge could have answered (part 3c's
// evidence).
const (
	FromSpec  = "spec"
	FromOwner = "owner"
)

// Answer is an answered question, with the session that answered it and
// where the answer came from.
type Answer struct {
	Question
	Answer  string `json:"answer"`
	Session string `json:"session,omitempty"`
	From    string `json:"from,omitempty"`
	// Head is the HEAD the run took with the answer: the manager's own
	// commits to the run's spec and plan since At, or "".
	Head string `json:"head,omitempty"`
}

// Finding is one FINDING line (spec §5.6).
type Finding struct {
	Severity string `json:"severity"` // Critical, Important or Minor
	Design   bool   `json:"design,omitempty"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Fix      string `json:"fix"`
}

func (f Finding) String() string {
	d := ""
	if f.Design {
		d = " (design)"
	}
	return fmt.Sprintf("%s%s %s:%d %s", f.Severity, d, f.File, f.Line, f.Fix)
}

// Load reads a run state, refusing one it can't decode or of another schema.
func Load(path string) (*State, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if st.Schema != Schema {
		return nil, fmt.Errorf("%s: schema %d, and this loom reads %d", path, st.Schema, Schema)
	}
	return &st, nil
}

// Save writes st whole at path (a temp file renamed).
func Save(path string, st *State) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return config.AtomicWriteFile(path, append(b, '\n'), 0o600)
}

// Answer records text as the answer to st's open question, from session,
// with where it came from (FromSpec or FromOwner) and the HEAD the run
// takes with it (Answer.Head, or ""), and sets st running; CheckAnswer has
// passed it.
func (st *State) Answer(text, session, from, head string) error {
	if st.Status != Parked || st.Question == nil {
		return errors.New("the run has no open question")
	}
	if from != FromSpec && from != FromOwner {
		return fmt.Errorf("an answer comes from %q or %q, not %q", FromSpec, FromOwner, from)
	}
	text = strings.TrimSpace(text)
	raw, err := json.Marshal(text)
	if err != nil {
		return err
	}
	if st.Memo == nil {
		st.Memo = map[string]json.RawMessage{}
	}
	st.Memo["q:"+st.Question.Key] = raw
	if head != "" {
		took, err := json.Marshal(head)
		if err != nil {
			return err
		}
		st.Memo["took:"+st.Question.Key] = took
	}
	st.Answers = append(st.Answers, Answer{Question: *st.Question, Answer: text, Session: session, From: from, Head: head})
	if session != "" && !slices.Contains(st.Managers, session) {
		st.Managers = append(st.Managers, session)
	}
	st.Question, st.Status = nil, Running
	return nil
}

// Restory takes a parked run's stories from check-plan's fresh reading of
// an edited plan (feedback thread 14): by story ID, each story's range and
// the fields only its briefs read (Verify, Interfaces, Landmines). Its
// developer, tier and review stay as the run started, since they choose
// the roles a replay follows; kept says where the plan now differs. It
// refuses, changing nothing, when the plan's stories aren't the run's in
// the same order. Rows and the memo are left as they are.
func (st *State) Restory(fresh []Story) (kept []string, err error) {
	ids := func(ss []Story) string {
		var out []string
		for _, s := range ss {
			out = append(out, s.ID)
		}
		return strings.Join(out, ", ")
	}
	if !slices.EqualFunc(st.Stories, fresh, func(a, b Story) bool { return a.ID == b.ID }) {
		return nil, fmt.Errorf("the plan's stories are now %s, and the run's are stories %s: a run can't add, remove or reorder its stories",
			strings.TrimSpace("stories "+ids(fresh)), ids(st.Stories))
	}
	for i := range st.Stories {
		s, f := &st.Stories[i], fresh[i]
		if s.BaseLine == 0 {
			s.BaseLine, s.BaseEnd = s.Line, s.End
		}
		s.Line, s.End, s.Verify, s.Interfaces, s.Landmines = f.Line, f.End, f.Verify, f.Interfaces, f.Landmines
		if f.Developer != s.Developer || f.Tier != s.Tier || f.Review != s.Review {
			kept = append(kept, fmt.Sprintf("Story %s's developer, tier and review stay %s, %s and %s (the plan now says %s, %s and %s)",
				s.ID, s.Developer, s.Tier, s.Review, f.Developer, f.Tier, f.Review))
		}
	}
	return kept, nil
}

// unmarshal reports whether raw decodes into v.
func unmarshal(raw json.RawMessage, v any) bool { return json.Unmarshal(raw, v) == nil }
