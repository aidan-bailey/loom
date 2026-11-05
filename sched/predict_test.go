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
	p := envelope([]Span{{sec(0), sec(60), 5}, {sec(30), sec(90), 8}, {sec(120), sec(150), 2}})
	assert.Equal(t, Prediction{{sec(0), sec(30), 5}, {sec(30), sec(90), 8}, {sec(120), sec(150), 2}}, p)
	assert.Equal(t, int64(5), p.At(sec(0)))
	assert.Equal(t, int64(8), p.At(sec(30)), "a span holds its start")
	assert.Equal(t, int64(0), p.At(sec(90)), "and not its end")
	assert.Equal(t, int64(0), p.At(sec(100)))
	assert.Equal(t, sec(150), p.End())
	assert.Equal(t, Prediction{{sec(0), sec(60), 3}}, envelope([]Span{{sec(0), sec(30), 3}, {sec(30), sec(60), 3}}), "equal neighbours merge")
}

func TestFlat(t *testing.T) {
	assert.Equal(t, Prediction{{sec(0), sec(10), 7}}, Flat(7, sec(0), sec(10)))
	assert.Equal(t, Prediction{{sec(0), sec(60), 7}}, Flat(7, sec(0), sec(0)), "a minute when the end has passed")
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
	assert.Equal(t, Prediction{{sec(0), sec(20), 1205863}, {sec(20), sec(130), 9646900}}, p)
}

func TestPredict_FromItsProgress(t *testing.T) {
	// At progress 150 the run had been going 75 s; 25 s of its peak are left.
	p, ok := Predict([]*Curve{rising(), rising(), rising()}, 150, t0)
	require.True(t, ok)
	assert.Equal(t, Prediction{{sec(0), sec(55), 9646900}}, p)
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
	b := Prediction{{sec(0), sec(60), 1}, {sec(60), sec(120), 9}}
	assert.Equal(t, int64(10), PeakSum([]Prediction{a, b}, sec(0), sec(120)), "the peaks don't meet")
	c := Prediction{{sec(30), sec(90), 9}}
	assert.Equal(t, int64(18), PeakSum([]Prediction{a, c}, sec(0), sec(120)), "they do")
	assert.Equal(t, int64(9), PeakSum([]Prediction{a, c}, sec(0), sec(20)), "not within the window")
}
