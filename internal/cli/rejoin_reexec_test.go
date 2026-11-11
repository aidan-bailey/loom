package cli

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/core/rpc"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newerRefusal is a join's refusal of a newer daemon as daemon_client.go's
// newerDaemonError makes it: it matches rpc.ErrDaemonNewer and gives the
// daemon's hello (NewerDaemon).
type newerRefusal struct{ peer rpc.Hello }

func (e *newerRefusal) Error() string {
	return "the loom daemon is 0.15.0, newer than this loom (v1): upgrade loom"
}

func (e *newerRefusal) Is(target error) bool { return target == rpc.ErrDaemonNewer }

func (e *newerRefusal) NewerDaemon() (rpc.Hello, string) { return e.peer, "0.15.0 (v0.15.0 abc1234)" }

// refuseAsNewerBuild makes every join fail as a newer daemon whose hello
// is peer, with refuseAsNewer's short backoff, and counts the joins.
func refuseAsNewerBuild(t *testing.T, peer rpc.Hello) *int {
	t.Helper()
	joins := refuseAsNewer(t)
	Join = func() (*rpc.Client, error) {
		*joins++
		return nil, &newerRefusal{peer: peer}
	}
	return joins
}

// envValue is key's value in env, and whether it is there.
func envValue(env []string, key string) (string, bool) {
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, key+"="); ok {
			return v, true
		}
	}
	return "", false
}

// TestRejoin_ANewerDaemonIsReexecuted: a command that may start again,
// refused because the daemon is newer, executes the daemon's build with
// its own arguments, the marker and its deadline, said first.
func TestRejoin_ANewerDaemonIsReexecuted(t *testing.T) {
	markReexec(t, "")
	peer := daemonBuild(t, 0o755)
	refuseAsNewerBuild(t, peer)
	call := fakeExec(t)
	var said []string
	deadline := time.Now().Add(time.Minute)

	err := rejoin(&cobra.Command{}, deadline, 0, reexecOnUpgrade, func(s string) { said = append(said, s) }, func(*client) error { return nil })

	require.True(t, call.calledWith, "it executed the daemon's build")
	assert.Equal(t, peer.ExePath, call.path)
	assert.Equal(t, peer.ExePath, call.argv[0])
	marker, _ := envValue(call.env, envReexec)
	assert.Equal(t, peer.Exe, marker)
	carried, _ := envValue(call.env, envReexecDeadline)
	assert.Equal(t, strconv.FormatInt(deadline.UnixNano(), 10), carried)
	assert.Equal(t, []string{"the loom daemon was upgraded to 0.15.0 (v0.15.0 abc1234); carrying on under it (" + peer.ExePath + ")"}, said)
	var up *daemonUpgradedError
	assert.ErrorAs(t, err, &up, "an exec that returned (the fake's) fails fast")
}

// TestRejoin_ANewerDaemonItCantReexecFailsFast: a refusal whose build can't
// be executed (here a daemon that names no path) fails fast with the
// upgraded error, saying why it didn't carry on.
func TestRejoin_ANewerDaemonItCantReexecFailsFast(t *testing.T) {
	markReexec(t, "")
	peer := daemonBuild(t, 0o755)
	peer.ExePath = ""
	joins := refuseAsNewerBuild(t, peer)
	call := fakeExec(t)
	var said []string

	err := rejoin(&cobra.Command{}, time.Time{}, time.Minute, reexecOnUpgrade, func(s string) { said = append(said, s) }, func(*client) error { return nil })

	assert.False(t, call.calledWith, "nothing was executed")
	assert.Equal(t, 1, *joins)
	var up *daemonUpgradedError
	require.ErrorAs(t, err, &up)
	assert.ErrorContains(t, err, "the loom daemon was upgraded")
	require.Len(t, said, 1)
	assert.Contains(t, said[0], "can't carry on under the upgraded daemon: the daemon names no executable")
}

// TestRejoin_ARefusalWithNoHelloFailsFast: a newer-daemon refusal that
// carries no hello leaves nothing to execute.
func TestRejoin_ARefusalWithNoHelloFailsFast(t *testing.T) {
	refuseAsNewer(t)
	call := fakeExec(t)
	err := rejoin(&cobra.Command{}, time.Time{}, time.Minute, reexecOnUpgrade, func(string) {}, func(*client) error { return nil })
	assert.False(t, call.calledWith)
	var up *daemonUpgradedError
	assert.ErrorAs(t, err, &up)
}

// TestRejoin_ACommandThatCantStartAgainFailsFast: a caller that may not
// re-execute (flow gates start) fails fast, with a build it could execute.
func TestRejoin_ACommandThatCantStartAgainFailsFast(t *testing.T) {
	markReexec(t, "")
	refuseAsNewerBuild(t, daemonBuild(t, 0o755))
	call := fakeExec(t)
	var said []string
	err := rejoin(&cobra.Command{}, time.Time{}, time.Minute, failOnUpgrade, func(s string) { said = append(said, s) }, func(*client) error { return nil })
	assert.False(t, call.calledWith, "nothing was executed")
	assert.Empty(t, said)
	var up *daemonUpgradedError
	assert.ErrorAs(t, err, &up)
}

// TestRejoin_AnOrdinaryLossIsNotReexecuted: a daemon that can't be reached
// is joined again, as before, and nothing is executed.
func TestRejoin_AnOrdinaryLossIsNotReexecuted(t *testing.T) {
	call := fakeExec(t)
	joins := 0
	old, first, most := Join, rejoinFirst, rejoinMax
	Join = func() (*rpc.Client, error) { joins++; return nil, errors.New("dial: no loom daemon listens") }
	rejoinFirst, rejoinMax = 10*time.Millisecond, 40*time.Millisecond
	t.Cleanup(func() { Join, rejoinFirst, rejoinMax = old, first, most })

	err := rejoin(&cobra.Command{}, time.Time{}, 200*time.Millisecond, reexecOnUpgrade, func(string) {}, func(*client) error { return nil })

	var away *outOfReachError
	require.ErrorAs(t, err, &away)
	assert.Greater(t, joins, 1, "it joined again")
	assert.False(t, call.calledWith)
}

// TestFlowCommands_WhichReexecIntoANewerDaemon: each waiting command that
// reads its record before acting carries on under the newer daemon's
// build; flow gates start, whose fresh nonce would queue a partly queued
// chain again under new keys, fails fast. The bounded waits carry their
// deadline, the engine and run wait (unbounded) none.
func TestFlowCommands_WhichReexecIntoANewerDaemon(t *testing.T) {
	type outcome struct {
		out, errOut string
		err         error
	}
	cases := []struct {
		name     string
		setup    func(t *testing.T) // starts what the command follows, before the refusal
		run      func(t *testing.T) outcome
		reexec   bool
		deadline bool
	}{
		{name: "flow run engine", reexec: true,
			setup: func(t *testing.T) {
				runFixture(t, runStory("1", "intermediate-dev"))
				_, _, err := runRun(t, "", "start", "docs/plan.md")
				require.NoError(t, err)
			},
			run: func(t *testing.T) outcome {
				out, errOut, err := runRun(t, "", "engine", "plan")
				return outcome{out, errOut, err}
			}},
		{name: "flow run wait", reexec: true,
			setup: func(t *testing.T) {
				d, _, _ := runFixture(t, runStory("1", "intermediate-dev"))
				engineArgv = func(string) ([]string, error) { return []string{"sh", "-c", "echo up; sleep 30"}, nil }
				_, _, err := runRun(t, "", "start", "docs/plan.md")
				require.NoError(t, err)
				d.waitLog(t, 1, "up")
			},
			run: func(t *testing.T) outcome {
				out, errOut, err := runRun(t, "", "wait", "plan")
				return outcome{out, errOut, err}
			}},
		{name: "flow agent wait", reexec: true, deadline: true,
			setup: func(t *testing.T) {
				agentFixture(t, "intermediate-dev")
				_, _, err := runAgent(t, "SLOW", "run", "intermediate-dev")
				require.NoError(t, err)
			},
			run: func(t *testing.T) outcome {
				out, errOut, err := runAgent(t, "", "wait", "60")
				return outcome{out, errOut, err}
			}},
		{name: "flow wait wait", reexec: true, deadline: true,
			setup: func(t *testing.T) {
				d, _ := flowJobsDaemon(t)
				_, _, err := runFlow(t, "wait", "start", "slow", "--", "sh", "-c", "echo up; sleep 30")
				require.NoError(t, err)
				d.waitLog(t, 1, "up")
			},
			run: func(t *testing.T) outcome {
				out, errOut, err := runFlow(t, "wait", "wait", "slow", "60")
				return outcome{out, errOut, err}
			}},
		{name: "flow gates wait", reexec: true, deadline: true,
			setup: func(t *testing.T) {
				d, conf := flowJobsDaemon(t)
				conf.gates = []string{"RUN echo up; sleep 30"}
				_, _, err := runFlow(t, "gates", "start", "--story", "abc")
				require.NoError(t, err)
				d.waitLog(t, 1, "up")
			},
			run: func(t *testing.T) outcome {
				out, errOut, err := runFlow(t, "gates", "wait", "60")
				return outcome{out, errOut, err}
			}},
		{name: "flow gates start", reexec: false,
			setup: func(t *testing.T) {
				_, conf := flowJobsDaemon(t)
				conf.gates = []string{"RUN echo one"}
			},
			run: func(t *testing.T) outcome {
				out, errOut, err := runFlow(t, "gates", "start", "--final")
				return outcome{out, errOut, err}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			markReexec(t, "")
			inheritDeadline(t, time.Time{})
			tc.setup(t)
			peer := daemonBuild(t, 0o755)
			refuseAsNewerBuild(t, peer)
			call := fakeExec(t)
			start := time.Now()

			got := tc.run(t)

			assert.Equal(t, exitNotRun, flowExit(got.err), "the exec returned (the fake's): it fails fast\n%s%s", got.out, got.errOut)
			assert.ErrorContains(t, got.err, "the loom daemon was upgraded")
			if !tc.reexec {
				assert.False(t, call.calledWith, "it can't start again safely: nothing was executed")
				return
			}
			require.True(t, call.calledWith, "it executed the daemon's build")
			assert.Equal(t, peer.ExePath, call.path)
			assert.Contains(t, got.out+got.errOut, "carrying on under it ("+peer.ExePath+")")
			carried, has := envValue(call.env, envReexecDeadline)
			if !tc.deadline {
				assert.False(t, has, "an unbounded command carries no deadline")
				return
			}
			require.True(t, has, "a bounded wait carries its deadline")
			nanos, err := strconv.ParseInt(carried, 10, 64)
			require.NoError(t, err)
			d := time.Unix(0, nanos)
			assert.False(t, d.Before(start.Add(59*time.Second)), "the wait's own deadline, %v", d)
			assert.False(t, d.After(time.Now().Add(60*time.Second)), "the wait's own deadline, %v", d)
		})
	}
}
