package core

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/feedback"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/sched/advisor"
)

// feedbackDirName is the inbox's folder under the global config dir.
const feedbackDirName = "feedback"

// feedbackSettingsEvery is how often the grouper rereads the feedback
// block from config.json on its own, so a hand edit applies.
const feedbackSettingsEvery = time.Minute

// FeedbackNote is what `loom feedback add` sends: the agent's kind, title
// and text, the launch the CLI ran in ($LOOM_LAUNCH), which the model
// resolves to the filing session, and the turn's effort ($CLAUDE_EFFORT,
// which Claude Code sets in every Bash call).
type FeedbackNote struct {
	Kind   feedback.Kind
	Title  string
	Text   string
	Launch string
	Effort string
}

// FeedbackView is the inbox as published (FeedbackChanged, Core.Feedback):
// a copy, the grouper's state ("" when it runs, else why not), why the
// inbox refuses notes ("" when it doesn't), and the workspace a fix
// session starts in by default (feedback.workspace, "" when unset).
type FeedbackView struct {
	Inbox     feedback.Inbox
	Grouper   string
	Err       string
	Workspace string
}

// Clone deep-copies v.
func (v FeedbackView) Clone() FeedbackView {
	v.Inbox = v.Inbox.Clone()
	return v
}

// feedbackState is the inbox the model owns (session feedback spec §4–§5).
type feedbackState struct {
	globalDir string // the global config dir; "" until boot resolved it
	dir       string // globalDir/feedback
	inbox     feedback.Inbox
	loadErr   error // feedback.json failed to load: notes are refused
	rev       uint64

	settings    advisor.Settings // the grouper's, resolved
	settingsErr error
	workspace   string    // feedback.workspace
	readAt      time.Time // when config.json was last read

	failures   int    // failed calls in a row
	paused     string // why the grouper is paused; "" when not
	pausedWith advisor.Settings

	// program and timeout are SetFeedbackGrouperForTest's: a fake claude,
	// and a shorter call. "" and 0 keep Model.ClaudeProgram and the default.
	program string
	timeout time.Duration
}

// feedbackPublication is what publishFeedback last published: the
// inbox's revision and the grouper's state, which also reads the account
// registry and so changes without a bump.
type feedbackPublication struct {
	sent    bool
	rev     uint64
	grouper string
}

// bootFeedback loads the inbox under the global config dir. Boot calls it
// after the queue. A file that fails to load is left as it is and notes are
// refused, with a Notice saying so.
func (m *Model) bootFeedback() {
	f := m.feedback
	dir, err := config.GetGlobalConfigDir()
	if err != nil {
		log.For("core").Error("feedback.no_global_dir", "err", err)
		m.notifyErr(fmt.Errorf("feedback inbox: the global config dir could not be resolved: %w", err))
		return
	}
	f.globalDir, f.dir = dir, filepath.Join(dir, feedbackDirName)
	in, err := feedback.Load(f.dir)
	if err != nil {
		f.loadErr = err
		log.For("core").Error("feedback.load_failed", "dir", f.dir, "err", err)
		m.notifyErr(fmt.Errorf("feedback inbox: %w; notes are refused, and feedback.json is left as it is", err))
	} else {
		f.inbox = in
	}
	m.refreshFeedbackSettings(time.Now(), true)
	f.rev++
	m.maybeGroupFeedback()
}

// refreshFeedbackSettings rereads the feedback block from the global
// config.json when force is set or feedbackSettingsEvery has passed. A
// change of the grouper's settings ends a pause.
func (m *Model) refreshFeedbackSettings(now time.Time, force bool) {
	f := m.feedback
	if f.globalDir == "" || (!force && now.Sub(f.readAt) < feedbackSettingsEvery) {
		return
	}
	f.readAt = now
	var block config.FeedbackSettings
	b, err := diskConfigBlock[config.FeedbackSettings](f.globalDir, "feedback")
	if err != nil {
		err = fmt.Errorf("config.json could not be read: %w", err)
	} else if b != nil {
		block = *b
	}
	settings := advisor.Settings{}
	if err == nil {
		settings, err = feedback.Resolve(block.Grouper)
	}
	if f.paused != "" && settings != f.pausedWith {
		f.paused, f.failures = "", 0
		f.rev++
	}
	if settings != f.settings || errText(err) != errText(f.settingsErr) || block.Workspace != f.workspace {
		f.rev++
	}
	f.settings, f.settingsErr, f.workspace = settings, err, block.Workspace
}

// grouperOff is why the grouper can't be called now, "" when it can.
func (m *Model) grouperOff() string {
	f := m.feedback
	switch {
	case f.dir == "":
		return "the global config dir could not be resolved"
	case f.loadErr != nil:
		return "the inbox did not load"
	case f.settingsErr != nil:
		return f.settingsErr.Error()
	case !f.settings.Enabled:
		return "turned off (feedback.grouper.enabled)"
	case m.grouperProgram() == "":
		return "no Claude program is configured"
	case f.paused != "":
		return fmt.Sprintf("paused after %d failed calls in a row: %s", grouperPauseAfter, f.paused)
	}
	if _, err := m.accountEnv(f.settings.Account); err != nil {
		return err.Error()
	}
	return ""
}

// grouperProgram is the Claude CLI the grouper runs.
func (m *Model) grouperProgram() string {
	if p := m.feedback.program; p != "" {
		return p
	}
	return m.ClaudeProgram()
}

// feedbackView builds the published view: a copy of the inbox, the
// grouper's state and the load error.
func (m *Model) feedbackView() FeedbackView {
	f := m.feedback
	return FeedbackView{Inbox: f.inbox.Clone(), Grouper: m.grouperOff(), Err: errText(f.loadErr), Workspace: f.workspace}
}

// Feedback is the inbox as it stands now (Core.Feedback; a client's replica
// answers it from the last FeedbackChanged).
func (m *Model) Feedback() FeedbackView { return m.feedbackView() }

// publishFeedback returns a FeedbackChanged when the inbox's revision or
// the grouper's state moved since the last publish, and on the first call
// always. Every change the model makes to the inbox bumps
// feedbackState.rev.
func (m *Model) publishFeedback() []Event {
	p := feedbackPublication{sent: true, rev: m.feedback.rev, grouper: m.grouperOff()}
	if m.feedbackPub == p {
		return nil
	}
	m.feedbackPub = p
	return []Event{FeedbackChanged{View: m.feedbackView()}}
}

// saveFeedback writes feedback.json; a refused load or no folder is an
// error and writes nothing.
func (m *Model) saveFeedback() error {
	f := m.feedback
	switch {
	case f.dir == "":
		return fmt.Errorf("the global config dir could not be resolved")
	case f.loadErr != nil:
		return fmt.Errorf("feedback.json was not loaded and is left as it is: %w", f.loadErr)
	}
	if err := feedback.Save(f.dir, f.inbox); err != nil {
		return fmt.Errorf("write feedback.json: %w", err)
	}
	return nil
}
