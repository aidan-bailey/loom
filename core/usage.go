package core

import (
	"sync"
	"time"

	"github.com/aidan-bailey/loom/account"
	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/log"
)

// usageInterval is the usage probes' cadence. Plan usage moves slowly and
// the views dim a sample older than two intervals (ui.UsageStaleAfter);
// the moments the user is choosing an account expedite a probe instead
// (RequestUsageProbe).
const usageInterval = 2 * time.Minute

// usageTarget is one account to probe. dir is its config dir, "" for the
// default account.
type usageTarget struct{ name, dir string }

// usageResult carries one round of probes: results for the accounts that
// answered, errs for the ones that did not.
type usageResult struct {
	results map[string]account.Usage
	errs    map[string]error
}

// usageProbeJob probes every target in parallel and returns one result.
// Each probe runs in its account's config dir (the main dir for default)
// so no project entry is recorded for an arbitrary directory.
func usageProbeJob(program, mainDir string, targets []usageTarget, r internalexec.Executor) Job {
	return func() any {
		msg := usageResult{results: map[string]account.Usage{}, errs: map[string]error{}}
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

// maybeUsageProbe dispatches a probe round when gateUsage is due, an extra
// account exists, and a Claude CLI is configured. It reads the registry as
// the health tick last reloaded it (maybeReloadAccounts): a builder that
// returns nil leaves the gate due, so it runs again on the very next tick.
// Update goroutine only.
func (m *Model) maybeUsageProbe() bool {
	return m.dispatchGated(gateUsage, time.Now(), func() Job {
		if !m.HasExtraAccounts() {
			return nil
		}
		program := m.ClaudeProgram()
		if program == "" {
			return nil
		}
		targets := []usageTarget{{name: account.DefaultName}}
		for _, a := range m.accounts.Accounts {
			targets = append(targets, usageTarget{name: a.Name, dir: a.Dir})
		}
		return usageProbeJob(program, m.MainConfigDir(), targets, internalexec.Default{})
	})
}

// RequestUsageProbe brings the next probe round forward: the user is about
// to choose an account (a picker opened) or the accounts changed.
func (m *Model) RequestUsageProbe() {
	m.gate(gateUsage).request()
	m.maybeUsageProbe()
}

// deliverUsage stores a probe round. A failed probe keeps the account's
// last good sample and records the error, which the views show as a dimmed,
// aged value; usage never drives a status, so stale beats blank here.
//
// A probe that finds no plan access where there was some (lostAccess)
// rereads the account's auth: an expired login probes as not available,
// the same as API-key auth, and only `claude auth status` can tell the
// strip to say "logged out" rather than "n/a".
func (m *Model) deliverUsage(msg usageResult) {
	m.ensureAccountMaps()
	reread, rereadDefault := false, false
	for name, u := range msg.results {
		if m.lostAccess(name, u) {
			reread = true
			rereadDefault = rereadDefault || name == account.DefaultName
		}
		m.usage[name] = accountUsage{last: u}
	}
	for name, err := range msg.errs {
		cur := m.usage[name]
		cur.err = err
		m.usage[name] = cur
		log.For("account").Debug("usage.probe_failed", "account", name, "err", err.Error())
	}
	m.emit(AccountsChanged{})
	if reread {
		m.RequestAccountsRefresh(rereadDefault)
	}
}

// lostAccess reports that u, name's new probe, has no plan access where
// the account had some: its previous sample was available, or this is its
// first sample and its auth says it is logged in. An account that stays
// without access (API-key auth) triggers nothing after the first time.
func (m *Model) lostAccess(name string, u account.Usage) bool {
	if u.Available {
		return false
	}
	prev := m.usage[name].last
	if prev.At.IsZero() {
		return m.RCAuthFor(name).Identity.LoggedIn
	}
	return prev.Available
}
