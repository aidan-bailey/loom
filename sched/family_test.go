package sched

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// famSig is the signature of argv in the folder "." of a repo.
func famSig(argv ...string) Signature { return NewSignature("/src/loom/.git", ".", argv) }

// famLabel is argv's family's label, "" for none.
func famLabel(argv ...string) string {
	f, ok := FamilyOf(famSig(argv...))
	if !ok {
		return ""
	}
	return f.Label()
}

func TestFamilyOf(t *testing.T) {
	cases := []struct {
		want string
		argv []string
	}{
		// Merged: filters, trims, redirections, assignments, wrappers, flag order, paths.
		{"go test ./sched", []string{"go", "test", "./sched"}},
		{"go test ./sched", []string{"sh", "-c", "go test ./sched -run TestA 2>&1 | tail -40"}},
		{"go test ./sched", []string{"go", "test", "./sched/", "-count=1", "-v"}},
		{"go test ./sched", []string{"env", "CGO_ENABLED=0", "go", "test", "./sched"}},
		{"go test ./sched", []string{"sh", "-c", "CGO_ENABLED=0 go test ./sched && echo ok"}},
		{"go test ./sched", []string{"bash", "-ec", "export X=1; nice -n 5 go test ./sched | grep -v ^ok"}},
		{"go test ./sched", []string{"/usr/local/bin/go", "test", "./sched"}},
		{"go test ./sched", []string{"sh", "-c", "go test ./sched; go test ./sched -run TestB"}},
		{"go test -race ./...", []string{"go", "test", "-race", "./...", "-count=1"}},
		{"go test -race ./...", []string{"go", "test", "-count=1", "--race", "./..."}},
		{"go test -tags e2e ./e2e/...", []string{"go", "test", "-tags", "e2e", "./e2e/..."}},
		{"go test -tags e2e ./e2e/...", []string{"go", "test", "-tags=e2e", "./e2e/...", "-v"}},
		{"go test ./core ./sched", []string{"go", "test", "./sched", "./core"}},
		{"cargo nextest run -p a -p b", []string{"cargo", "nextest", "run", "-p", "b", "-p", "a", "some_filter"}},
		{"cargo nextest run -p a -p b", []string{"cargo", "nextest", "run", "--package=a", "-pb", "--lib", "--", "--exact"}},
		{"cargo test --workspace --doc", []string{"cargo", "test", "--all", "--doc", "--all-features"}},
		{"cargo test --workspace --doc", []string{"cargo", "test", "--doc", "--workspace"}},
		{"cargo clippy -p a && cargo fmt", []string{"sh", "-c", "cargo fmt --check && cargo clippy -p a -- -D warnings"}},
		{"cargo clippy -p a && cargo fmt", []string{"sh", "-c", "cargo clippy -p a && cargo fmt --check"}},
		{"cargo clippy -p a && cargo fmt --workspace", []string{"sh", "-c", "cargo clippy -p a && cargo fmt --all --check"}},
		// Kept apart.
		{"cd a && pnpm test", []string{"sh", "-c", "cd a && pnpm test"}},
		{"cd b && pnpm test", []string{"sh", "-c", "cd b && pnpm test"}},
		{"cargo build --release", []string{"cargo", "build", "--release"}},
		{"cargo build", []string{"cargo", "build"}},
		{"go test -tags systemd_live ./sched", []string{"go", "test", "-tags", "systemd_live", "./sched"}},
		{"go vet ./sched", []string{"go", "vet", "./sched"}},
		// Other programs: the program and its first two words that aren't flags.
		{"pnpm exec playwright", []string{"pnpm", "exec", "playwright", "test", "x.spec.ts", "--workers", "8"}},
		{"python3 scripts/gen.py", []string{"python3", "scripts/gen.py", "--check"}},
		{"bash scripts/verify-affected.sh", []string{"bash", "scripts/verify-affected.sh"}},
		{"sleep 0", []string{"sh", "-c", "sleep 0 2>&1 | tail -3"}},
		// No family.
		{"", []string{"sh", "-c", "echo 'unbalanced"}},
		{"", []string{"sh", "-c", "(cd x && make)"}},
		{"", []string{"sh", "-c", "cat <<EOF\nx\nEOF"}},
		{"", []string{"sh", "-c", "for f in a b; do go test $f; done"}},
		{"", []string{"sh", "-c", "echo hi | tail -1"}},
		{"", []string{"sh", "-c", "cd x"}},
		{"", []string{"true"}},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, famLabel(c.argv...), "%q", c.argv)
	}
}

func TestFamilyOf_KeyHoldsTheRepoAndFolderButNotTheEnvOrPrerequisite(t *testing.T) {
	key := func(s Signature) string {
		f, ok := FamilyOf(s)
		require.True(t, ok)
		return f.Key()
	}
	base := famSig("go", "test", "./sched")
	withEnv := famSig("env", "CGO_ENABLED=0", "go", "test", "./sched")
	after := base
	after.After = "0123456789ab"
	assert.Equal(t, key(base), key(withEnv), "the environment is not part of it")
	assert.Equal(t, key(base), key(after), "nor the prerequisite")
	assert.NotEqual(t, key(base), key(NewSignature("/src/loom/.git", "core", []string{"go", "test", "./sched"})), "another folder")
	assert.NotEqual(t, key(base), key(NewSignature("/src/other/.git", ".", []string{"go", "test", "./sched"})), "another repo")
}

func TestFamilies_Pool(t *testing.T) {
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	var bs Beliefs
	self := famSig("sh", "-c", "go test ./sched -run TestA")
	sister := famSig("go", "test", "./sched")
	cousin := famSig("go", "test", "./sched", "-run", "TestB")
	stranger := famSig("go", "test", "./core")
	run := func(job JobID, age time.Duration) Run {
		return Run{Job: job, Ended: now.Add(-age), Outcome: OutcomeDone, PeakKB: 1 << 20, WallSeconds: 1}
	}
	bs.Ensure(self, now).AddRun(run(1, 0))
	for i := range 8 {
		bs.Ensure(sister, now).AddRun(run(JobID(10+i), time.Duration(i+1)*time.Minute))
	}
	for i := range 4 {
		bs.Ensure(cousin, now).AddRun(run(JobID(20+i), time.Duration(i)*time.Minute+30*time.Second))
	}
	bs.Ensure(stranger, now).AddRun(run(30, 0))

	var fs Families
	pool := fs.Pool(bs.Beliefs, self)
	require.NotNil(t, pool)
	require.Len(t, pool.Runs, window, "cut to the window")
	var jobs []JobID
	for _, r := range pool.Runs {
		jobs = append(jobs, r.Job)
	}
	assert.Equal(t, []JobID{20, 10, 21, 11, 22, 12, 23, 13, 14, 15}, jobs,
		"its relatives' runs newest first; its own run and the stranger's are left out")
	assert.Nil(t, fs.Pool(bs.Beliefs, stranger), "a family with no other belief")
	assert.Nil(t, fs.Pool(bs.Beliefs, famSig("sh", "-c", "(x)")), "no family")
	assert.Equal(t, 3, fs.Members(bs.Beliefs, self), "self, sister and cousin")
	assert.Zero(t, fs.Members(bs.Beliefs, famSig("true")))
}

func TestFamilies_Kin(t *testing.T) {
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	var bs Beliefs
	for _, argv := range [][]string{
		{"sh", "-c", "cd a && cargo nextest run -p x && cargo clippy -p x"},
		{"sh", "-c", "cd a && cargo clippy -p x && cargo nextest run -p x filter"},
		{"cargo", "clippy", "-p", "x"},
		{"sh", "-c", "cd a && cargo build"},
		{"sh", "-c", "(x)"},
	} {
		bs.Ensure(famSig(argv...), now)
	}
	b := func(i int) *Belief { return &bs.Beliefs[i] }
	var fs Families
	same, shared := fs.Kin(b(0), b(1))
	assert.True(t, same)
	assert.Equal(t, 2, shared, "the cd step doesn't count")
	same, shared = fs.Kin(b(0), b(2))
	assert.False(t, same)
	assert.Equal(t, 1, shared)
	_, shared = fs.Kin(b(0), b(3))
	assert.Zero(t, shared, "a shared cd step alone")
	_, shared = fs.Kin(b(0), b(4))
	assert.Zero(t, shared, "a belief with no family")
}
