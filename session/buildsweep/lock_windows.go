package buildsweep

import (
	"errors"
	"os"
)

// tryLock takes no lock on Windows, where the sweep never gets this far
// (supported is false).
func tryLock(string) (*os.File, bool, error) {
	return nil, false, errors.New("cargo's locks are not taken on windows")
}
