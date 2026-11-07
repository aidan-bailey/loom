package attention

import (
	"fmt"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/sched/advisor"
)

// The block's defaults and bounds beyond the advisor's (spec §6).
const (
	DefaultMaxParallel = 3
	DefaultWait        = 30 * time.Second
	maxParallel        = 8
	minWaitSecs        = 5
	maxWaitSecs        = 120
)

// Settings is the attention block resolved: whether the classifier runs,
// the model and effort it runs at, how many calls run at once, and how long
// a call may take, its wait for a free slot included.
type Settings struct {
	Enabled     bool
	Model       string
	Effort      string
	MaxParallel int
	Wait        time.Duration
}

// Resolve applies the defaults to the attention block (nil is every
// default) and checks its bounds: the advisor's for enabled, model and
// effort, max_parallel 1–8 and wait_seconds 5–120 (0 takes the default). A
// block outside them is an error and turns the classifier off.
func Resolve(s *config.AttentionSettings) (Settings, error) {
	var agent *config.AgentSettings
	if s != nil {
		agent = &config.AgentSettings{Enabled: s.Enabled, Model: s.Model, Effort: s.Effort}
	}
	base, err := advisor.ResolveBlock("attention", agent)
	if err != nil {
		return Settings{}, err
	}
	out := Settings{Enabled: base.Enabled, Model: base.Model, Effort: base.Effort, MaxParallel: DefaultMaxParallel, Wait: DefaultWait}
	if s == nil {
		return out, nil
	}
	if s.MaxParallel != 0 {
		if s.MaxParallel < 1 || s.MaxParallel > maxParallel {
			return Settings{}, fmt.Errorf("attention.max_parallel is %d: it must be 1–%d", s.MaxParallel, maxParallel)
		}
		out.MaxParallel = s.MaxParallel
	}
	if s.WaitSeconds != 0 {
		if s.WaitSeconds < minWaitSecs || s.WaitSeconds > maxWaitSecs {
			return Settings{}, fmt.Errorf("attention.wait_seconds is %d: it must be %d–%d", s.WaitSeconds, minWaitSecs, maxWaitSecs)
		}
		out.Wait = time.Duration(s.WaitSeconds) * time.Second
	}
	return out, nil
}
