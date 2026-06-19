package app

import (
	"sync"
	"testing"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"

	"charm.land/bubbles/v2/spinner"
)

// TestScriptHost_ConcurrentNavAndRead reproduces the data race the audit
// identified: the script "sync" primitives (CursorUp/CursorDown/List*) mutate
// m.list directly from the dispatch tea.Cmd goroutine, while the Update/View
// loop reads the same list on the main goroutine (e.g. the preview tick calling
// GetSelectedInstance). After the fix these primitives only record deferred
// actions applied on the main goroutine in handleScriptDone, so the dispatch
// goroutine no longer mutates shared model state. Must pass under
// `go test -race`.
func TestScriptHost_ConcurrentNavAndRead(t *testing.T) {
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	list := ui.NewList(&sp, false)
	for _, title := range []string{"a", "b", "c"} {
		inst, err := session.NewInstance(session.InstanceOptions{
			Title:   title,
			Path:    t.TempDir(),
			Program: "claude",
		})
		if err != nil {
			t.Fatal(err)
		}
		_ = list.AddInstance(inst)
	}
	list.SetSelectedInstance(0)

	h := &home{list: list}
	host := &scriptHost{m: h}

	var wg sync.WaitGroup
	const n = 2000
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			host.CursorDown()
			host.CursorUp()
			host.ListBottom()
			host.ListTop()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			_ = h.list.GetSelectedInstance()
		}
	}()
	wg.Wait()
}
