package app

import (
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/ui"
)

// ownerJobBadges is every session's work-queue badge text, keyed by the
// owner's InstanceID (core.JobBadges' labels).
func ownerJobBadges(q core.QueueView) map[core.InstanceID]string {
	badges := core.JobBadges(q)
	out := make(map[core.InstanceID]string, len(badges))
	for id, b := range badges {
		out[id] = b.Label
	}
	return out
}

// syncJobBadges publishes the badges of home.queue to the cards. Call it
// wherever home.queue changes.
func (m *home) syncJobBadges() {
	ui.SetJobBadges(ownerJobBadges(m.queue))
}
