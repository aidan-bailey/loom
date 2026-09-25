package app

import (
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/aidan-bailey/loom/account"
	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/log"
)

// usageInterval is the usage probes' cadence. Plan usage moves slowly and
// the views dim a sample older than two intervals (ui.UsageStaleAfter);
// the moments the user is choosing an account expedite a probe instead
// (requestUsageProbe).
const usageInterval = 2 * time.Minute

// usageTarget is one account to probe. dir is its config dir, "" for the
// default account.
type usageTarget struct{ name, dir string }

// usageReadyMsg carries one round of probes: results for the accounts that
// answered, errs for the ones that did not.
type usageReadyMsg struct {
	results map[string]account.Usage
	errs    map[string]error
}

// usageProbeCmd probes every target in parallel and returns one message.
// Each probe runs in its account's config dir (the main dir for default)
// so no project entry is recorded for an arbitrary directory.
func usageProbeCmd(program, mainDir string, targets []usageTarget, r internalexec.Executor) tea.Cmd {
	return func() tea.Msg {
		msg := usageReadyMsg{results: map[string]account.Usage{}, errs: map[string]error{}}
		var mu sync.Mutex
		var wg sync.WaitGroup
		for _, t := range targets {
			wg.Add(1)
			go func(t usageTarget) {
				defer wg.Done()
				cwd, env := mainDir, []string(nil)
				if t.dir != "" {
					cwd, env = t.dir, account.EnvFor(t.dir)
				}
				u, err := account.ProbeUsage(program, env, cwd, r)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					msg.errs[t.name] = err
				} else {
					msg.results[t.name] = u
				}
			}(t)
		}
		wg.Wait()
		return msg
	}
}

// maybeUsageProbe returns a probe round when gateUsage is due, an extra
// account exists, and a Claude CLI is configured. It reads the registry as
// the health tick last reloaded it (maybeReloadAccounts): a builder that
// returns nil leaves the gate due, so it runs again on the very next tick.
// Update goroutine only.
func (m *home) maybeUsageProbe() tea.Cmd {
	return m.dispatchGated(gateUsage, time.Now(), func() tea.Cmd {
		if !m.hasExtraAccounts() {
			return nil
		}
		program := m.claudeProgram()
		if program == "" {
			return nil
		}
		targets := []usageTarget{{name: account.DefaultName}}
		for _, a := range m.accounts.Accounts {
			targets = append(targets, usageTarget{name: a.Name, dir: a.Dir})
		}
		return usageProbeCmd(program, m.mainConfigDir(), targets, internalexec.Default{})
	})
}

// requestUsageProbe brings the next probe round forward: the user is about
// to choose an account (a picker opened) or the accounts changed.
func (m *home) requestUsageProbe() tea.Cmd {
	m.gate(gateUsage).request()
	return m.maybeUsageProbe()
}

// handleUsageReady stores a probe round. A failed probe keeps the account's
// last good sample and records the error, which the views show as a dimmed,
// aged value; usage never drives a status, so stale beats blank here.
func (m *home) handleUsageReady(msg usageReadyMsg) tea.Cmd {
	m.ensureAccountMaps()
	for name, u := range msg.results {
		m.usage[name] = accountUsage{last: u}
	}
	for name, err := range msg.errs {
		cur := m.usage[name]
		cur.err = err
		m.usage[name] = cur
		log.For("account").Debug("usage.probe_failed", "account", name, "err", err.Error())
	}
	return m.refreshAccountViews()
}
