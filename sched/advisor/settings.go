package advisor

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/aidan-bailey/loom/config"
)

// The agent block's defaults (smart scheduler spec §6).
const (
	DefaultModel  = "haiku"
	DefaultEffort = "low"
	DefaultWait   = time.Minute
)

const (
	maxModelChars = 100
	maxWaitSecs   = 600
)

var efforts = []string{"low", "medium", "high", "xhigh", "max"}

// Settings is the agent block resolved: whether the advisor runs, the model
// and effort it runs at, the account it runs as ("" the default), and how
// long a job waits for its prior.
type Settings struct {
	Enabled bool
	Model   string
	Effort  string
	Account string
	Wait    time.Duration
}

// Resolve applies the defaults to the agent block (nil is every default)
// and checks its bounds: model 1–100 characters with no whitespace, effort
// one of low, medium, high, xhigh and max, wait_seconds 1–600 (0 takes the
// default). A block outside them is an error and turns the advisor off;
// whether the account is registered is the caller's to check.
func Resolve(s *config.AgentSettings) (Settings, error) { return ResolveBlock("scheduler.agent", s) }

// ResolveBlock is Resolve for an agent block under key ("scheduler.agent",
// "feedback.grouper"): the same defaults and bounds, its errors naming key.
func ResolveBlock(key string, s *config.AgentSettings) (Settings, error) {
	out := Settings{Enabled: true, Model: DefaultModel, Effort: DefaultEffort, Wait: DefaultWait}
	if s == nil {
		return out, nil
	}
	if s.Enabled != nil {
		out.Enabled = *s.Enabled
	}
	if s.Model != "" {
		if utf8.RuneCountInString(s.Model) > maxModelChars || strings.ContainsFunc(s.Model, unicode.IsSpace) {
			return Settings{}, fmt.Errorf("%s.model %q: it must be 1–%d characters with no whitespace", key, s.Model, maxModelChars)
		}
		out.Model = s.Model
	}
	if s.Effort != "" {
		if !slices.Contains(efforts, s.Effort) {
			return Settings{}, fmt.Errorf("%s.effort %q: it must be one of %s", key, s.Effort, strings.Join(efforts, ", "))
		}
		out.Effort = s.Effort
	}
	if s.WaitSeconds != 0 {
		if s.WaitSeconds < 1 || s.WaitSeconds > maxWaitSecs {
			return Settings{}, fmt.Errorf("%s.wait_seconds is %d: it must be 1–%d", key, s.WaitSeconds, maxWaitSecs)
		}
		out.Wait = time.Duration(s.WaitSeconds) * time.Second
	}
	out.Account = s.Account
	return out, nil
}
