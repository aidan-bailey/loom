package session

import (
	"encoding/json"
	"fmt"

	"github.com/aidan-bailey/loom/log"
)

// Migrate decodes a single raw JSON object representing a stored instance
// and upgrades it to CurrentSchemaVersion. The function is idempotent: a
// record already at CurrentSchemaVersion round-trips unchanged.
//
// v0 records (missing SchemaVersion field → decodes to 0) are treated as
// pre-versioning. The v0→v1 step is a pure field-default upgrade and
// exists to establish the migration plumbing. v1→v2 drops the AutoYes
// field (encoding/json already ignores the now-unknown "auto_yes" key
// on unmarshal, so this step too is just a version stamp). v2→v3 adds
// GitWorktreeData.StashRef; v3→v4 adds HeadroomProxy; v4→v5 adds
// CacheTTL1h; v5→v6 adds Issue; v6→v7 adds ClaudeSessionID and
// ClaudeTranscriptPath — all default correctly on their own
// (empty string, false, 0), so every step is a pure version stamp.
//
// Contributor protocol: when adding/renaming/removing an InstanceData
// field, bump CurrentSchemaVersion and append a new case to the switch
// that upgrades from the previous version. The JSON fixture in
// cmd/workspace_migrate_shape_test.go is a drift guard for the
// `workspace migrate` CLI's typed mirror struct and must be updated in
// the same commit.
func Migrate(raw []byte) (InstanceData, error) {
	var data InstanceData
	if err := json.Unmarshal(raw, &data); err != nil {
		return InstanceData{}, fmt.Errorf("unmarshal instance: %w", err)
	}

	if data.SchemaVersion > CurrentSchemaVersion {
		return InstanceData{}, fmt.Errorf("unsupported schema version %d (binary supports up to %d)", data.SchemaVersion, CurrentSchemaVersion)
	}

	for data.SchemaVersion < CurrentSchemaVersion {
		switch data.SchemaVersion {
		case 0:
			// v0 → v1: no payload changes. Just stamp the version so
			// future decodes skip this branch.
			data.SchemaVersion = 1
		case 1:
			// v1 → v2: AutoYes removed. No payload changes needed —
			// unmarshal already dropped the field — just stamp the version.
			data.SchemaVersion = 2
		case 2:
			// v2 → v3: GitWorktreeData gained StashRef. No payload
			// changes needed — unmarshal already defaults a missing
			// field to "" — just stamp the version.
			data.SchemaVersion = 3
		case 3:
			// v3 → v4: HeadroomProxy added. No payload changes needed —
			// the zero value (false) already matches the desired default
			// for pre-existing records — just stamp the version.
			data.SchemaVersion = 4
		case 4:
			// v4 → v5: CacheTTL1h added. No payload changes needed — the
			// zero value (false) already matches the desired default for
			// pre-existing records — just stamp the version.
			data.SchemaVersion = 5
		case 5:
			// v5 → v6: Issue added. Zero value (0 = unlinked) is the
			// correct default for pre-existing records — version stamp only.
			data.SchemaVersion = 6
		case 6:
			// v6 → v7: ClaudeSessionID and ClaudeTranscriptPath added.
			// Empty (no conversation recorded, resume with --continue) is
			// the correct default for pre-existing records — version stamp
			// only.
			data.SchemaVersion = 7
		default:
			return InstanceData{}, fmt.Errorf("no upgrade path from schema version %d", data.SchemaVersion)
		}
	}
	return data, nil
}

// MigrateAll applies Migrate to every element of a JSON array of raw
// instance records.
//
// A record that fails Migrate — corrupt, or written by a newer loom with a
// schema_version above CurrentSchemaVersion (e.g. after a downgrade) — is
// logged and returned in skipped as its original bytes rather than failing
// the whole array. Storage writes skipped records back verbatim on every
// save, so one unreadable record can never take the rest of the list down
// with it, nor be silently dropped itself. err is returned only when the
// top-level payload is not a JSON array at all; there are then no
// individual records to preserve, and Storage refuses to write over it.
//
// Empty input returns nil, nil, nil.
func MigrateAll(rawArray []byte) (records []InstanceData, skipped []json.RawMessage, err error) {
	if len(rawArray) == 0 {
		return nil, nil, nil
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(rawArray, &raw); err != nil {
		return nil, nil, fmt.Errorf("unmarshal instance array: %w", err)
	}
	records = make([]InstanceData, 0, len(raw))
	for i, r := range raw {
		d, err := Migrate(r)
		if err != nil {
			log.For("session").Warn("instance_undecodable", "index", i, "err", err, "action", "preserved_verbatim")
			skipped = append(skipped, r)
			continue
		}
		records = append(records, d)
	}
	return records, skipped, nil
}
