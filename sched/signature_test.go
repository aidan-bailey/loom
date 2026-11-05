package sched

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		argv, command, env []string
	}{
		{[]string{"direnv", "exec", ".", "env", "CARGO_INCREMENTAL=0", "bash", "scripts/web-smoke.sh"},
			[]string{"bash", "scripts/web-smoke.sh"}, []string{"CARGO_INCREMENTAL=0"}},
		{[]string{"env", "CGO_ENABLED=0", "go", "test", "./..."}, []string{"go", "test", "./..."}, []string{"CGO_ENABLED=0"}},
		{[]string{"nix", "develop", ".#ci", "-c", "cargo", "test", "--release"}, []string{"cargo", "test", "--release"}, nil},
		{[]string{"nix", "shell", "nixpkgs#jq", "--command", "jq", "."}, []string{"jq", "."}, nil},
		{[]string{"/usr/bin/time", "-v", "cargo", "build"}, []string{"time", "-v", "cargo", "build"}, nil},
		{[]string{"time", "cargo", "build"}, []string{"cargo", "build"}, nil},
		{[]string{"env", "-i", "PATH=/bin", "make"}, []string{"env", "-i", "PATH=/bin", "make"}, nil},
		{[]string{"nice", "-n", "5", "ionice", "-c", "3", "-t", "make", "-j8"}, []string{"make", "-j8"}, nil},
		{[]string{"nice", "-19", "make"}, []string{"make"}, nil},
		{[]string{"env", "B=2", "--", "A=1", "./scripts/x.sh"}, []string{"scripts/x.sh"}, []string{"A=1", "B=2"}},
		{[]string{"/nix/store/abc-cargo/bin/cargo", "build"}, []string{"cargo", "build"}, nil},
		{[]string{"env"}, []string{"env"}, nil},
		{[]string{"nix", "develop"}, []string{"nix", "develop"}, nil},
		{[]string{"sh", "-c", "cargo build && cargo test"}, []string{"sh", "-c", "cargo build && cargo test"}, nil},
	}
	for _, c := range cases {
		command, env := Normalize(c.argv)
		assert.Equal(t, c.command, command, "%q", c.argv)
		assert.Equal(t, c.env, env, "%q", c.argv)
	}
}

func TestSignature_ID(t *testing.T) {
	a := NewSignature("/r/.git", ".", []string{"env", "B=2", "A=1", "make"})
	b := NewSignature("/r/.git", ".", []string{"env", "A=1", "B=2", "make"})
	assert.Equal(t, a.ID(), b.ID(), "the pairs are sorted")
	assert.Len(t, string(a.ID()), 12)
	assert.NotEqual(t, a.ID(), NewSignature("/r/.git", "sub", []string{"env", "A=1", "B=2", "make"}).ID(), "the folder counts")
	assert.NotEqual(t, a.ID(), NewSignature("/s/.git", ".", []string{"env", "A=1", "B=2", "make"}).ID(), "the repo counts")
	assert.Equal(t, NewSignature("/r/.git", ".", []string{"make"}).ID(), Signature{Repo: "/r/.git", Folder: ".", Command: []string{"make"}, Env: []string{}}.ID(),
		"no pairs and an empty list are one signature")
}

func TestRepoName(t *testing.T) {
	assert.Equal(t, "polyoxide", RepoName("/src/polyoxide/.git"))
	assert.Equal(t, "loom.git", RepoName("/srv/loom.git"))
	assert.Equal(t, "scratch", RepoName("/tmp/scratch"))
}

// fakeRepo lays out a main checkout at root/main with a worktree at
// root/wt, as git does, with no git run: main/.git is a directory, and
// wt/.git a file naming main/.git/worktrees/wt, whose commondir is ../..
func fakeRepo(t *testing.T) (root string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	gitdir := filepath.Join(root, "main", ".git", "worktrees", "wt")
	require.NoError(t, os.MkdirAll(gitdir, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "main", "crates", "core"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "wt", "crates", "core"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(gitdir, "commondir"), []byte("../..\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "wt", ".git"), []byte("gitdir: "+gitdir+"\n"), 0o644))
	return root
}

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

func TestSignature_IDWithAPrerequisite(t *testing.T) {
	cold := NewSignature("/r/.git", ".", []string{"make"})
	assert.Equal(t, SignatureID("4e0b9c350d02"), cold.ID(), "a signature with no prerequisite keeps the ID it always had")

	warm := cold
	warm.After = "1a2b3c4d5e6f"
	assert.NotEqual(t, cold.ID(), warm.ID(), "a prerequisite makes it another signature")
	other := cold
	other.After = "0f0f0f0f0f0f"
	assert.NotEqual(t, warm.ID(), other.ID(), "and each prerequisite another again")
	none := cold
	none.After = AfterNone
	assert.NotEqual(t, cold.ID(), none.ID(), "a prerequisite with no signature still counts")
}
