package core

import (
	"crypto/sha256"
	"encoding/binary"
)

// idMask keeps an ID within 53 bits, which any JSON client holds exactly
// (a float64's mantissa).
const idMask = 1<<53 - 1

// idHash derives an ID from parts: a var so a test can force a collision.
var idHash = stableID

// stableID hashes parts (NUL-separated) into a non-zero ID within idMask.
// Two daemons over one disk derive the same ID for the same workspace or
// record, so a client that rejoins another daemon keeps naming the same
// things.
func stableID(parts ...string) uint64 {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	id := binary.BigEndian.Uint64(h.Sum(nil)[:8]) & idMask
	if id == 0 {
		id = 1
	}
	return id
}

// probe returns want, or the first value after it that taken refuses,
// wrapping within idMask and skipping 0 (the draft row's ID).
func probe(want uint64, taken func(uint64) bool) uint64 {
	id := want
	for taken(id) {
		id = (id + 1) & idMask
		if id == 0 {
			id = 1
		}
	}
	return id
}

// wsKey is the part of an ID a workspace contributes: its canonical config
// dir ("" for a workspace with no context, a test's).
func wsKey(ws *Workspace) string {
	if ws == nil || ws.ctx == nil {
		return ""
	}
	return canonicalDir(ws.ctx.ConfigDir)
}
