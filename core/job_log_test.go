package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jobLogFixture is a queue model holding a running session "mine", a paused
// one "other", and job 7 owned by mine whose output.log holds text.
func jobLogFixture(t *testing.T, text string) (m *Model, mine, other InstanceID) {
	t.Helper()
	m = wqModel(t)
	a := activeInst(t, m, "mine")
	b := pausedInst(t, "other")
	hold(m, b)
	mine, other = m.idOf(a), m.idOf(b)
	m.queue.store.Jobs = append(m.queue.store.Jobs, wqEnded(7, mine), wqEnded(8, other), wqEnded(9, 0))
	for _, id := range []sched.JobID{7, 8, 9} {
		dir := m.queue.jobDir(id)
		require.NoError(t, os.MkdirAll(dir, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(dir, sched.LogFile), []byte(text), 0o600))
	}
	return m, mine, other
}

func TestReadJobLog_ReadsTheSessionsOwnJob(t *testing.T) {
	m, mine, _ := jobLogFixture(t, "one\ntwo\n")

	m.ReadJobLog(mine, 7, -1, 4)
	rs := replies(run(m, m.Drain()))
	require.Len(t, rs, 1)
	require.NoError(t, rs[0].Err)
	assert.Equal(t, sched.LogRead{Data: []byte("one\ntwo\n"), End: 8, Size: 8}, rs[0].JobLog)

	m.ReadJobLog(mine, 7, 4, 5)
	rs = replies(run(m, m.Drain()))
	require.Len(t, rs, 1)
	assert.Equal(t, "two\n", string(rs[0].JobLog.Data), "a read from an offset")
}

func TestReadJobLog_APausedSessionsJobIsRead(t *testing.T) {
	m, _, other := jobLogFixture(t, "x\n")

	m.ReadJobLog(other, 8, -1, 4)
	rs := replies(run(m, m.Drain()))
	require.Len(t, rs, 1)
	require.NoError(t, rs[0].Err)
	assert.Equal(t, "x\n", string(rs[0].JobLog.Data))
}

func TestReadJobLog_RefusesAJobThatIsNotTheSessions(t *testing.T) {
	m, mine, _ := jobLogFixture(t, "secret\n")
	for _, c := range []struct {
		name string
		job  sched.JobID
		want string
	}{
		{"another session's job", 8, "read a job log of mine: job 8 is not this session's"},
		{"a job with no owner", 9, "read a job log of mine: job 9 is not this session's"},
		{"a job the queue does not hold", 40, "read a job log of mine: job 40 is no longer in the queue"},
	} {
		m.ReadJobLog(mine, c.job, -1, 4)
		out := m.Drain()
		assert.Empty(t, out.Jobs, "%s: nothing to read", c.name)
		rs := replies(out)
		require.Len(t, rs, 1, c.name)
		assert.EqualError(t, rs[0].Err, c.want, c.name)
		assert.ErrorIs(t, rs[0].Err, ErrRefused, c.name)
		assert.Empty(t, rs[0].JobLog.Data, c.name)
	}
}
