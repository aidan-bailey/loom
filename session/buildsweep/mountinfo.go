package buildsweep

import (
	"fmt"
	"strings"
	"time"
)

// atimeMode is how a mount records access times.
type atimeMode int

const (
	// atimeStrict records every access: a mount whose options name no
	// atime flag (strictatime).
	atimeStrict atimeMode = iota
	// atimeRelative records an access at most once a day (relatime).
	atimeRelative
	// atimeOff records none (noatime).
	atimeOff
)

// mount is one line of the mount table: where it is mounted and how it
// records access times.
type mount struct {
	point string
	atime atimeMode
}

// parseMountInfo reads /proc/self/mountinfo (proc(5)): the mount point is
// each line's fifth field and the per-mount options its sixth, where the
// kernel reports noatime and relatime.
func parseMountInfo(data string) ([]mount, error) {
	var out []mount
	for _, line := range strings.Split(data, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if len(f) < 6 {
			return nil, fmt.Errorf("mountinfo: short line %q", line)
		}
		m := mount{point: unescapeMount(f[4])}
		for _, opt := range strings.Split(f[5], ",") {
			switch {
			case opt == "noatime":
				m.atime = atimeOff
			case opt == "relatime" && m.atime != atimeOff:
				m.atime = atimeRelative
			}
		}
		out = append(out, m)
	}
	return out, nil
}

// unescapeMount decodes the octal escapes (\040 for a space) the kernel
// writes in a mount point.
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) && isOctal(s[i+1]) && isOctal(s[i+2]) && isOctal(s[i+3]) {
			if v := int(s[i+1]-'0')<<6 | int(s[i+2]-'0')<<3 | int(s[i+3]-'0'); v <= 0xff {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isOctal(c byte) bool { return c >= '0' && c <= '7' }

// mountFor is the mount holding path: the one with the longest mount
// point at or above it, and of two at one point the later line, which
// was mounted over the other.
func mountFor(mounts []mount, path string) (mount, bool) {
	var best mount
	found := false
	for _, m := range mounts {
		if within(m.point, path) && (!found || len(m.point) >= len(best.point)) {
			best, found = m, true
		}
	}
	return best, found
}

// within reports whether p is dir or lies beneath it; both are clean,
// absolute paths.
func within(dir, p string) bool {
	if dir == "/" {
		return strings.HasPrefix(p, "/")
	}
	return p == dir || strings.HasPrefix(p, dir+"/")
}

// recordsUse reports whether m records access times finely enough to tell
// a file unused for after, and if not why, for the report.
func recordsUse(m mount, after time.Duration) (bool, string) {
	switch m.atime {
	case atimeOff:
		return false, m.point + " is mounted noatime, so it records no access times"
	case atimeRelative:
		if after < 24*time.Hour {
			return false, m.point + " is mounted relatime, which records an access at most once a day: too coarse for a threshold under 24h"
		}
	}
	return true, ""
}
