package session

import (
	"os"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/log"
)

// e2ePauseHold is empty in every build but the e2e suite's, which sets it
// at link time
// (-X github.com/aidan-bailey/loom/session.e2ePauseHold=<marker>|<duration>)
// to hold each Pause in flight: TestE2E_Daemon_PauseAcrossAStop stops the
// daemon under a pause, and Pause runs no git step a hook could hold.
var e2ePauseHold string

// holdPauseForE2E creates e2ePauseHold's marker file and sleeps its
// duration. It does nothing when e2ePauseHold is empty, and logs and does
// nothing when it does not parse.
func holdPauseForE2E() {
	if e2ePauseHold == "" {
		return
	}
	marker, wait, ok := strings.Cut(e2ePauseHold, "|")
	d, err := time.ParseDuration(wait)
	if !ok || marker == "" || err != nil {
		log.For("session").Warn("e2e_pause_hold.unparsable", "value", e2ePauseHold)
		return
	}
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		log.For("session").Warn("e2e_pause_hold.marker_failed", "err", err)
	}
	time.Sleep(d)
}
