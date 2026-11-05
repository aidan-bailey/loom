# Advisor run context Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every job record and run keeps the checkout it ran in, and each known belief in an advisor request carries its signature's allowed pairs and its last three runs, with whether each ran in the job's checkout, so the advisor can tell a warm build from a cold one.

**Architecture:** `sched.FindRepo` also returns the checkout (the directory holding the `.git`, symlinks resolved); `submitJob` stores it on the `Record` and `RunOf` copies it to the `Run`. `sched/advisor` gains `KnownRun` and `RecentRuns`, a pure function from a belief's runs, the job's checkout and the time; `core`'s `known` fills `Known.Env` (through `AllowedEnv`) and `Known.Recent`. The prompt says how to read them. Learned sizing is untouched.

**Tech Stack:** Go (module `github.com/aidan-bailey/loom`), testify.

**Spec:** [`docs/superpowers/specs/2026-11-05-advisor-run-context-design.md`](../specs/2026-11-05-advisor-run-context-design.md). Execution: inline (superpowers:executing-plans), no checkpoint pauses; one commit per task.

---

## Ground rules

- Scratch files in the session scratchpad; commit by path (`git commit -- <paths>`), never `-a` or `git add .`. zsh doesn't split a variable into words: list paths, or use an array (`"${F[@]}"`).
- Quick checks directly (`CGO_ENABLED=0 go test ./sched/ -run X`); the full suite and the race run through `loom job run`, in the background.
- `gofmt -l` on the packages touched (never `gofmt -w .`); `CGO_ENABLED=0 go vet ./sched/... ./core/...`; golangci-lint skipped (the dev shell's v2 rejects the config).
- No syscall, process or socket code changes, so no Windows or Darwin build is needed.

## Files

| File | Responsibility |
|---|---|
| `sched/repo.go`, `sched/signature_test.go` | `FindRepo` returns the checkout; `TestFindRepo` |
| `sched/job.go` | `Record.Checkout` |
| `sched/belief.go`, `sched/belief_test.go` | `Run.Checkout`, `RunOf`; `TestRunOf`, the store round trip |
| `core/queue_beliefs.go`, `core/queue_ops.go`, `core/queue_beliefs_test.go` | `jobSignature` returns the checkout, `submitJob` stores it; the record and run test |
| `sched/advisor/request.go`, `sched/advisor/request_test.go` (new) | `KnownRun`, `MaxRecentRuns`, `RecentRuns`, `Known.Env`, `Known.Recent`; `TestRecentRuns` |
| `sched/advisor/prompt.md` | how to read `recent` and `here` |
| `core/queue_advisor.go`, `core/queue_advisor_test.go` | `known`, `knownBeliefs` and `advisorInputFor` take the job's checkout and the time; `TestAdvisor_KnownBeliefsCarryTheirRecentRuns` |
| `sched/README.md`, `sched/advisor/README.md`, `docs/superpowers/specs/2026-11-05-smart-scheduler-design.md` | the facts |

---

### Task 1: The checkout, from `FindRepo` to the run

**Files:**
- Modify: `sched/repo.go` (`FindRepo`)
- Modify: `sched/signature_test.go` (`TestFindRepo`)
- Modify: `sched/job.go` (`Record`)
- Modify: `sched/belief.go` (`Run`, `RunOf`)
- Modify: `sched/belief_test.go` (`TestRunOf`, `TestBeliefsStore_RoundTripAndRefusals`)
- Modify: `core/queue_beliefs.go` (`jobSignature`), `core/queue_ops.go` (`submitJob`)
- Modify: `core/queue_beliefs_test.go` (`wqSigOf`, `TestJobQueue_ARunTeachesTheNextJobOfTheSameCommand`)

- [ ] **Step 1: Write the failing tests**

In `sched/signature_test.go`, replace `TestFindRepo` with:

```go
func TestFindRepo(t *testing.T) {
	root := fakeRepo(t)
	common := filepath.Join(root, "main", ".git")
	main, wt := filepath.Join(root, "main"), filepath.Join(root, "wt")
	cases := []struct{ dir, repo, folder, checkout string }{
		{main, common, ".", main},
		{filepath.Join(main, "crates", "core"), common, filepath.Join("crates", "core"), main},
		{wt, common, ".", wt},
		{filepath.Join(wt, "crates", "core"), common, filepath.Join("crates", "core"), wt},
	}
	for _, c := range cases {
		repo, folder, checkout, err := FindRepo(c.dir)
		require.NoError(t, err, c.dir)
		assert.Equal(t, c.repo, repo, c.dir)
		assert.Equal(t, c.folder, folder, c.dir)
		assert.Equal(t, c.checkout, checkout, c.dir)
	}

	t.Run("a relative gitdir", func(t *testing.T) {
		root := fakeRepo(t)
		require.NoError(t, os.WriteFile(filepath.Join(root, "wt", ".git"), []byte("gitdir: ../main/.git/worktrees/wt\n"), 0o644))
		repo, _, checkout, err := FindRepo(filepath.Join(root, "wt"))
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(root, "main", ".git"), repo)
		assert.Equal(t, filepath.Join(root, "wt"), checkout)
	})
	t.Run("through a symlink", func(t *testing.T) {
		root := fakeRepo(t)
		link := filepath.Join(t.TempDir(), "core")
		require.NoError(t, os.Symlink(filepath.Join(root, "wt", "crates", "core"), link))
		_, folder, checkout, err := FindRepo(link)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("crates", "core"), folder)
		assert.Equal(t, filepath.Join(root, "wt"), checkout, "the tree the link resolves into, not the link's own")
	})
	t.Run("no repository", func(t *testing.T) {
		dir, err := filepath.EvalSymlinks(t.TempDir())
		require.NoError(t, err)
		repo, folder, checkout, err := FindRepo(dir)
		require.NoError(t, err)
		assert.Equal(t, dir, repo, "the folder is its own repo")
		assert.Equal(t, ".", folder)
		assert.Equal(t, dir, checkout, "and its own checkout")
	})
	t.Run("a .git file naming nothing", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".git"), []byte("not a gitdir line\n"), 0o644))
		_, _, _, err := FindRepo(dir)
		assert.ErrorContains(t, err, "names no gitdir")
	})
	t.Run("a gitdir that is gone", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /nonexistent/x/.git/worktrees/y\n"), 0o644))
		_, _, _, err := FindRepo(dir)
		assert.Error(t, err)
	})
}
```

In `sched/belief_test.go`, `TestRunOf`: give `base` a checkout and check it in the loop:

```go
	base := Record{ID: 6, Claim: Claim{CPUs: 8, MemoryGB: 12, IO: true}, Started: bt0, Ended: bt0.Add(93 * time.Second), PeakRSSKB: 14_000_000,
		Checkout: "/src/wt"}
```

and after `assert.Equal(t, rec.Claim, run.Claim)` in the `t.Run` body:

```go
			assert.Equal(t, "/src/wt", run.Checkout, "the run keeps its checkout")
```

In `TestBeliefsStore_RoundTripAndRefusals`, give the stored run a checkout so the round trip covers it:

```go
	b.AddRun(Run{Job: 3, Outcome: OutcomeDone, PeakKB: 700_000, WallSeconds: 40, WriteBytes: int64p(1), Checkout: "/src/wt"})
```

In `core/queue_beliefs_test.go`, `wqSigOf` takes the new return:

```go
	repo, folder, _, err := sched.FindRepo(cwd)
```

and in `TestJobQueue_ARunTeachesTheNextJobOfTheSameCommand`, after `first := wqRecord(t, m, id1)`:

```go
	checkout, err := filepath.EvalSymlinks(sub.Cwd)
	require.NoError(t, err)
	assert.Equal(t, checkout, first.Checkout, "a folder in no repository is its own checkout")
```

and after `require.Len(t, b.Runs, 1, "and the settle recorded the run")`:

```go
	assert.Equal(t, checkout, b.Runs[0].Checkout, "the run keeps it")
```

- [ ] **Step 2: Run them to see them fail**

Run: `CGO_ENABLED=0 go test ./sched/ ./core/ -run 'TestFindRepo|TestRunOf|TestBeliefsStore_RoundTripAndRefusals|TestJobQueue_ARunTeachesTheNextJobOfTheSameCommand'`
Expected: build failures: `FindRepo` returns 3 values, `unknown field Checkout`.

- [ ] **Step 3: Implement**

`sched/repo.go`, `FindRepo` (doc comment and body):

```go
// FindRepo finds the repository holding dir from git's own files, with no
// git run (smart scheduler spec §5.1): it walks up from dir, symlinks
// resolved, to the first directory with a .git. A .git directory is the
// repository's git dir; a .git file names a worktree's (or a submodule's)
// git dir on its gitdir: line, whose commondir file, when it has one, names
// the common dir. repo is the common dir, symlinks resolved, so every
// worktree of a repository has the same one; folder is dir relative to the
// directory holding the .git, and checkout is that directory, the working
// tree's top (advisor run context spec §5.1). With no .git up to the root,
// dir is its own repo and checkout, and folder is ".". A .git it can't
// read, or one naming nothing that resolves, is an error: the caller sizes
// the job without a signature.
func FindRepo(dir string) (repo, folder, checkout string, err error) {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", "", "", fmt.Errorf("resolve %s: %w", dir, err)
	}
	for top := real; ; {
		common, found, err := gitCommonDir(top)
		if err != nil {
			return "", "", "", err
		}
		if found {
			rel, err := filepath.Rel(top, real)
			if err != nil {
				return "", "", "", fmt.Errorf("place %s under %s: %w", real, top, err)
			}
			return common, rel, top, nil
		}
		parent := filepath.Dir(top)
		if parent == top {
			return real, ".", real, nil
		}
		top = parent
	}
}
```

`sched/job.go`, in `Record`'s smart scheduler block, after `Signature`:

```go
	Checkout  string       `json:"checkout,omitempty"` // the working tree's top it runs in (FindRepo); "" without a signature (advisor run context spec §5.1)
```

`sched/belief.go`, in `Run`, after `Spare`:

```go
	Checkout    string    `json:"checkout,omitempty"` // the working tree's top it ran in (Record.Checkout); "" for a run recorded before 2026-11-05
```

and in `RunOf`'s literal:

```go
	run := Run{
		Job: rec.ID, Ended: rec.Ended, Outcome: out, Adopted: rec.Adopted, Claim: rec.Claim,
		PeakKB: rec.PeakRSSKB, WallSeconds: rec.Ended.Sub(rec.Started).Seconds(), Spare: rec.Spare.CPUs,
		Checkout: rec.Checkout,
	}
```

`core/queue_beliefs.go`, `jobSignature`:

```go
// jobSignature is the signature of argv run in cwd with the checkout it
// runs in, or why there is none (spec §5.1). It reads git's files, never
// runs git, so the loop can call it.
func jobSignature(argv []string, cwd string) (sig *sched.Signature, checkout, unsized string) {
	repo, folder, checkout, err := sched.FindRepo(cwd)
	if err != nil {
		return nil, "", err.Error()
	}
	s := sched.NewSignature(repo, folder, argv)
	return &s, checkout, ""
}
```

`core/queue_ops.go`, `submitJob`:

```go
	sig, checkout, unsized := jobSignature(s.Argv, s.Cwd)
	rec := sched.Record{
		Description: strings.TrimSpace(s.Description), Argv: slices.Clone(s.Argv), Cwd: s.Cwd,
		Notify: s.Notify, Tail: s.Tail, State: sched.Queued, Submitted: now,
		Asked: s.Claim, Unsized: unsized, Checkout: checkout,
	}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `gofmt -w sched/repo.go sched/job.go sched/belief.go sched/signature_test.go sched/belief_test.go core/queue_beliefs.go core/queue_ops.go core/queue_beliefs_test.go && CGO_ENABLED=0 go test ./sched/ ./core/ -run 'TestFindRepo|TestRunOf|TestBeliefsStore_RoundTripAndRefusals|TestJobQueue_|TestAdvisor_'`
Expected: `ok` for both packages.

- [ ] **Step 5: Commit**

```bash
git add sched/repo.go sched/job.go sched/belief.go sched/signature_test.go sched/belief_test.go core/queue_beliefs.go core/queue_ops.go core/queue_beliefs_test.go
git commit -m "feat(sched,core): keep the checkout each job and run is in" -- sched/repo.go sched/job.go sched/belief.go sched/signature_test.go sched/belief_test.go core/queue_beliefs.go core/queue_ops.go core/queue_beliefs_test.go
```

---

### Task 2: Recent runs, the pairs and the prompt

**Files:**
- Modify: `sched/advisor/request.go` (`Known`; new `KnownRun`, `MaxRecentRuns`, `RecentRuns`)
- Create: `sched/advisor/request_test.go`
- Modify: `sched/advisor/prompt.md`
- Modify: `core/queue_advisor.go` (`dispatchAdvisor`, `advisorInputFor`, `knownBeliefs`, `known`)
- Modify: `core/queue_advisor_test.go`

- [ ] **Step 1: Write the failing tests**

Create `sched/advisor/request_test.go`:

```go
package advisor

import (
	"testing"
	"time"

	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
)

func TestRecentRuns(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	wrote := int64(1536 << 20)
	runs := []sched.Run{ // newest first, as a belief keeps them
		{Ended: now.Add(-90 * time.Second), Outcome: sched.OutcomeDone, Claim: sched.Claim{CPUs: 4, MemoryGB: 6}, Spare: 2,
			WallSeconds: 12.6, PeakKB: 2_936_013, WriteBytes: &wrote, Checkout: "/src/wt"},
		{Ended: now.Add(-time.Hour), Outcome: sched.OutcomeFailed, Claim: sched.Claim{CPUs: 2, MemoryGB: 4},
			WallSeconds: 2, PeakKB: 512_000, Checkout: "/src/main"},
		{Ended: now.Add(-2 * time.Hour), Outcome: sched.OutcomeDone, Claim: sched.Claim{Exclusive: true}, WallSeconds: 700, PeakKB: 8 << 20},
		{Ended: now.Add(-3 * time.Hour), Outcome: sched.OutcomeDone, Claim: sched.Claim{CPUs: 4, MemoryGB: 6}, WallSeconds: 8},
	}
	yes, no, mb := true, false, int64(1536)
	assert.Equal(t, []KnownRun{
		{Outcome: sched.OutcomeDone, MinutesAgo: 1, Cores: 6, WallSeconds: 13, PeakGB: 2.8, WroteMB: &mb, Here: &yes},
		{Outcome: sched.OutcomeFailed, MinutesAgo: 60, Cores: 2, WallSeconds: 2, PeakGB: 0.5, Here: &no},
		{Outcome: sched.OutcomeDone, MinutesAgo: 120, WallSeconds: 700, PeakGB: 8},
	}, RecentRuns(runs, "/src/wt", now), "the newest three; no cores for an exclusive claim, no here for a run with no checkout")

	for _, r := range RecentRuns(runs, "", now) {
		assert.Nil(t, r.Here, "no here for a job with no checkout")
	}
	assert.Empty(t, RecentRuns(nil, "/src/wt", now))
	ahead := []sched.Run{{Ended: now.Add(time.Minute), Outcome: sched.OutcomeDone}}
	assert.Equal(t, 0, RecentRuns(ahead, "", now)[0].MinutesAgo, "a run ending after now is 0 minutes ago, not negative")
}
```

In `core/queue_advisor_test.go`, add `"encoding/json"` and `"slices"` to the imports, and these:

```go
// wqCheckouts makes a repository's main checkout and one worktree of it from
// git's files alone, which is all FindRepo reads, and returns both, resolved.
func wqCheckouts(t *testing.T) (main, wt string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	main, wt = filepath.Join(root, "main"), filepath.Join(root, "wt")
	gitdir := filepath.Join(main, ".git", "worktrees", "wt")
	require.NoError(t, os.MkdirAll(gitdir, 0o755))
	require.NoError(t, os.MkdirAll(wt, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(gitdir, "commondir"), []byte("../..\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o644))
	return main, wt
}

// wqKnown is the belief of command in a prior's request, as the advisor is
// told of it.
func wqKnown(t *testing.T, request string, command ...string) advisor.Known {
	t.Helper()
	var req advisor.PriorRequest
	require.NoError(t, json.Unmarshal([]byte(request), &req))
	for _, k := range req.Beliefs {
		if slices.Equal(k.Command, command) {
			return k
		}
	}
	require.Failf(t, "no such belief", "%q in %s", command, request)
	return advisor.Known{}
}

func TestAdvisor_KnownBeliefsCarryTheirRecentRuns(t *testing.T) {
	m, calls := wqAdvisedModel(t, wqPriorAnswer, 0)
	main, wt := wqCheckouts(t)
	submit := func(cwd string, argv ...string) sched.JobID {
		t.Helper()
		sub := wqJob(t, argv...)
		sub.Cwd = cwd
		id, _, err := m.submitJob(sub)
		require.NoError(t, err)
		wqWait(t, "the prior", func() bool { wqPump(t, m); return wqRecord(t, m, id).Sizing == "" })
		return id
	}
	first := submit(main, "env", "CC=clang", "API_TOKEN=s3cret", "sh", "-c", "exit 0")
	assert.Equal(t, main, wqRecord(t, m, first).Checkout)
	wqSettle(t, m, first)

	here := submit(main, "sh", "-c", "exit 1")
	there := submit(wt, "sh", "-c", "exit 2")
	assert.Equal(t, main, wqRecord(t, m, here).Checkout)
	assert.Equal(t, wt, wqRecord(t, m, there).Checkout, "a worktree is its own checkout of the same repository")

	got := wqCalls(t, calls)
	require.Len(t, got, 3, "one prior per command")
	k := wqKnown(t, got[1], "sh", "-c", "exit 0")
	assert.Equal(t, []string{"CC=clang"}, k.Env, "its pairs, less the one named like a secret")
	require.Len(t, k.Recent, 1)
	assert.Equal(t, sched.OutcomeDone, k.Recent[0].Outcome)
	require.NotNil(t, k.Recent[0].Here)
	assert.True(t, *k.Recent[0].Here, "the run was in the main checkout, as this job is")
	k = wqKnown(t, got[2], "sh", "-c", "exit 0")
	require.Len(t, k.Recent, 1)
	require.NotNil(t, k.Recent[0].Here)
	assert.False(t, *k.Recent[0].Here, "this job is in the worktree")
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `CGO_ENABLED=0 go test ./sched/advisor/ ./core/ -run 'TestRecentRuns|TestAdvisor_KnownBeliefsCarryTheirRecentRuns'`
Expected: build failures: `undefined: KnownRun`, `undefined: RecentRuns`, `k.Env undefined`, `k.Recent undefined`.

- [ ] **Step 3: Implement**

`sched/advisor/request.go`: add `"math"` and `"time"` to the imports, give `Known` its two fields, and add the run type and its builder:

```go
// Known is a belief as the advisor is told of it.
type Known struct {
	ID           sched.SignatureID  `json:"id"`
	Repo         string             `json:"repo"`
	Folder       string             `json:"folder"`
	Command      []string           `json:"command"`
	Env          []string           `json:"env,omitempty"` // its signature's pairs, through AllowedEnv
	Runs         int                `json:"runs"`
	Claim        sched.Claim        `json:"claim"`
	Sources      sched.ClaimSources `json:"sources"`
	UsualSeconds int                `json:"usual_seconds,omitempty"`
	P90Seconds   int                `json:"p90_seconds,omitempty"`
	Notes        []string           `json:"notes,omitempty"`
	Recent       []KnownRun         `json:"recent,omitempty"` // RecentRuns
}

// MaxRecentRuns is how many of a known belief's runs a request carries.
const MaxRecentRuns = 3

// KnownRun is one of a known belief's recent runs as the advisor is told of
// it (advisor run context spec §5.2).
type KnownRun struct {
	Outcome     sched.Outcome `json:"outcome"`
	MinutesAgo  int           `json:"minutes_ago"`
	Cores       int           `json:"cores,omitempty"` // the cores it ran with; 0 for an exclusive claim
	WallSeconds int           `json:"wall_seconds"`
	PeakGB      float64       `json:"peak_gb"`
	WroteMB     *int64        `json:"wrote_mb,omitempty"` // nil when it measured no I/O
	Here        *bool         `json:"here,omitempty"`     // whether it ran in the job's checkout; nil when either checkout is unknown
}

// RecentRuns is the newest MaxRecentRuns of runs (newest first, as a belief
// keeps them) as the advisor is told of them at now, for a job in checkout
// ("" for none): cores with spare ones, the peak in GiB to one decimal, MiB
// written, and here when both checkouts are known.
func RecentRuns(runs []sched.Run, checkout string, now time.Time) []KnownRun {
	var out []KnownRun
	for _, r := range runs[:min(len(runs), MaxRecentRuns)] {
		k := KnownRun{
			Outcome: r.Outcome, MinutesAgo: max(0, int(now.Sub(r.Ended)/time.Minute)), Cores: r.Claim.CPUs + r.Spare,
			WallSeconds: int(math.Round(r.WallSeconds)), PeakGB: math.Round(float64(r.PeakKB)/(1<<20)*10) / 10,
		}
		if r.WriteBytes != nil {
			mb := int64(math.Round(float64(*r.WriteBytes) / (1 << 20)))
			k.WroteMB = &mb
		}
		if checkout != "" && r.Checkout != "" {
			here := r.Checkout == checkout
			k.Here = &here
		}
		out = append(out, k)
	}
	return out
}
```

`core/queue_advisor.go`: `dispatchAdvisor` passes the time,

```go
			if in, ok = m.advisorInputFor(c, now); !ok {
```

`advisorInputFor` takes it and hands both on:

```go
// advisorInputFor captures what call c's job needs, at now.
func (m *Model) advisorInputFor(c advisorCall, now time.Time) (advisorInput, bool) {
```

```go
	switch c.kind {
	case advisePrior:
		in.known = m.knownBeliefs(b, rec.Checkout, now)
	case adviseReconcile:
		in.belief, in.runs = m.known(*b, rec.Checkout, now), slices.Clone(b.Runs)
```

`knownBeliefs` and `known`:

```go
// knownBeliefs are the beliefs a prior for self is told of, for a job in
// checkout at now: up to knownSameRepo of its repo and knownOtherRepos of
// others running the same program, most recently seen first.
func (m *Model) knownBeliefs(self *sched.Belief, checkout string, now time.Time) []advisor.Known {
```

with its two loops calling `m.known(b, checkout, now)`, and

```go
// known is b as the advisor is told of it, for a job in checkout at now
// (advisor run context spec §5.2).
func (m *Model) known(b sched.Belief, checkout string, now time.Time) advisor.Known {
	q := m.queue
	k := advisor.Known{
		ID: b.ID, Repo: sched.RepoName(b.Repo), Folder: b.Folder, Command: slices.Clone(b.Command),
		Env: advisor.AllowedEnv(b.Env), Runs: len(b.Runs), Recent: advisor.RecentRuns(b.Runs, checkout, now),
	}
```

(the rest of `known` is unchanged).

`sched/advisor/prompt.md`: in the paragraph that begins "Reason from the measurements", name the recent runs,

```
Reason from the measurements in "beliefs" (claims learned from runs, usual durations, recent runs, notes) before general knowledge.
```

and add a paragraph after it:

```
Each belief's "recent" lists its last runs, newest first. A run with "wrote_mb" of 1024 or more built from cold; one that wrote little reused an earlier build. A run whose outcome is not "done" stopped early, so its time and memory are lower bounds. "here" says whether a run was in this job's checkout, where it left its build output: expect this job to reuse a build when a recent run here built the same crates or packages with the same flags and environment, and to build from cold otherwise. Don't give a "like" whose runs reused a build for a job you expect to build from cold, or the other way round: give your own numbers.
```

- [ ] **Step 4: Run the tests to see them pass, then the live contract**

Run: `gofmt -w sched/advisor/request.go sched/advisor/request_test.go core/queue_advisor.go core/queue_advisor_test.go && CGO_ENABLED=0 go test ./sched/advisor/ ./core/ -run 'TestRecentRuns|TestParsePrior|TestAdvisor_'`
Expected: `ok` for both.

Run (spends one Haiku call; strip the session's Claude variables, `CLAUDE_EFFORT` among them): `env -u CLAUDECODE -u CLAUDE_EFFORT ADVISOR_LIVE_CLAUDE_CONFIG_DIR=$CLAUDE_CONFIG_DIR CGO_ENABLED=0 go test -count=1 -tags advisor_live ./sched/advisor -run TestLive -v`
Expected: `--- PASS: TestLive_TheRealCLIAnswersAPrior`.

- [ ] **Step 5: Commit**

```bash
git add sched/advisor/request.go sched/advisor/request_test.go sched/advisor/prompt.md core/queue_advisor.go core/queue_advisor_test.go
git commit -m "feat(sched,core): show the advisor each known belief's recent runs" -- sched/advisor/request.go sched/advisor/request_test.go sched/advisor/prompt.md core/queue_advisor.go core/queue_advisor_test.go
```

---

### Task 3: The facts, and the whole suite

**Files:**
- Modify: `sched/README.md`
- Modify: `sched/advisor/README.md`
- Modify: `docs/superpowers/specs/2026-11-05-smart-scheduler-design.md` (§5.6)

- [ ] **Step 1: Update the facts**

`sched/README.md`, the files list:

```
- `repo.go` — `FindRepo`: the repository, the folder within it and the checkout.
```

and at the end of the **Signature.** paragraph:

```
`FindRepo` also gives the checkout, the directory holding the `.git` (symlinks resolved): a record and its run keep it (`Checkout`), apart from the signature, which every checkout of a repository shares.
```

`sched/advisor/README.md`, the `request.go` line:

```
- `request.go` — `PriorRequest` and `ReconcileRequest`, the JSON the call reads on stdin: the job (argv, normalized command, description, repo, folder, the allowed environment), the host (cores, RAM, caps, default claim), known beliefs (each with its signature's allowed pairs and, from `RecentRuns`, its last `MaxRecentRuns` runs: outcome, minutes ago, cores, wall time, peak, MiB written, and whether it ran in the job's checkout), and for a reconcile the runs, the prior, the surprising run and the log's last lines.
```

`docs/superpowers/specs/2026-11-05-smart-scheduler-design.md`, §5.6, the beliefs bullet ends:

```
… each with its ID, folder, command, learned claim, usual duration and P90, run count and notes, and since the [advisor run context](2026-11-05-advisor-run-context-design.md) its signature's allowed pairs and its last three runs;
```

- [ ] **Step 2: Check the docs lint and format**

Run: `CGO_ENABLED=0 go test ./tools/claudemd/ && gofmt -l sched core && CGO_ENABLED=0 go vet ./sched/... ./core/...`
Expected: `ok`, no files listed, no vet output.

- [ ] **Step 3: The full suite and the race run, through the queue**

Run (background): `loom job run -d "full suite: advisor run context" -- env CGO_ENABLED=0 go test ./...`
Run (background): `loom job run -d "race: advisor run context" -- env CC=clang CGO_ENABLED=1 go test -race ./sched/... ./core/...`
Expected: every package `ok`. If `TestProtocolReference` fails, regenerate with `go test ./core/rpc -run TestProtocolReference -update` and add `docs/specs/protocol.md` to the commit.

- [ ] **Step 4: Commit**

```bash
git add sched/README.md sched/advisor/README.md docs/superpowers/specs/2026-11-05-smart-scheduler-design.md
git commit -m "docs(sched): the checkout and the advisor's recent runs" -- sched/README.md sched/advisor/README.md docs/superpowers/specs/2026-11-05-smart-scheduler-design.md
```
