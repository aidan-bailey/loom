package core

import (
	"maps"
	"reflect"
	"slices"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
)

// modelView is the model's own state as a client sees it.
func (m *Model) modelView() ModelView {
	return ModelView{RCAuth: m.rcAuth, Registry: m.Registry(), Web: m.Web()}
}

// accountsView is the account state as a client sees it. AccountsView's
// methods replicate the model's own account queries, and
// TestStateViews_AnswerAsTheModel keeps the two in step.
func (m *Model) accountsView() AccountsView {
	v := AccountsView{
		DefaultAuth:        m.rcAuth,
		ClaudeProgram:      m.ClaudeProgram(),
		CredentialOverride: m.CredentialOverride(),
		RunningAsAccount:   m.RunningAsAccount(),
	}
	if m.accounts != nil {
		v.Names = AccountNames{Present: true, Default: m.accounts.Default(), Names: m.accounts.Names()}
		v.Loaded = m.accounts.LoadErr() == nil
		v.Extra = m.accounts.HasExtra()
		v.Accounts = make(map[string]account.Account, len(m.accounts.Accounts))
		for _, a := range m.accounts.Accounts {
			v.Accounts[a.Name] = a
		}
	}
	if len(m.accountAuth) > 0 {
		v.Auth = make(map[string]session.RemoteControlAuth, len(m.accountAuth))
		for k, a := range m.accountAuth {
			v.Auth[k] = a
		}
	}
	if len(m.accountSync) > 0 {
		v.Sync = make(map[string]account.SyncReport, len(m.accountSync))
		for k, rep := range m.accountSync {
			v.Sync[k] = rep.Clone()
		}
	}
	if len(m.usage) > 0 {
		v.Usage = make(map[string]account.Usage, len(m.usage))
		for k, u := range m.usage {
			v.Usage[k] = u.last.Clone()
			if u.err != nil {
				if v.UsageErr == nil {
					v.UsageErr = map[string]string{}
				}
				v.UsageErr[k] = u.err.Error()
			}
		}
	}
	return v
}

// githubView is the GitHub poll's state as a client sees it. GitHubView's
// methods replicate the model's own GitHub queries, and
// TestStateViews_AnswerAsTheModel keeps the two in step.
func (m *Model) githubView() GitHubView {
	v := GitHubView{
		Unavailable: m.ghAvailable.checked && !m.ghAvailable.ok,
		Reason:      m.ghAvailable.reason,
	}
	if len(m.ghState) > 0 {
		v.Snapshots = make(map[string]github.Snapshot, len(m.ghState))
		for repo, s := range m.ghState {
			v.Snapshots[repo] = s.Clone()
		}
	}
	if len(m.ghErrs) > 0 {
		v.Errs = make(map[string]string, len(m.ghErrs))
		for repo, err := range m.ghErrs {
			v.Errs[repo] = err.Error()
		}
	}
	if len(m.ghAliases) > 0 {
		v.Aliases = maps.Clone(m.ghAliases)
	}
	return v
}

// publishState returns a ModelChanged, an AccountsChanged, a GitHubChanged
// and a QueueChanged for each of those views that changed since the last
// publish (always on the first). The model and account views are
// compared whole; the account view is also republished after every usage
// probe round (usageGen, which deliverUsage bumps), because the TUI
// renders a usage sample's age when it refreshes, and a probe failing the
// same way again changes nothing the view holds. The GitHub view, whose
// snapshots can be large, is republished when a poll lands (ghGen, which
// deliverGH bumps: the one place its state changes).
func (m *Model) publishState() []Event {
	var events []Event
	mv := m.modelView()
	if m.publishedModel == nil || !reflect.DeepEqual(*m.publishedModel, mv) {
		m.publishedModel = &mv
		events = append(events, ModelChanged{View: mv.Clone()})
	}
	av := m.accountsView()
	if m.publishedAccounts == nil || m.usagePublishedGen != m.usageGen || !reflect.DeepEqual(*m.publishedAccounts, av) {
		m.publishedAccounts, m.usagePublishedGen = &av, m.usageGen
		events = append(events, AccountsChanged{View: av.Clone()})
	}
	if !m.ghPublished || m.ghPublishedGen != m.ghGen {
		m.ghPublished, m.ghPublishedGen = true, m.ghGen
		events = append(events, GitHubChanged{View: m.githubView()})
	}
	events = append(events, m.publishQueue()...)
	return events
}

// queuePublication is what publishQueue last published: the queue (by
// pointer, which guards a queue installed in place of the one last
// published, as SetJobRunnerForTest or a later replacement does), its
// revision, and the owners its jobs resolved to.
type queuePublication struct {
	sent   bool
	of     *queueState
	rev    uint64
	owners []jobOwner
}

// same reports whether p and o are one publication.
func (p queuePublication) same(o queuePublication) bool {
	return p.sent == o.sent && p.of == o.of && p.rev == o.rev && slices.Equal(p.owners, o.owners)
}

// publishQueue returns a QueueChanged when the queue is not the one last
// published, its revision moved since, or the owner of a job resolves to
// something else than it did, and on the first call always. The revision is
// queueState.rev, which every change to the queue, to a job or to the
// sampled usage bumps. A job's owner fields come from the model's
// workspaces and sessions, which a rename, an unregister or a kill's
// completion changes without touching the queue, so the owners are compared
// too (queueOwners): any other change that leaves the revision alone is not
// seen until something else moves it.
func (m *Model) publishQueue() []Event {
	p := queuePublication{sent: true, of: m.queue, rev: m.queueRev(), owners: m.queueOwners()}
	if m.queuePub.same(p) {
		return nil
	}
	m.queuePub = p
	return []Event{QueueChanged{View: m.queueView()}}
}

// Snapshot is the whole published state as events, in Sync's order: the
// workspace views, the model, account, GitHub and queue views, and every served
// workspace's instance views. It leaves what the next Sync diffs against
// alone: it is what a client needs when it subscribes, before the diffs.
func (m *Model) Snapshot() []Event {
	events := []Event{
		WorkspacesChanged{Views: m.Workspaces()},
		ModelChanged{View: m.modelView()},
		AccountsChanged{View: m.accountsView()},
		GitHubChanged{View: m.githubView()},
		QueueChanged{View: m.queueView()},
	}
	for _, w := range m.workspaces {
		views := make([]InstanceView, len(w.insts))
		for i, inst := range w.insts {
			views[i] = m.viewOf(inst)
		}
		events = append(events, ViewsChanged{WS: m.wsIDOf(w), Views: views})
	}
	return events
}
