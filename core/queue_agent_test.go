package core

import (
	"testing"

	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestJobQueue_AnAgentJobIsNeverSized: an agent job (flow in loom part 3a)
// keeps no claim, no signature and no sizing, asks the advisor nothing,
// records its account, is admitted by its account's limit, and its spec
// tells the shim its kind.
func TestJobQueue_AnAgentJobIsNeverSized(t *testing.T) {
	m := wqModel(t)
	sub := wqJob(t, "sh", "-c", "exit 0")
	sub.Kind = sched.KindAgent
	sub.Env = append(sub.Env, "CLAUDE_CONFIG_DIR=/acc/a")

	id, _, err := m.submitJob(sub)

	require.NoError(t, err)
	rec := wqRecord(t, m, id)
	assert.Equal(t, sched.KindAgent, rec.Kind)
	assert.Equal(t, "/acc/a", rec.AccountKey)
	assert.Equal(t, sched.Claim{}, rec.Claim, "no claim, not even the default")
	assert.Empty(t, rec.Signature)
	assert.Empty(t, rec.Sizing)
	assert.Empty(t, m.queue.advisor.pending, "the advisor is asked nothing")
	assert.Equal(t, sched.Running, rec.State, "admitted by its account's limit")
	spec, err := sched.ReadSpec(m.queue.jobDir(id))
	require.NoError(t, err)
	assert.Equal(t, sched.KindAgent, spec.Kind, "the shim gives it AgentHints")
	assert.Equal(t, sched.KindAgent, m.queueJobView(m.queue, rec, nil).Kind)

	build := wqSubmit(t, m, "sh", "-c", "exit 0")
	assert.Equal(t, sched.Claim{CPUs: 4, MemoryGB: 6}, wqRecord(t, m, build).Claim, "a build job is still sized")
}
