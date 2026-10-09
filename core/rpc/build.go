package rpc

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Build names this binary: its module version, VCS revision and whether
// the tree was modified. A daemon records it in its lock.
func Build() string { return build() }

func build() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	b := info.Main.Version
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			b += " " + s.Value
		case "vcs.modified":
			if s.Value == "true" {
				b += "+dirty"
			}
		}
	}
	return b
}

// version is the release SetVersion names.
var version string

// SetVersion names this binary's release (main's version) for its hello.
// main calls it before any connection is made.
func SetVersion(v string) { version = v }

// Self is this binary's hello: its protocol and build. A server adds the
// tmux server it serves (Server.SetTmux).
func Self() Hello {
	h := identity()
	h.Version = version
	return h
}

// identity is the part of Self that does not change while the process
// runs, read once: hashing the executable reads it whole.
var identity = sync.OnceValue(func() Hello {
	h := Hello{Protocol: Protocol, Build: build(), Exe: exeHash()}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.time":
				h.Time = s.Value
			case "vcs.modified":
				h.Modified = s.Value == "true"
			}
		}
	}
	return h
})

// exeHash is the SHA-256 of the running executable, "" when it can't be
// read. It reads /proc/self/exe where there is one, the image this process
// runs, rather than os.Executable's path: a rebuild replaces the file at
// that path while an older process still runs, and that process must not
// report the new build as its own.
func exeHash() string {
	f, err := os.Open("/proc/self/exe")
	if err != nil {
		path, perr := os.Executable()
		if perr != nil {
			return ""
		}
		if f, err = os.Open(path); err != nil {
			return ""
		}
	}
	defer f.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return ""
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// Order is how a client's build compares with a server's (CompareBuilds).
type Order int

const (
	// SameBuild is one executable on both sides.
	SameBuild Order = iota
	// ClientNewer is a client newer than its server: it replaces the server.
	ClientNewer
	// ServerNewer is a server newer than its client: the client must be
	// upgraded.
	ServerNewer
)

// CompareBuilds says which of a client's and a server's builds is newer,
// from their hellos: the same executable (Exe) is the same build;
// otherwise the higher release (Version, as semver), then the later commit
// (Time), then a modified tree over a clean one. A tie counts as the
// client newer: it was just started, so a rebuild nothing else tells apart
// (a dev build edited again at one commit) replaces its sandbox's server.
// A field either side lacks, or can't be read, decides nothing.
func CompareBuilds(client, server Hello) Order {
	if client.Exe != "" && client.Exe == server.Exe {
		return SameBuild
	}
	if c := compareVersions(client.Version, server.Version); c != 0 {
		return orderOf(c)
	}
	if c := compareTimes(client.Time, server.Time); c != 0 {
		return orderOf(c)
	}
	if client.Modified != server.Modified {
		if client.Modified {
			return ClientNewer
		}
		return ServerNewer
	}
	return ClientNewer
}

// orderOf is the Order of a comparison of client with server.
func orderOf(c int) Order {
	if c > 0 {
		return ClientNewer
	}
	return ServerNewer
}

// compareTimes compares two RFC 3339 times; 0 when they are equal or
// either can't be read.
func compareTimes(a, b string) int {
	ta, errA := time.Parse(time.RFC3339, a)
	tb, errB := time.Parse(time.RFC3339, b)
	if errA != nil || errB != nil {
		return 0
	}
	return ta.Compare(tb)
}

// semver is a parsed semantic version: MAJOR.MINOR.PATCH, a pre-release
// (dot-separated identifiers) and build metadata, which orders nothing.
type semver struct {
	core [3]int
	pre  []string
}

// parseSemver reads v, with or without a leading "v".
func parseSemver(v string) (semver, bool) {
	v = strings.TrimPrefix(v, "v")
	v, _, _ = strings.Cut(v, "+")
	v, pre, hasPre := strings.Cut(v, "-")
	parts := strings.Split(v, ".")
	if len(parts) != 3 || (hasPre && pre == "") {
		return semver{}, false
	}
	var s semver
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return semver{}, false
		}
		s.core[i] = n
	}
	if hasPre {
		s.pre = strings.Split(pre, ".")
	}
	return s, true
}

// compareVersions compares two semantic versions as semver orders them; 0
// when they are equal or either can't be read.
func compareVersions(a, b string) int {
	va, okA := parseSemver(a)
	vb, okB := parseSemver(b)
	if !okA || !okB {
		return 0
	}
	for i := range va.core {
		if c := cmp.Compare(va.core[i], vb.core[i]); c != 0 {
			return c
		}
	}
	// A release outranks its pre-releases.
	switch {
	case len(va.pre) == 0 && len(vb.pre) == 0:
		return 0
	case len(va.pre) == 0:
		return 1
	case len(vb.pre) == 0:
		return -1
	}
	for i := 0; i < len(va.pre) && i < len(vb.pre); i++ {
		if c := comparePreIdent(va.pre[i], vb.pre[i]); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(va.pre), len(vb.pre))
}

// comparePreIdent compares two pre-release identifiers: numbers
// numerically, below any alphanumeric one, which compare as text.
func comparePreIdent(a, b string) int {
	na, errA := strconv.Atoi(a)
	nb, errB := strconv.Atoi(b)
	switch {
	case errA == nil && errB == nil:
		return cmp.Compare(na, nb)
	case errA == nil:
		return -1
	case errB == nil:
		return 1
	}
	return strings.Compare(a, b)
}
