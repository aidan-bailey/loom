package flowview

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/aidan-bailey/loom/internal/flowrun"
)

// Priced is `loom flow costs run --json`'s output (flow/lib/agent_costs.py
// run_json): a row per agent run in dispatch order, per manager session,
// and the total, at API list prices.
type Priced struct {
	Run      string        `json:"run"`
	Agents   []PricedAgent `json:"agents"`
	Managers []PricedAgent `json:"managers"`
	Total    struct {
		USD        float64 `json:"usd"`
		USDInclEst float64 `json:"usd_incl_est"`
	} `json:"total"`
}

// PricedAgent is one row of Priced. USD is nil for an agent run no
// transcript was found for.
type PricedAgent struct {
	Name       string   `json:"name"`
	Role       string   `json:"role"`
	Model      string   `json:"model"`
	USD        *float64 `json:"usd"`
	USDInclEst *float64 `json:"usd_incl_est"`
	Running    bool     `json:"running"`
	Session    string   `json:"session,omitempty"`
}

// The rows of the cost box's story sums that are no story.
const (
	RowPremise = "P"
	RowFinal   = "F"
)

// defaultFinalShare is the final round's share of a run's cost when the
// repo's records don't give one (spec §3).
const defaultFinalShare = 0.15

// compareRuns is how many past runs the comparison shows.
const compareRuns = 5

// Costs is the board's cost box (spec §3).
type Costs struct {
	Spent    float64            // the run's priced total
	ByStory  map[string]float64 // story id, RowPremise, RowFinal
	ByAgent  map[string]float64 // agent run name
	ByRole   map[string]float64 // role; the manager sessions as "manager"
	Unpriced []string           // agent runs with no figure yet
	Design   *float64           // the design session's, nil when not priced
	Draft    *float64           // the **Design stage:** line's, nil when the plan has none
	// Flow is design + draft + run, over the FlowParts of them that have
	// a figure.
	Flow       float64
	FlowParts  int
	Projection *float64
	Why        string // why there is no projection
	Compare    Comparison
	// Error is the pricer's failure: the run has no figures at all.
	Error string
}

// Comparison is this run's cost per story beside the repo's past runs'.
type Comparison struct {
	Past   []float64 // the last compareRuns runs' USD per story, oldest first
	Median *float64
	This   *float64 // spent per finished story, nil before one has finished
}

// CostsOf builds the cost box from the board, the pricer's output (nil
// with perr saying why it failed, or for a flow with no run), the design
// session's figure and the records of the repo key's past runs.
func CostsOf(d FlowDetail, p *Priced, perr string, design *float64, history []PastRun, key string) Costs {
	c := Costs{ByStory: map[string]float64{}, ByAgent: map[string]float64{}, ByRole: map[string]float64{}, Design: design}
	if usd, ok := designStageUSD(d.Design); ok {
		c.Draft = &usd
	}
	stories := make([]flowrun.Story, len(d.Stories))
	for i, s := range d.Stories {
		stories[i] = flowrun.Story{ID: s.ID}
	}
	switch {
	case perr != "":
		c.Error = "cost unavailable: " + perr
	case p != nil:
		c.Spent = p.Total.USD
		for _, a := range p.Agents {
			if a.USD == nil {
				c.Unpriced = append(c.Unpriced, a.Name)
				continue
			}
			c.ByAgent[a.Name] += *a.USD
			c.ByRole[a.Role] += *a.USD
			if s, ok := flowrun.StepOf(d.Run, a.Name, stories); ok {
				switch {
				case s.Kind == flowrun.KindPremise:
					c.ByStory[RowPremise] += *a.USD
				case s.Final:
					c.ByStory[RowFinal] += *a.USD
				default:
					c.ByStory[s.Story] += *a.USD
				}
			}
		}
		for _, m := range p.Managers {
			if m.USD != nil {
				c.ByRole["manager"] += *m.USD
			}
		}
	}
	for _, part := range []*float64{c.Design, c.Draft} {
		if part != nil {
			c.Flow += *part
			c.FlowParts++
		}
	}
	if p != nil && perr == "" {
		c.Flow += c.Spent
		c.FlowParts++
	}

	var past []PastRun
	for _, r := range history {
		if r.Key == key && r.Unreadable == "" && r.HasUSD && r.Stories > 0 {
			past = append(past, r)
		}
	}
	finished, finishedUSD := 0, 0.0
	for _, s := range d.Stories {
		if s.Row.Status == "done" {
			finished++
			finishedUSD += c.ByStory[s.ID]
		}
	}
	c.Compare = compare(past, c.Spent, finished)
	if c.Error == "" {
		c.Projection, c.Why = project(d, c, past, finished, finishedUSD)
	} else {
		c.Why = "no figures to project from"
	}
	return c
}

// project is spec §3's projection, or why there is none.
func project(d FlowDetail, c Costs, past []PastRun, finished int, finishedUSD float64) (*float64, string) {
	if d.Run != "" && flowrun.Ended(d.Status) {
		return nil, "the run has ended"
	}
	var perStory float64
	switch {
	case finished > 0:
		perStory = finishedUSD / float64(finished)
	case len(past) > 0:
		rates := make([]float64, len(past))
		for i, r := range past {
			rates[i] = r.USD / float64(r.Stories)
		}
		perStory = median(rates)
	default:
		return nil, "no story has finished and the repo has no past runs"
	}
	share := finalShare(past)
	var v float64
	if d.Final == nil {
		v = (c.Spent + perStory*float64(len(d.Stories)-finished)) / (1 - share)
	} else {
		v = max(c.Spent, (c.Spent-c.ByStory[RowFinal])/(1-share))
	}
	return &v, ""
}

// finalShare is the median of the past engine runs' final-round share, when
// two or more give one, else defaultFinalShare.
func finalShare(past []PastRun) float64 {
	var shares []float64
	for _, r := range past {
		if r.HasFinal && r.USD > 0 {
			shares = append(shares, r.FinalUSD/r.USD)
		}
	}
	if len(shares) < 2 {
		return defaultFinalShare
	}
	return median(shares)
}

// compare is the comparison over past, newest first as History gives it.
func compare(past []PastRun, spent float64, finished int) Comparison {
	var c Comparison
	n := min(len(past), compareRuns)
	for i := n - 1; i >= 0; i-- {
		c.Past = append(c.Past, past[i].USD/float64(past[i].Stories))
	}
	if n > 0 {
		m := median(c.Past)
		c.Median = &m
	}
	if finished > 0 {
		v := spent / float64(finished)
		c.This = &v
	}
	return c
}

// median is the median of xs, which must not be empty.
func median(xs []float64) float64 {
	s := slices.Clone(xs)
	slices.Sort(s)
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}

var loggedUSD = regexp.MustCompile(`\$([0-9][0-9,]*(?:\.[0-9]+)?) logged`)

// designStageUSD reads the **Design stage:** line's "$N logged".
func designStageUSD(line string) (float64, bool) {
	m := loggedUSD.FindStringSubmatch(line)
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", ""), 64)
	return v, err == nil
}
