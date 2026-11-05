package sched

import (
	"encoding/json"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tracePts is a complete trace of points one second apart from t0.
func tracePts(t0 time.Time, pts ...TracePoint) *Trace {
	tr := &Trace{}
	for i, p := range pts {
		tr.Add(t0.Add(time.Duration(i)*time.Second), p)
	}
	return tr
}

func TestTrace_Add(t *testing.T) {
	t0 := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	tr := tracePts(t0, TracePoint{X: 5, RSSKB: 10}, TracePoint{X: 3, RSSKB: 20})
	assert.Equal(t, 5.0, tr.Points[1].X, "progress never falls")
	assert.False(t, tr.Incomplete)
	assert.True(t, tr.Fresh(t0.Add(31*time.Second)), "30 s after its newest point it is fresh")
	assert.False(t, tr.Fresh(t0.Add(32*time.Second)))
	assert.False(t, (*Trace)(nil).Fresh(t0), "no trace is never fresh")

	tr.Add(t0.Add(32*time.Second), TracePoint{X: 6})
	assert.True(t, tr.Incomplete, "a gap of 31 s between points")

	full := &Trace{}
	for i := range tracePoints + 1 {
		p := TracePoint{X: float64(i), RSSKB: int64(i % 7), Cores: float64(i % 5)}
		if i == 0 {
			p.RSSKB = 1000 // the earlier point of its pair holds the peak
		}
		full.Add(t0.Add(time.Duration(i)*time.Second), p)
	}
	require.Len(t, full.Points, (tracePoints+2)/2, "past 256 points, pairs merge")
	assert.Equal(t, TracePoint{X: 1, RSSKB: 1000, Cores: 1}, full.Points[0], "the later progress, the higher usage")
	var rss int64
	var cores float64
	for _, p := range full.Points {
		rss, cores = max(rss, p.RSSKB), max(cores, p.Cores)
	}
	assert.Equal(t, int64(1000), rss, "every peak stays")
	assert.Equal(t, 4.0, cores)
	assert.Equal(t, float64(tracePoints), full.Points[len(full.Points)-1].X, "the odd point out stays as it was")
}

func TestBuildProfile(t *testing.T) {
	t0 := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	tr := tracePts(t0,
		TracePoint{X: 1, RSSKB: 3000, Cores: 4},
		TracePoint{X: 10.04, RSSKB: 6000, Cores: 4},
		TracePoint{X: 10.04, RSSKB: 2000, Cores: 1},
		TracePoint{X: 20, RSSKB: 1000, Cores: 1},
		TracePoint{X: 30, RSSKB: 1000, Cores: 0.5},
	)
	p, ok := BuildProfile(tr)
	require.True(t, ok)
	assert.Equal(t, 30.0, p.End)
	assert.Equal(t, Staircase{PeakAt: 10.1, Steps: []Step{{Until: 10.1, Level: 6144}, {Until: 30, Level: 1024}}}, p.Mem,
		"suffix maxima, rounded up to whole MB, one step per progress and per level")
	assert.Equal(t, Staircase{PeakAt: 1, Steps: []Step{{Until: 10.1, Level: 4000}, {Until: 20, Level: 1000}, {Until: 30, Level: 500}}}, p.Cores)
	require.NoError(t, p.Validate())

	for name, tr := range map[string]*Trace{"none": nil, "empty": {}} {
		_, ok := BuildProfile(tr)
		assert.False(t, ok, name)
	}
	gap := tracePts(t0, TracePoint{X: 1})
	gap.Add(t0.Add(time.Minute), TracePoint{X: 2})
	_, ok = BuildProfile(gap)
	assert.False(t, ok, "an incomplete trace")
}

// A compressed staircase is never below the suffix maximum at any progress.
func TestBuildProfile_NeverBelowTheRun(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	t0 := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	for range 200 {
		tr := &Trace{}
		x := 0.0
		for i := range 1 + r.IntN(300) {
			x += r.Float64() * 3
			tr.Add(t0.Add(time.Duration(i)*time.Second), TracePoint{X: x, RSSKB: r.Int64N(1 << 24), Cores: r.Float64() * 16})
		}
		p, ok := BuildProfile(tr)
		require.True(t, ok)
		require.NoError(t, p.Validate())
		require.LessOrEqual(t, len(p.Mem.Steps), profileSteps)
		require.LessOrEqual(t, len(p.Cores.Steps), profileSteps)
		for i, pt := range tr.Points {
			var rss int64
			var cores float64
			for _, later := range tr.Points[i:] {
				rss, cores = max(rss, later.RSSKB), max(cores, later.Cores)
			}
			// Just below the point's progress, its own usage is still to come.
			at := max(0, pt.X-1e-9)
			m, ok := p.Mem.At(at)
			require.True(t, ok)
			require.GreaterOrEqual(t, m, rss)
			c, ok := p.Cores.At(at)
			require.True(t, ok)
			require.GreaterOrEqual(t, float64(c), cores*1000)
		}
	}
}

func TestStaircase_At(t *testing.T) {
	s := Staircase{Steps: []Step{{Until: 10, Level: 5}, {Until: 20, Level: 2}}}
	for _, c := range []struct {
		x    float64
		want int64
		ok   bool
	}{{0, 5, true}, {9.9, 5, true}, {10, 2, true}, {19.9, 2, true}, {20, 0, false}} {
		got, ok := s.At(c.x)
		assert.Equal(t, c.want, got, "at %v", c.x)
		assert.Equal(t, c.ok, ok, "at %v", c.x)
	}
	assert.Equal(t, int64(5), s.Peak())
	assert.Zero(t, Staircase{}.Peak())
}

func TestProfile_Validate(t *testing.T) {
	good := func() *Profile {
		return &Profile{End: 20,
			Mem:   Staircase{PeakAt: 5, Steps: []Step{{Until: 10, Level: 5}, {Until: 20, Level: 2}}},
			Cores: Staircase{PeakAt: 0, Steps: []Step{{Until: 20, Level: 1000}}}}
	}
	require.NoError(t, good().Validate())
	for name, spoil := range map[string]func(p *Profile){
		"no steps":            func(p *Profile) { p.Cores.Steps = nil },
		"too many steps":      func(p *Profile) { p.Cores.Steps = make([]Step, profileSteps+1) },
		"a negative level":    func(p *Profile) { p.Mem.Steps[1].Level = -1 },
		"a negative until":    func(p *Profile) { p.Mem.Steps[0].Until = -1 },
		"steps out of order":  func(p *Profile) { p.Mem.Steps[1].Until = 10 },
		"a rising level":      func(p *Profile) { p.Mem.Steps[1].Level = 6 },
		"a peak past step 1":  func(p *Profile) { p.Mem.PeakAt = 11 },
		"a negative peak":     func(p *Profile) { p.Mem.PeakAt = -1 },
		"an end off the last": func(p *Profile) { p.End = 21 },
	} {
		p := good()
		spoil(p)
		assert.Error(t, p.Validate(), name)
	}
}

func TestProfile_JSON(t *testing.T) {
	p := Profile{End: 30, Mem: Staircase{PeakAt: 10.1, Steps: []Step{{Until: 10.1, Level: 6144}, {Until: 30, Level: 1024}}},
		Cores: Staircase{PeakAt: 1, Steps: []Step{{Until: 30, Level: 4000}}}}
	b, err := json.Marshal(p)
	require.NoError(t, err)
	assert.JSONEq(t, `{"end":30,"mem":{"peak_at":10.1,"steps":[[10.1,6144],[30,1024]]},"cores":{"peak_at":1,"steps":[[30,4000]]}}`, string(b))
	var back Profile
	require.NoError(t, json.Unmarshal(b, &back))
	assert.Equal(t, p, back)
	assert.Error(t, json.Unmarshal([]byte(`{"mem":{"steps":["x"]}}`), &back))
}
