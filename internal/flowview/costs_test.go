package flowview

import (
	"testing"

	"github.com/aidan-bailey/loom/internal/flowrun"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func usd(v float64) *float64 { return &v }

// pricedRun is a run's pricing: agent rows by name, role and USD (nil for
// unpriced), and one manager.
func pricedRun(manager float64, rows ...PricedAgent) *Priced {
	p := &Priced{Run: "r", Agents: rows, Managers: []PricedAgent{{Name: "manager m", Role: "manager", USD: usd(manager)}}}
	p.Total.USD = manager
	for _, r := range rows {
		if r.USD != nil {
			p.Total.USD += *r.USD
		}
	}
	return p
}

// board is a run's detail with n stories, the first done of them finished.
func boardOf(n, done int, final bool) FlowDetail {
	d := FlowDetail{Summary: Summary{Run: "r", Status: flowrun.Running}}
	for i := 0; i < n; i++ {
		s := StoryView{ID: string(rune('1' + i))}
		if i < done {
			s.Row.Status = "done"
		}
		d.Stories = append(d.Stories, s)
	}
	if final {
		d.Final = &StoryView{ID: RowFinal}
	}
	return d
}

func TestCostsOf_SumsByStoryAndRole(t *testing.T) {
	p := pricedRun(0.90,
		PricedAgent{Name: "r-premise1", Role: "premise-check", USD: usd(0.10)},
		PricedAgent{Name: "r-s1-dev1", Role: "intermediate-dev", USD: usd(0.50)},
		PricedAgent{Name: "r-s1-review1", Role: "code-quality-reviewer", USD: usd(0.70)},
		PricedAgent{Name: "r-s2-dev1", Role: "intermediate-dev", USD: usd(0.40), Running: true},
		PricedAgent{Name: "r-s2-review1", Role: "story-reviewer"},
		PricedAgent{Name: "r-final-review1", Role: "code-quality-reviewer", USD: usd(1.00)})
	c := CostsOf(boardOf(2, 1, true), p, "", nil, nil, "-k")
	assert.InDelta(t, 3.60, c.Spent, 1e-9)
	assert.InDeltaMapValues(t, map[string]float64{RowPremise: 0.10, "1": 1.20, "2": 0.40, RowFinal: 1.00}, c.ByStory, 1e-9)
	assert.InDeltaMapValues(t, map[string]float64{"premise-check": 0.10, "intermediate-dev": 0.90, "code-quality-reviewer": 1.70, "manager": 0.90}, c.ByRole, 1e-9)
	assert.Equal(t, []string{"r-s2-review1"}, c.Unpriced)
	assert.InDelta(t, 3.60, c.Flow, 1e-9)
	assert.Equal(t, 1, c.FlowParts)
}

func TestCostsOf_TheWholeFlow(t *testing.T) {
	d := boardOf(1, 0, false)
	d.Design = "/flow:draft session s1, 2026-11-06T12:33:06Z to 2026-11-06T13:05:46Z UTC: $12.05 logged, $12.16 with estimated thinking; 5225 plan lines, $2.31 per 1,000 lines."
	c := CostsOf(d, pricedRun(1.00), "", usd(3.14), nil, "-k")
	require.NotNil(t, c.Draft)
	assert.InDelta(t, 12.05, *c.Draft, 1e-9)
	assert.InDelta(t, 3.14+12.05+1.00, c.Flow, 1e-9)
	assert.Equal(t, 3, c.FlowParts)

	none := CostsOf(boardOf(1, 0, false), nil, "", nil, nil, "-k")
	assert.Zero(t, none.FlowParts, "a part with no figure is left out of the sum, and counted out")
}

func TestCostsOf_APricerFailureShowsNoFigure(t *testing.T) {
	c := CostsOf(boardOf(2, 1, false), nil, "agent_costs: boom", usd(1), nil, "-k")
	assert.Equal(t, "cost unavailable: agent_costs: boom", c.Error)
	assert.Zero(t, c.Spent)
	assert.Nil(t, c.Projection)
	assert.Equal(t, 1, c.FlowParts, "the design figure still stands")
}

func TestProjection(t *testing.T) {
	history := []PastRun{
		{Key: "-k", Stories: 4, USD: 10.08, HasUSD: true},
		{Key: "-k", Stories: 6, USD: 14.20, HasUSD: true},
		{Key: "-k", Stories: 9, USD: 31.77, HasUSD: true},
		{Key: "-other", Stories: 1, USD: 100, HasUSD: true},
	}
	t.Run("from this run's finished stories", func(t *testing.T) {
		p := pricedRun(0.40, PricedAgent{Name: "r-s1-dev1", USD: usd(1.20)}, PricedAgent{Name: "r-s2-dev1", USD: usd(0.80)})
		c := CostsOf(boardOf(5, 2, false), p, "", nil, nil, "-k")
		require.NotNil(t, c.Projection)
		// mean 1.00 a story; (2.40 + 1.00×3) / (1 − 0.15)
		assert.InDelta(t, 5.40/0.85, *c.Projection, 1e-9)
	})
	t.Run("from the repo's past runs before a story finishes", func(t *testing.T) {
		c := CostsOf(boardOf(5, 0, false), pricedRun(0.50), "", nil, history, "-k")
		require.NotNil(t, c.Projection)
		// per story: 2.52, 2.3667, 3.53 → median 2.52; (0.50 + 2.52×5) / 0.85
		assert.InDelta(t, (0.50+2.52*5)/0.85, *c.Projection, 1e-9)
	})
	t.Run("no basis", func(t *testing.T) {
		c := CostsOf(boardOf(5, 0, false), pricedRun(0.50), "", nil, nil, "-k")
		assert.Nil(t, c.Projection)
		assert.Equal(t, "no story has finished and the repo has no past runs", c.Why)
	})
	t.Run("in the final round", func(t *testing.T) {
		p := pricedRun(1.00, PricedAgent{Name: "r-s1-dev1", USD: usd(4.00)}, PricedAgent{Name: "r-final-review1", USD: usd(1.00)})
		c := CostsOf(boardOf(1, 1, true), p, "", nil, nil, "-k")
		require.NotNil(t, c.Projection)
		// spent 6.00, final spent 1.00: max(6.00, 5.00 / 0.85 = 5.88)
		assert.InDelta(t, 6.00, *c.Projection, 1e-9)
	})
	t.Run("the final share from the repo's records", func(t *testing.T) {
		withFinal := []PastRun{
			{Key: "-k", Stories: 4, USD: 10.08, HasUSD: true, FinalUSD: 1.31, HasFinal: true},
			{Key: "-k", Stories: 6, USD: 14.20, HasUSD: true, FinalUSD: 2.00, HasFinal: true},
		}
		p := pricedRun(0, PricedAgent{Name: "r-s1-dev1", USD: usd(1.00)})
		c := CostsOf(boardOf(2, 1, false), p, "", nil, withFinal, "-k")
		share := (1.31/10.08 + 2.00/14.20) / 2
		require.NotNil(t, c.Projection)
		assert.InDelta(t, (1.00+1.00)/(1-share), *c.Projection, 1e-9)
	})
	t.Run("an ended run", func(t *testing.T) {
		d := boardOf(1, 1, true)
		d.Status = flowrun.Done
		c := CostsOf(d, pricedRun(1), "", nil, nil, "-k")
		assert.Nil(t, c.Projection)
		assert.Equal(t, "the run has ended", c.Why)
	})
}

func TestComparison(t *testing.T) {
	var history []PastRun
	for i, v := range []float64{6, 5, 4, 3, 2, 1} { // newest first, as History gives them
		history = append(history, PastRun{Key: "-k", File: string(rune('a' + i)), Stories: 2, USD: v * 2, HasUSD: true})
	}
	history = append(history, PastRun{Key: "-k", Stories: 2, Unreadable: "x"})
	p := pricedRun(0, PricedAgent{Name: "r-s1-dev1", USD: usd(3.00)})
	c := CostsOf(boardOf(2, 1, false), p, "", nil, history, "-k")
	assert.Equal(t, []float64{2, 3, 4, 5, 6}, c.Compare.Past, "the last five, oldest first")
	require.NotNil(t, c.Compare.Median)
	assert.InDelta(t, 4, *c.Compare.Median, 1e-9)
	require.NotNil(t, c.Compare.This)
	assert.InDelta(t, 3.00, *c.Compare.This, 1e-9)
	assert.Nil(t, CostsOf(boardOf(2, 0, false), p, "", nil, nil, "-k").Compare.This, "no story finished")
}

func TestDesignStageUSD(t *testing.T) {
	v, ok := designStageUSD("/flow:draft session s1: $1,012.05 logged, $1,012.16 with estimated thinking")
	assert.True(t, ok)
	assert.InDelta(t, 1012.05, v, 1e-9)
	_, ok = designStageUSD("/flow:draft session s1: not priced")
	assert.False(t, ok)
}
