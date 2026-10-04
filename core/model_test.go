package core

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestModel_DrainReturnsInOrderAndForgets(t *testing.T) {
	m := NewForTest(Options{})
	m.notifyInfo("one")
	m.notifyErr(errors.New("two"))
	m.notifyErr(nil) // ignored
	m.spawn(func() any { return nil })
	m.spawn(nil) // ignored

	out := m.Drain()
	assert.Equal(t, []Event{Notice{Info: "one"}, Notice{Err: errors.New("two")}}, out.Events)
	assert.Len(t, out.Jobs, 1)
	assert.True(t, m.Drain().Empty(), "a second drain finds nothing")
}

func TestModel_DeliverUnknownIsDropped(t *testing.T) {
	m := NewForTest(Options{})
	m.Deliver(struct{}{})
	assert.True(t, m.Drain().Empty())
}
