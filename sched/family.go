package sched

import (
	"cmp"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
)

// Family is what a job shares with its near relatives (family sizing spec
// §4): its repo, its folder and the sorted set of its steps, each a command
// reduced to what decides its size.
type Family struct {
	Repo   string
	Folder string
	Steps  []string
}

// Key is f as one comparable string: the JSON array [repo, folder, steps].
func (f Family) Key() string {
	data, err := json.Marshal([]any{f.Repo, f.Folder, f.Steps})
	if err != nil {
		panic(err) // strings and a slice of them: Marshal cannot fail
	}
	return string(data)
}

// Label is f's steps joined by " && ", as the display names a family.
func (f Family) Label() string { return strings.Join(f.Steps, " && ") }

// outputOnly are the programs whose step a family drops: they shape a
// command's output and size nothing.
var outputOnly = map[string]bool{
	"tail": true, "head": true, "grep": true, "sed": true, "wc": true, "sort": true,
	"cat": true, "echo": true, "true": true, "export": true, "set": true,
}

// FamilyOf is sig's family (family sizing spec §5.1), a pure function of
// its repo, folder and command: the environment and the prerequisite are
// not part of it. It is false when sig has none: a shell script the lexer
// doesn't read, or no step left but cd.
func FamilyOf(sig Signature) (Family, bool) {
	cmds, ok := commandsOf(sig.Command)
	if !ok {
		return Family{}, false
	}
	var steps []string
	real := false
	for _, words := range cmds {
		if step, ok := stepOf(words); ok {
			steps = append(steps, step)
			real = real || !isCdStep(step)
		}
	}
	if !real {
		return Family{}, false
	}
	slices.Sort(steps)
	return Family{Repo: sig.Repo, Folder: sig.Folder, Steps: slices.Compact(steps)}, true
}

// commandsOf is command's simple commands: a shell's -c script split by
// splitScript, or command itself.
func commandsOf(command []string) ([][]string, bool) {
	if len(command) >= 3 && isShell(command[0]) && isDashC(command[1]) {
		cmds, err := splitScript(command[2])
		return cmds, err == nil
	}
	return [][]string{command}, len(command) > 0
}

func isShell(w string) bool {
	switch filepath.Base(w) {
	case "sh", "bash", "dash", "zsh":
		return true
	}
	return false
}

// isDashC reports whether a is -c or a one-dash cluster of letters ending
// in c (-ec, -lc, -euc).
func isDashC(a string) bool {
	if len(a) < 2 || a[0] != '-' || a[len(a)-1] != 'c' {
		return false
	}
	for _, r := range a[1:] {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

func isCdStep(step string) bool { return step == "cd" || strings.HasPrefix(step, "cd ") }

// stepOf is the step of one simple command (spec §5.1), false for a step
// the family drops.
func stepOf(words []string) (string, bool) {
	i := 0
	for i < len(words) && isAssignment(words[i]) {
		i++
	}
	command, _ := Normalize(words[i:])
	if len(command) == 0 || outputOnly[command[0]] {
		return "", false
	}
	switch prog, args := command[0], command[1:]; prog {
	case "cd":
		if len(args) == 0 {
			return "cd", true
		}
		return "cd " + args[0], true
	case "cargo":
		return cargoStep(args), true
	case "go":
		return goStep(args), true
	default:
		return otherStep(prog, args), true
	}
}

// cargoStep is a cargo command's step: its subcommand (nextest with the
// word after it), --workspace, --doc and --release, and its crates.
func cargoStep(args []string) string {
	sub, rest := "", []string(nil)
	for i, a := range args {
		if a == "--" {
			break
		}
		if !strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "+") {
			sub, rest = a, args[i+1:]
			break
		}
	}
	if sub == "nextest" && len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		sub, rest = sub+" "+rest[0], rest[1:]
	}
	var workspace, doc, release bool
	var crates []string
	for i := 0; i < len(rest); i++ {
		switch a := rest[i]; {
		case a == "--":
			i = len(rest)
		case a == "--workspace" || a == "--all":
			workspace = true
		case a == "--doc":
			doc = true
		case a == "--release":
			release = true
		case (a == "-p" || a == "--package") && i+1 < len(rest):
			crates = append(crates, rest[i+1])
			i++
		case strings.HasPrefix(a, "--package="):
			crates = append(crates, strings.TrimPrefix(a, "--package="))
		case strings.HasPrefix(a, "-p") && len(a) > 2:
			crates = append(crates, a[2:])
		}
	}
	parts := []string{"cargo"}
	if sub != "" {
		parts = append(parts, sub)
	}
	for _, f := range []struct {
		on   bool
		flag string
	}{{workspace, "--workspace"}, {doc, "--doc"}, {release, "--release"}} {
		if f.on {
			parts = append(parts, f.flag)
		}
	}
	slices.Sort(crates)
	for _, c := range slices.Compact(crates) {
		parts = append(parts, "-p", c)
	}
	return strings.Join(parts, " ")
}

// goStep is a go command's step: its subcommand, -race, -tags and its
// package patterns, up to -args.
func goStep(args []string) string {
	sub, rest := "", []string(nil)
	for i, a := range args {
		if !strings.HasPrefix(a, "-") {
			sub, rest = a, args[i+1:]
			break
		}
	}
	race, tags := false, ""
	var pkgs []string
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		flag := strings.TrimLeft(a, "-")
		dashed := flag != a
		switch {
		case dashed && flag == "args":
			i = len(rest)
		case dashed && flag == "race":
			race = true
		case dashed && flag == "tags" && i+1 < len(rest):
			tags = rest[i+1]
			i++
		case dashed && strings.HasPrefix(flag, "tags="):
			tags = strings.TrimPrefix(flag, "tags=")
		case a == "." || strings.HasPrefix(a, "./") || strings.HasPrefix(a, "../"):
			if len(a) > 2 {
				a = strings.TrimSuffix(a, "/")
			}
			pkgs = append(pkgs, a)
		}
	}
	parts := []string{"go"}
	if sub != "" {
		parts = append(parts, sub)
	}
	if race {
		parts = append(parts, "-race")
	}
	if tags != "" {
		parts = append(parts, "-tags", tags)
	}
	slices.Sort(pkgs)
	return strings.Join(append(parts, slices.Compact(pkgs)...), " ")
}

// otherStep is any other program's step: it and its first two words that
// aren't flags.
func otherStep(prog string, args []string) string {
	parts := []string{prog}
	for _, a := range args {
		if len(parts) == 3 {
			break
		}
		if !strings.HasPrefix(a, "-") {
			parts = append(parts, a)
		}
	}
	return strings.Join(parts, " ")
}

// Families memoizes FamilyOf by signature ID (family sizing spec §5.2): an
// ID names one signature, so an entry never goes stale, and the memo
// starts over past maxFamilyMemo entries, since beliefs come and go. The
// zero value is ready; it is not safe for concurrent use (the model's loop
// holds the one it uses).
type Families struct {
	byID map[SignatureID]familyMemo
}

// familyMemo is one signature's family: key is "" when it has none.
type familyMemo struct {
	family Family
	key    string
}

const maxFamilyMemo = 2 * MaxBeliefs

func (fs *Families) memo(id SignatureID, sig Signature) familyMemo {
	if m, ok := fs.byID[id]; ok {
		return m
	}
	if fs.byID == nil || len(fs.byID) >= maxFamilyMemo {
		fs.byID = make(map[SignatureID]familyMemo)
	}
	var m familyMemo
	if f, ok := FamilyOf(sig); ok {
		m = familyMemo{family: f, key: f.Key()}
	}
	fs.byID[id] = m
	return m
}

// memoOf is b's family memo; a belief built with no ID (a test's) is
// looked up by its signature's.
func (fs *Families) memoOf(b *Belief) familyMemo {
	id := b.ID
	if id == "" {
		id = b.Signature.ID()
	}
	return fs.memo(id, b.Signature)
}

// Of is sig's family, false for none.
func (fs *Families) Of(sig Signature) (Family, bool) {
	m := fs.memo(sig.ID(), sig)
	f := m.family
	f.Steps = slices.Clone(f.Steps)
	return f, m.key != ""
}

// Pool is sig's family pool among bs (spec §5.2): the runs of every belief
// of sig's family but sig's own, newest first (by Ended, then by job),
// cut to the window, as a belief with only its runs (their profiles and
// curves dropped: nothing reads them from a pool). It is nil when sig has
// no family or no other belief of its family has a run.
func (fs *Families) Pool(bs []Belief, sig Signature) *Belief {
	id := sig.ID()
	self := fs.memo(id, sig)
	if self.key == "" {
		return nil
	}
	var runs []Run
	for i := range bs {
		if b := &bs[i]; b.ID != id && fs.memoOf(b).key == self.key {
			runs = append(runs, b.Runs...)
		}
	}
	if len(runs) == 0 {
		return nil
	}
	slices.SortStableFunc(runs, func(x, y Run) int {
		if c := y.Ended.Compare(x.Ended); c != 0 {
			return c
		}
		return cmp.Compare(y.Job, x.Job)
	})
	runs = runs[:min(len(runs), window)]
	for i := range runs {
		runs[i].Profile, runs[i].Curve = nil, nil
	}
	return &Belief{Runs: runs}
}

// Members is how many beliefs of bs are of sig's family, sig's own
// included; 0 when sig has none.
func (fs *Families) Members(bs []Belief, sig Signature) int {
	self := fs.memo(sig.ID(), sig)
	if self.key == "" {
		return 0
	}
	n := 0
	for i := range bs {
		if fs.memoOf(&bs[i]).key == self.key {
			n++
		}
	}
	return n
}

// Kin is how a and b are related (spec §5.5): whether they are of one
// family, and how many steps but cd steps their families share; false and
// 0 when either has none.
func (fs *Families) Kin(a, b *Belief) (same bool, shared int) {
	ma, mb := fs.memoOf(a), fs.memoOf(b)
	if ma.key == "" || mb.key == "" {
		return false, 0
	}
	for _, s := range ma.family.Steps {
		if !isCdStep(s) && slices.Contains(mb.family.Steps, s) {
			shared++
		}
	}
	return ma.key == mb.key, shared
}
