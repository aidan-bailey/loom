package sched

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
)

// SignatureID names a signature: the first 12 hex digits of the SHA-256 of
// the JSON array [repo, folder, command, environment pairs], and the
// prerequisite's signature after them when there is one.
type SignatureID string

// Signature is what makes two jobs the same job (smart scheduler spec §5.1):
// the repository (its git common dir), the folder within it, and the command
// with its wrapper programs removed, the NAME=value pairs they carried kept
// apart and sorted; and for a dependent job, its prerequisite's signature
// (dependent jobs spec §5.6).
type Signature struct {
	Repo    string      `json:"repo"`
	Folder  string      `json:"folder"`
	Command []string    `json:"command"`
	Env     []string    `json:"env,omitempty"`
	After   SignatureID `json:"after,omitempty"` // the prerequisite's signature, AfterNone for one with none; "" for no prerequisite
}

// AfterNone is Signature.After for a job whose prerequisite has no signature.
const AfterNone SignatureID = "none"

// NewSignature is the signature of argv run in folder of repo.
func NewSignature(repo, folder string, argv []string) Signature {
	command, env := Normalize(argv)
	return Signature{Repo: repo, Folder: folder, Command: command, Env: env}
}

// ID is s's ID. No pairs and an empty list of pairs are one signature, and
// After is hashed only when it is set, so a signature with no prerequisite
// keeps the ID it always had.
func (s Signature) ID() SignatureID {
	env := s.Env
	if env == nil {
		env = []string{}
	}
	parts := []any{s.Repo, s.Folder, s.Command, env}
	if s.After != "" {
		parts = append(parts, s.After)
	}
	data, err := json.Marshal(parts)
	if err != nil {
		panic(err) // strings and slices of them: Marshal cannot fail
	}
	sum := sha256.Sum256(data)
	return SignatureID(hex.EncodeToString(sum[:])[:12])
}

// RepoName is a repo's display name: the name of the common dir's parent
// when the common dir is .git, else the common dir's own name.
func RepoName(repo string) string {
	if filepath.Base(repo) == ".git" {
		return filepath.Base(filepath.Dir(repo))
	}
	return filepath.Base(repo)
}

// Normalize splits argv into the command a signature keeps and the
// environment pairs its wrappers set (spec §5.1). It removes wrappers from
// the front while one matches: env with only NAME=value operands and one
// optional --; direnv exec DIR; nix develop … or nix shell … up to -c or
// --command; time with no option; nice with one -n N, -N or
// --adjustment=N; ionice with -c N, -n N and -t. A wrapper is known by its
// base name, and one with any other option, or nothing after it, ends the
// removal. The program word left first becomes its base name when it is an
// absolute path and is cleaned when it is a relative one. The pairs are
// sorted.
func Normalize(argv []string) (command, env []string) {
	rest := argv
	for len(rest) > 0 {
		next, pairs, ok := unwrap(rest)
		if !ok {
			break
		}
		env = append(env, pairs...)
		rest = next
	}
	command = slices.Clone(rest)
	if len(command) > 0 {
		command[0] = programWord(command[0])
	}
	slices.Sort(env)
	return command, env
}

// unwrap removes the wrapper at argv's front, returning what it runs and
// the pairs it sets; ok is false when argv[0] is no wrapper Normalize
// removes, or is one it leaves.
func unwrap(argv []string) (rest, pairs []string, ok bool) {
	switch filepath.Base(argv[0]) {
	case "env":
		return unwrapEnv(argv)
	case "direnv":
		if len(argv) > 3 && argv[1] == "exec" {
			return argv[3:], nil, true
		}
	case "nix":
		if len(argv) > 2 && (argv[1] == "develop" || argv[1] == "shell") {
			for i := 2; i < len(argv)-1; i++ {
				if argv[i] == "-c" || argv[i] == "--command" {
					return argv[i+1:], nil, true
				}
			}
		}
	case "time":
		if len(argv) > 1 && !strings.HasPrefix(argv[1], "-") {
			return argv[1:], nil, true
		}
	case "nice":
		return unwrapNice(argv)
	case "ionice":
		return unwrapIonice(argv)
	}
	return nil, nil, false
}

func unwrapEnv(argv []string) ([]string, []string, bool) {
	var pairs []string
	dashes := false
	for i := 1; i < len(argv); i++ {
		switch a := argv[i]; {
		case a == "--" && !dashes:
			dashes = true
		case isAssignment(a):
			pairs = append(pairs, a)
		default:
			if strings.HasPrefix(a, "-") {
				return nil, nil, false
			}
			return argv[i:], pairs, true
		}
	}
	return nil, nil, false
}

// isAssignment reports whether arg is env's NAME=value operand: a name of
// letters, digits and underscores that doesn't start with a digit.
func isAssignment(arg string) bool {
	name, _, ok := strings.Cut(arg, "=")
	if !ok || name == "" || (name[0] >= '0' && name[0] <= '9') {
		return false
	}
	for _, r := range name {
		if r != '_' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func unwrapNice(argv []string) ([]string, []string, bool) {
	i := 1
	switch {
	case i+1 < len(argv) && argv[i] == "-n":
		i += 2
	case i < len(argv) && (strings.HasPrefix(argv[i], "--adjustment=") || isDashNumber(argv[i])):
		i++
	}
	if i < len(argv) && !strings.HasPrefix(argv[i], "-") {
		return argv[i:], nil, true
	}
	return nil, nil, false
}

// isDashNumber reports whether arg is nice's -N: a dash and digits.
func isDashNumber(arg string) bool {
	n, ok := strings.CutPrefix(arg, "-")
	if !ok || n == "" {
		return false
	}
	for _, r := range n {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func unwrapIonice(argv []string) ([]string, []string, bool) {
	i := 1
loop:
	for i < len(argv) {
		switch {
		case (argv[i] == "-c" || argv[i] == "-n") && i+1 < len(argv):
			i += 2
		case argv[i] == "-t":
			i++
		default:
			break loop
		}
	}
	if i < len(argv) && !strings.HasPrefix(argv[i], "-") {
		return argv[i:], nil, true
	}
	return nil, nil, false
}

// programWord is the program word as a signature keeps it.
func programWord(w string) string {
	switch {
	case filepath.IsAbs(w):
		return filepath.Base(w)
	case strings.Contains(w, "/"):
		return filepath.Clean(w)
	}
	return w
}
