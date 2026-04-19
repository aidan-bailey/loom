package git

import (
	cmd_test "claude-squad/cmd/cmd_test"
	"context"
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPushChanges_FallbackUsesFreshContext asserts that when the initial
// `gh repo sync --source` fails, the fallback `git push -u origin <branch>`
// runs under a freshly-minted context rather than the one already spent
// on the failed gh sync.
func TestPushChanges_FallbackUsesFreshContext(t *testing.T) {
	var mu sync.Mutex
	var ghSyncSourceCtx context.Context
	var gitPushCtx context.Context
	var gitPushErrAtRun error
	var gitPushDeadlineAtRun time.Time
	ghSyncErr := errors.New("simulated gh sync failure")

	mock := cmd_test.MockCmdExec{
		RunFunc: func(c *exec.Cmd) error {
			mu.Lock()
			defer mu.Unlock()
			switch classifyCmd(c) {
			case "gh-auth":
				return nil
			case "gh-sync-source":
				ghSyncSourceCtx = cmdContext(c)
				return ghSyncErr
			default:
				t.Fatalf("unexpected Run cmd: %v", c.Args)
				return nil
			}
		},
		OutputFunc: func(c *exec.Cmd) ([]byte, error) {
			t.Fatalf("unexpected Output cmd: %v", c.Args)
			return nil, nil
		},
		CombinedOutputFunc: func(c *exec.Cmd) ([]byte, error) {
			mu.Lock()
			defer mu.Unlock()
			switch classifyCmd(c) {
			case "git-status":
				return nil, nil
			case "git-push":
				gitPushCtx = cmdContext(c)
				// Snapshot ctx state while PushChanges is still running —
				// after it returns, the deferred cancels fire and Err()
				// would be misleading.
				gitPushErrAtRun = gitPushCtx.Err()
				if d, ok := gitPushCtx.Deadline(); ok {
					gitPushDeadlineAtRun = d
				}
				return nil, nil
			case "gh-sync":
				return nil, nil
			default:
				t.Fatalf("unexpected CombinedOutput cmd: %v", c.Args)
				return nil, nil
			}
		},
	}

	g := NewGitWorktreeFromStorageWithRunner(
		"/fake/repo",
		"/fake/worktree",
		"session",
		"user/branch",
		"deadbeef",
		false,
		"",
		mock,
	)

	err := g.PushChanges("msg", false)
	require.NoError(t, err, "fallback push should succeed")

	require.NotNil(t, ghSyncSourceCtx, "gh repo sync --source should have run")
	require.NotNil(t, gitPushCtx, "fallback git push should have run")
	assert.NotSame(t, ghSyncSourceCtx, gitPushCtx,
		"fallback push must use a distinct context from the failed gh sync")
	assert.NoError(t, gitPushErrAtRun,
		"fallback context must not be canceled at the moment the push runs")
	require.False(t, gitPushDeadlineAtRun.IsZero(), "fallback context must have a deadline")
	assert.Greater(t, time.Until(gitPushDeadlineAtRun), 25*time.Second,
		"fallback deadline should carry close to a full gitNetworkTimeout, got %v remaining",
		time.Until(gitPushDeadlineAtRun))
}

// classifyCmd categorises a *exec.Cmd so test dispatch logic stays readable.
// Matches on the command name (last path segment) and a key argument.
func classifyCmd(c *exec.Cmd) string {
	if len(c.Args) == 0 {
		return ""
	}
	bin := c.Args[0]
	if i := strings.LastIndex(bin, "/"); i >= 0 {
		bin = bin[i+1:]
	}
	joined := strings.Join(c.Args[1:], " ")
	switch bin {
	case "gh":
		switch {
		case strings.HasPrefix(joined, "auth status"):
			return "gh-auth"
		case strings.Contains(joined, "repo sync --source"):
			return "gh-sync-source"
		case strings.Contains(joined, "repo sync"):
			return "gh-sync"
		}
	case "git":
		switch {
		case strings.Contains(joined, "status --porcelain"):
			return "git-status"
		case strings.Contains(joined, "push"):
			return "git-push"
		}
	}
	return ""
}

// cmdContext extracts the context.Context stored in an *exec.Cmd. exec.Cmd
// keeps the context private (set via exec.CommandContext), so tests reach it
// via reflection — acceptable because the field name has been stable since
// the package's introduction and there is no public accessor.
func cmdContext(c *exec.Cmd) context.Context {
	v := reflect.ValueOf(c).Elem().FieldByName("ctx")
	if !v.IsValid() {
		return nil
	}
	return *(*context.Context)(unsafe.Pointer(v.UnsafeAddr()))
}
