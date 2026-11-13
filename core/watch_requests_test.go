package core

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/watch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReply_TheWatchViewCrossesTheWire: a Reply has a wire form of its own
// (replyJSON), so a field it does not list is one every client reads as
// zero.
func TestReply_TheWatchViewCrossesTheWire(t *testing.T) {
	at := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	in := Reply{Req: 3, Watch: WatchView{
		On: true, Err: "no loom-watch.json on origin/main", Viewer: "bot", Considered: 4, Unconsidered: 1,
		Issues: []WatchIssueView{{
			Number: 12, State: "open", Session: "heal 12", PR: 30,
			Waiting: "a foreign session holds it", Failed: "start: the hook said no", Nudges: map[string]int{"ci": 2}, Escalated: true,
		}},
		Actions:     []watch.Action{{Kind: watch.ActWait, Issue: 12, Reason: "a foreign session holds it"}},
		Escalations: []WatchEscalation{{Issue: 12, Reason: "stuck", At: at}},
	}}

	data, err := json.Marshal(in)
	require.NoError(t, err)
	for _, key := range []string{`"on":true`, `"considered":4`, `"unconsidered":1`, `"escalations"`, `"waiting"`, `"failed":"start: the hook said no"`, `"escalated":true`, `"issue":12`} {
		assert.Contains(t, string(data), key, "the views' keys are snake_case")
	}

	var out Reply
	require.NoError(t, json.Unmarshal(data, &out))
	assert.EqualValues(t, 3, out.Req)
	assert.Equal(t, in.Watch, out.Watch)
}
