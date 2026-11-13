package watch

// CheckoutDir is the directory name of the watcher's detached checkout of
// origin/<base>, under the workspace's config dir (`{repo}/.loom`). It sits
// outside `worktrees/`, so orphan discovery never walks it.
const CheckoutDir = "watch-checkout"
