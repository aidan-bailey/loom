package watch

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/aidan-bailey/loom/session/github"
)

// IssueSeen fingerprints what an issue's author and maintainers have said:
// the hex SHA-256 of the body, a colon, and the ID of the newest comment by
// an OWNER, MEMBER or COLLABORATOR (empty when there is none). A comment by
// anyone else never changes it, so a stranger cannot make the watcher nudge.
func IssueSeen(is github.WatchIssue) string {
	sum := sha256.Sum256([]byte(is.Body))
	var (
		id     string
		newest time.Time
		found  bool
	)
	for _, c := range is.Comments {
		if !trustedAssociation(c.Association) {
			continue
		}
		if !found || c.CreatedAt.After(newest) {
			id, newest, found = c.ID, c.CreatedAt, true
		}
	}
	return hex.EncodeToString(sum[:]) + ":" + id
}

func trustedAssociation(a string) bool {
	switch a {
	case "OWNER", "MEMBER", "COLLABORATOR":
		return true
	}
	return false
}
