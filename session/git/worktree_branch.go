package git

import (
	"errors"
)

// combineErrors combines multiple errors into a single error. Uses
// errors.Join so errors.Is/errors.As still work against any
// underlying cause — the prior errors.New(msg) broke that chain.
func (g *GitWorktree) combineErrors(errs []error) error {
	return errors.Join(errs...)
}
