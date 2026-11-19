package sched

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var t0 = time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)

func sec(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }

func TestPrediction_EnvelopeAndAt(t *testing.T) {
	p := envelope([][]Span{{{sec(0), sec(60), 5, 0}, {sec(30), sec(90), 8, 0}, {sec(120), sec(150), 2, 0}}})
	assert.Equal(t, Prediction{{sec(0), sec(30), 5, 0}, {sec(30), sec(90), 8, 0}, {sec(120), sec(150), 2, 0}}, p)
	assert.Equal(t, int64(5), p.At(sec(0)))
	assert.Equal(t, int64(8), p.At(sec(30)), "a span holds its start")
	assert.Equal(t, int64(0), p.At(sec(90)), "and not its end")
	assert.Equal(t, int64(0), p.At(sec(100)))
	assert.Equal(t, sec(150), p.End())
	assert.Equal(t, Prediction{{sec(0), sec(60), 3, 0}}, envelope([][]Span{{{sec(0), sec(30), 3, 0}, {sec(30), sec(60), 3, 0}}}), "equal neighbours merge")
	runs := envelope([][]Span{{{sec(0), sec(60), 9, 0}}, {{sec(0), sec(60), 2, 0}}, {{sec(0), sec(30), 4, 0}}})
	assert.Equal(t, Prediction{{sec(0), sec(30), 9, 5}, {sec(30), sec(60), 9, 7}}, runs, "the most run and the median one; a run with no span counts 0")
	assert.Len(t, runs, 2, "equal worsts with different usual levels don't merge")
}

func TestFlat(t *testing.T) {
	assert.Equal(t, Prediction{{sec(0), sec(10), 7, 0}}, Flat(7, sec(0), sec(10)))
	assert.Equal(t, Prediction{{sec(0), sec(60), 7, 0}}, Flat(7, sec(0), sec(0)), "a minute when the end has passed")
}

// rising is a run whose memory is 1 GB for its first 100 CPU-seconds (50 s)
// and 8 GB to its end at 200 (100 s).
func rising() *Curve {
	return &Curve{End: 200, Mem: []CurveStep{{100, 1 << 20, 50}, {200, 8 << 20, 100}}, Cores: []CurveStep{{200, 2000, 100}}}
}

func TestPredict_FromTheStart(t *testing.T) {
	p, ok := Predict([]*Curve{rising(), rising(), rising()}, 0, t0)
	require.True(t, ok)
	// 1 GB × 1.15 from 0 to 50 s, widened to 80 s; 8 GB × 1.15 from 50 s,
	// widened back 30 s to 20 s, to 100 s, widened to 130 s.
	assert.Equal(t, Prediction{{sec(0), sec(20), 1205863, 0}, {sec(20), sec(130), 9646900, 0}}, p)
}

func TestPredict_FromItsProgress(t *testing.T) {
	// At progress 150 the run had been going 75 s; 25 s of its peak are left.
	p, ok := Predict([]*Curve{rising(), rising(), rising()}, 150, t0)
	require.True(t, ok)
	assert.Equal(t, Prediction{{sec(0), sec(55), 9646900, 0}}, p)
}

func TestPredict_NeedsThreeRunsThatLastedPastIt(t *testing.T) {
	_, ok := Predict([]*Curve{rising(), rising()}, 0, t0)
	assert.False(t, ok, "two curves")
	short := &Curve{End: 120, Mem: []CurveStep{{120, 1 << 20, 60}}, Cores: []CurveStep{{120, 1, 60}}}
	_, ok = Predict([]*Curve{rising(), rising(), short}, 150, t0)
	assert.False(t, ok, "one of three ended before 150")
}

func TestPeakSum(t *testing.T) {
	a := Flat(9, sec(0), sec(60))
	b := Prediction{{sec(0), sec(60), 1, 0}, {sec(60), sec(120), 9, 0}}
	assert.Equal(t, int64(10), PeakSum([]Prediction{a, b}, sec(0), sec(120)), "the peaks don't meet")
	c := Prediction{{sec(30), sec(90), 9, 0}}
	assert.Equal(t, int64(18), PeakSum([]Prediction{a, c}, sec(0), sec(120)), "they do")
	assert.Equal(t, int64(9), PeakSum([]Prediction{a, c}, sec(0), sec(20)), "not within the window")
}

// usual is flat kb from 0 to 600 s whose usual run uses usualKB.
func usual(kb, usualKB int64) Prediction { return Flat(kb, sec(0), sec(600)).UsualAtMost(usualKB) }

func TestPeakSum_UsualPlusOneWorst(t *testing.T) {
	a, b := usual(gb(8), gb(2)), usual(gb(8), gb(2))
	assert.Equal(t, gb(10), PeakSum([]Prediction{a, b}, sec(0), sec(600)), "2 + 2 usual, plus one 6 GB excess")
	assert.Equal(t, gb(8), PeakSum([]Prediction{a}, sec(0), sec(600)), "one prediction counts at its worst")
	c := usual(gb(5), gb(4))
	assert.Equal(t, gb(14), PeakSum([]Prediction{a, b, c}, sec(0), sec(600)), "only the largest excess is added")
	plain := Flat(gb(3), sec(0), sec(600))
	assert.Equal(t, gb(11), PeakSum([]Prediction{a, plain}, sec(0), sec(600)), "a prediction with no usual level counts whole: 3 + 2 + 6")
	later := Prediction{{From: sec(300), Until: sec(600), KB: gb(8), ExcessKB: gb(6)}}
	assert.Equal(t, gb(10), PeakSum([]Prediction{a, later}, sec(0), sec(600)), "the sum at a later span's start")
}

func TestPrediction_UsualBounds(t *testing.T) {
	p := Flat(gb(8), sec(0), sec(60))
	assert.Equal(t, gb(6), p.UsualAtMost(gb(2))[0].ExcessKB)
	assert.Equal(t, int64(0), p.UsualAtMost(gb(9))[0].ExcessKB, "never above the worst")
	assert.Equal(t, gb(8), p.UsualAtMost(-1)[0].ExcessKB, "never below 0")
	q := p.UsualAtMost(gb(2))
	assert.Equal(t, gb(3), q.UsualAtLeast(gb(5))[0].ExcessKB, "use raises the usual level")
	assert.Equal(t, gb(6), q.UsualAtLeast(gb(1))[0].ExcessKB, "and never lowers it")
	assert.Equal(t, int64(0), q.UsualAtLeast(gb(9))[0].ExcessKB, "nor lifts it past the worst")
	assert.Equal(t, gb(6), q[0].ExcessKB, "the helpers copy")
}

// level is a run whose memory is gb GB from its start to its end at 200
// CPU-seconds (100 s).
func level(gb int64) *Curve {
	return &Curve{End: 200, Mem: []CurveStep{{200, gb << 20, 100}}, Cores: []CurveStep{{200, 2000, 100}}}
}

func TestPredict_TheUsualLevelIsTheMedianRun(t *testing.T) {
	p, ok := Predict([]*Curve{level(8), level(2), level(3)}, 0, t0)
	require.True(t, ok)
	require.Len(t, p, 1)
	assert.Equal(t, WithMarginKB(8<<20), p[0].KB, "the worst run")
	assert.Equal(t, WithMarginKB(8<<20)-WithMarginKB(3<<20), p[0].ExcessKB, "the median run, 3 GB, is usual")
	p, ok = Predict([]*Curve{level(8), level(2), level(3), level(1)}, 0, t0)
	require.True(t, ok)
	assert.Equal(t, WithMarginKB(8<<20)-WithMarginKB(3<<20), p[0].ExcessKB, "the upper median of an even count")
	short := &Curve{End: 200, Mem: []CurveStep{{200, 1 << 20, 10}}, Cores: []CurveStep{{200, 2000, 10}}}
	p, ok = Predict([]*Curve{level(8), short, short}, 0, t0)
	require.True(t, ok)
	assert.Equal(t, WithMarginKB(8<<20), p[len(p)-1].ExcessKB, "once most runs are over, the usual level is 0")
}

func TestUsualPeakKB(t *testing.T) {
	done := func(peakGB int64) Run { return Run{Outcome: OutcomeDone, PeakKB: peakGB << 20} }
	b := &Belief{Runs: []Run{done(2), done(7), done(2)}}
	kb, ok := UsualPeakKB(b)
	require.True(t, ok)
	assert.Equal(t, WithMarginKB(2<<20), kb, "the median peak with the margin")
	b.Runs = append(b.Runs, done(7))
	kb, _ = UsualPeakKB(b)
	assert.Equal(t, WithMarginKB(7<<20), kb, "the upper median of an even count")

	failed := Run{Outcome: OutcomeFailed, PeakKB: 1 << 20}
	adopted, laddered := done(1), done(1)
	adopted.Adopted, laddered.Laddered = true, true
	_, ok = UsualPeakKB(&Belief{Runs: []Run{done(2), done(2), failed, adopted, laddered}})
	assert.False(t, ok, "only full peaks count, and two is too few")
	kb, ok = UsualPeakKB(&Belief{Runs: []Run{done(0), done(0), done(0), done(0), done(3), done(3), done(3)}})
	require.True(t, ok)
	assert.Equal(t, WithMarginKB(3<<20), kb, "a run that ended before a sample is no evidence")

	var runs []Run
	for range window {
		runs = append(runs, done(1))
	}
	for range window + 1 {
		runs = append(runs, done(9))
	}
	kb, _ = UsualPeakKB(&Belief{Runs: runs})
	assert.Equal(t, WithMarginKB(1<<20), kb, "runs past the window are left out, though they outnumber it")
	_, ok = UsualPeakKB(nil)
	assert.False(t, ok)
}
