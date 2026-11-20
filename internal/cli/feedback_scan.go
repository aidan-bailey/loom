package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/feedback"
	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/spf13/cobra"
)

// scanMinTitle is the fewest characters a session title needs to be a term:
// a shorter one matches ordinary words.
const scanMinTitle = 4

// scanGlobalLabel is the workspace a note from the global context names
// (core's Workspace.Label): it names no workspace, so it is no term.
const scanGlobalLabel = "global"

// scanInputs is what `loom feedback scan` found out about the host: the
// pure input of buildScanTerms.
type scanInputs struct {
	Home, User, Host, Email string
	// Cwd is the command's working directory.
	Cwd string
	// Workspaces are the workspaces the daemon serves.
	Workspaces []scanWorkspace
	// Accounts are the registered accounts, the implicit default included.
	Accounts []scanAccount
	// Notes are the notes of the thread `--thread` names, none without it.
	Notes []feedback.Note
}

// scanWorkspace is a served workspace and the titles of its sessions.
type scanWorkspace struct {
	Name, RepoPath string
	Titles         []string
}

// scanAccount is an account's name and the email it is logged in as, ""
// when unknown.
type scanAccount struct{ Name, Email string }

// buildScanTerms is the terms feedback.Scan looks for. The own workspace
// (ownWorkspace) is public: its name, its repository's base name, its
// sessions' titles and its notes' names stay out of every list but Private,
// which holds every repository's path, since a path inside the own
// repository carries the home directory (spec §3.1 writes it repo-relative).
func buildScanTerms(in scanInputs) feedback.ScanTerms {
	var t feedback.ScanTerms
	t.Private = appendUnique(t.Private, in.Home, config.CanonicalPath(in.Home))
	own := ownWorkspace(in)
	for i, w := range in.Workspaces {
		if w.RepoPath != "" {
			t.Private = appendUnique(t.Private, w.RepoPath, config.CanonicalPath(w.RepoPath))
		}
		if i == own {
			continue
		}
		t.Workspaces = appendUnique(t.Workspaces, w.Name)
		if b := filepath.Base(w.RepoPath); w.RepoPath != "" && b != "." && b != string(filepath.Separator) {
			t.Workspaces = appendUnique(t.Workspaces, b)
		}
		for _, title := range w.Titles {
			if longEnough(title) {
				t.Sessions = appendUnique(t.Sessions, strings.TrimSpace(title))
			}
		}
	}

	t.Identity = appendUnique(t.Identity, in.User, in.Host)
	if short, _, ok := strings.Cut(in.Host, "."); ok {
		t.Identity = appendUnique(t.Identity, short)
	}
	t.Identity = appendUnique(t.Identity, in.Email)
	for _, a := range in.Accounts {
		if a.Name != account.DefaultName {
			t.Identity = appendUnique(t.Identity, a.Name)
		}
		t.Identity = appendUnique(t.Identity, a.Email)
	}

	ownName := ""
	if own >= 0 {
		ownName = in.Workspaces[own].Name
	}
	for _, n := range in.Notes {
		t.Thread = appendUnique(t.Thread, n.From.ClaudeSession, n.From.Transcript)
		if n.From.Branch != "main" && n.From.Branch != "master" {
			t.Thread = appendUnique(t.Thread, n.From.Branch)
		}
		if own >= 0 && n.From.Workspace == ownName {
			continue
		}
		if longEnough(n.From.Title) {
			t.ThreadNames = appendUnique(t.ThreadNames, strings.TrimSpace(n.From.Title))
		}
		if n.From.Workspace != scanGlobalLabel {
			t.ThreadNames = appendUnique(t.ThreadNames, n.From.Workspace)
		}
	}
	return t
}

// ownWorkspace is the index of the served workspace whose repository holds
// the working directory, the deepest when repositories nest, and -1 when
// none does. The search is config.WorkspaceRegistry.FindByPath's (canonical
// paths, whole components, longest root) over a registry built from the
// served workspaces; the workspace it finds is mapped back to its index.
func ownWorkspace(in scanInputs) int {
	if in.Cwd == "" {
		return -1
	}
	var reg config.WorkspaceRegistry
	var index []int
	for i, w := range in.Workspaces {
		if w.RepoPath != "" {
			reg.Workspaces = append(reg.Workspaces, config.Workspace{Name: w.Name, Path: w.RepoPath})
			index = append(index, i)
		}
	}
	found := reg.FindByPath(in.Cwd)
	for j := range reg.Workspaces {
		if found == &reg.Workspaces[j] {
			return index[j]
		}
	}
	return -1
}

// longEnough reports whether a title has scanMinTitle characters once
// trimmed.
func longEnough(title string) bool {
	return utf8.RuneCountInString(strings.TrimSpace(title)) >= scanMinTitle
}

// appendUnique adds each non-empty s of add that list lacks.
func appendUnique(list []string, add ...string) []string {
	for _, s := range add {
		if s != "" && !slices.Contains(list, s) {
			list = append(list, s)
		}
	}
	return list
}

const (
	// scanMaxBytes bounds the draft: a larger input is refused, not cut, so
	// no tail goes unscanned.
	scanMaxBytes = 1 << 20
	// scanExitHit and scanExitError are the exit codes of a draft with a hit
	// and of any failure; a clean draft exits 0.
	scanExitHit   = 1
	scanExitError = 2
	// scanGitWait bounds `git config user.email`.
	scanGitWait = 5 * time.Second
)

func newFeedbackScanCmd() *cobra.Command {
	var thread string
	var allow []string
	cmd := &cobra.Command{
		Use:   "scan <file|-> [--thread <T>] [--allow <word>]…",
		Short: "Check a draft for text private to this host before it is posted",
		Long: "Read a draft from a file or standard input and report, as <line>:<col> <kind>: <match>, every " +
			"absolute path, credential, email, UUID and name that is private to this host: the home directory, the " +
			"user and host names, the accounts, every workspace and session but the one the current directory " +
			"belongs to, and with --thread the thread's notes' sessions, transcripts, branches and filers. " +
			"--allow drops the hits whose whole match is that word and prints them as allowed. Exit 0 with no hit, " +
			"1 with a hit, 2 on any error, a missing daemon included.",
		Args: func(c *cobra.Command, args []string) error {
			return scanUsage(cobra.ExactArgs(1)(c, args))
		},
		RunE: func(c *cobra.Command, args []string) error {
			hit, err := scanDraft(c, args[0], thread, allow)
			switch {
			case err != nil:
				return &ExitError{Code: scanExitError, Msg: err.Error()}
			case hit:
				return &ExitError{Code: scanExitHit}
			}
			return nil
		},
	}
	loomErrors(cmd)
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return scanUsage(err) })
	fl := cmd.Flags()
	fl.StringVar(&thread, "thread", "", "Also look for the names this thread's notes carry")
	fl.StringArrayVar(&allow, "allow", nil, "Drop the hits whose match is this word (repeatable)")
	return cmd
}

// scanUsage is a usage error as the exit-2 error every failure of scan is.
func scanUsage(err error) error {
	if err == nil {
		return nil
	}
	return &ExitError{Code: scanExitError, Msg: err.Error()}
}

// scanDraft scans src ("-" is standard input) and prints what it found:
// the hits, then the allowed ones. It reports whether there was a hit. The
// draft, the flags and the thread's number are read before the daemon is
// joined.
func scanDraft(c *cobra.Command, src, thread string, allow []string) (bool, error) {
	text, err := readScanInput(c.InOrStdin(), src)
	if err != nil {
		return false, err
	}
	var t feedback.ThreadID
	if thread != "" {
		if t, err = parseThread(thread); err != nil {
			return false, fmt.Errorf("--thread: %w", err)
		}
	}
	var hits, dropped []feedback.ScanHit
	err = withDaemon(c, func(d *client) error {
		in, err := gatherScanInputs(c.Context(), d, t)
		if err != nil {
			return err
		}
		hits, dropped = feedback.Scan(text, buildScanTerms(in), allow)
		return nil
	})
	if err != nil {
		return false, err
	}
	out := c.OutOrStdout()
	for _, h := range hits {
		fmt.Fprintf(out, "%d:%d %s: %s\n", h.Line, h.Col, h.Kind, h.Match)
	}
	for _, h := range dropped {
		fmt.Fprintf(out, "allowed %d:%d %s: %s\n", h.Line, h.Col, h.Kind, h.Match)
	}
	return len(hits) > 0, nil
}

// readScanInput reads the draft: the file src, or stdin for "-". More than
// scanMaxBytes is an error.
func readScanInput(stdin io.Reader, src string) (string, error) {
	r := stdin
	if src != "-" {
		f, err := os.Open(src)
		if err != nil {
			return "", err
		}
		defer f.Close()
		r = f
	}
	b, err := io.ReadAll(io.LimitReader(r, scanMaxBytes+1))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", src, err)
	}
	if len(b) > scanMaxBytes {
		return "", fmt.Errorf("%s is over %d bytes: scan a shorter draft", src, scanMaxBytes)
	}
	return string(b), nil
}

// gatherScanInputs asks the host and the daemon what is private: the home
// directory, $USER (else the account's user name), the host name, git's
// user.email, the served workspaces with their sessions' titles (and the
// titles their storage preserves unread), the
// accounts and, for a non-zero thread, its notes. Any answer it can't get
// is an error: a scan with a term missing would pass a draft it should
// not.
func gatherScanInputs(ctx context.Context, d *client, thread feedback.ThreadID) (scanInputs, error) {
	var in scanInputs
	var err error
	if in.Home, err = os.UserHomeDir(); err != nil {
		return in, fmt.Errorf("home directory: %w", err)
	}
	if in.User = os.Getenv("USER"); in.User == "" {
		u, err := user.Current()
		if err != nil {
			return in, fmt.Errorf("user name: %w", err)
		}
		in.User = u.Username
	}
	if in.Host, err = os.Hostname(); err != nil {
		return in, fmt.Errorf("host name: %w", err)
	}
	if in.Cwd, err = os.Getwd(); err != nil {
		return in, fmt.Errorf("working directory: %w", err)
	}
	if in.Email, err = gitEmail(ctx, in.Cwd); err != nil {
		return in, fmt.Errorf("git config user.email: %w", err)
	}
	for _, v := range d.Workspaces() {
		// A workspace whose saved state did not load keeps some titles only in
		// PreservedTitles: they are terms too.
		w := scanWorkspace{Name: v.Name, RepoPath: v.RepoPath, Titles: slices.Clone(v.PreservedTitles)}
		for _, iv := range d.Views(v.ID) {
			if !iv.IsWorkspaceTerminal {
				w.Titles = append(w.Titles, iv.Title)
			}
		}
		in.Workspaces = append(in.Workspaces, w)
	}
	in.Accounts = []scanAccount{{Name: account.DefaultName, Email: d.RCAuthFor(account.DefaultName).Identity.Email}}
	for _, name := range d.AccountNames().Names {
		if name != account.DefaultName {
			in.Accounts = append(in.Accounts, scanAccount{Name: name, Email: d.RCAuthFor(name).Identity.Email})
		}
	}
	if thread != 0 {
		inbox := d.Feedback().Inbox
		th, ok := inbox.Thread(thread)
		if !ok {
			return in, fmt.Errorf("thread %d doesn't exist", thread)
		}
		for _, id := range th.Notes {
			if n, ok := inbox.Note(id); ok {
				in.Notes = append(in.Notes, n)
			}
		}
	}
	return in, nil
}

// gitEmail is `git config user.email` in dir: "" when git has none set
// (exit status 1), an error when it can't be asked.
func gitEmail(ctx context.Context, dir string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, scanGitWait)
	defer cancel()
	out, err := internalexec.GitCommand(ctx, dir, "config", "user.email").Output()
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		return "", nil
	case err != nil:
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
