package session

import (
	"encoding/json"
	"fmt"
)

// Migrate decodes a single raw JSON object representing a stored instance
// and upgrades it to CurrentSchemaVersion. The function is idempotent: a
// record already at CurrentSchemaVersion round-trips unchanged.
//
// v0 records (missing SchemaVersion field → decodes to 0) are treated as
// pre-versioning. The v0→v1 step is a pure field-default upgrade today
// and exists to establish the migration plumbing; future schema changes
// extend this switch.
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
		default:
			return InstanceData{}, fmt.Errorf("no upgrade path from schema version %d", data.SchemaVersion)
		}
	}
	return data, nil
}

// MigrateAll applies Migrate to every element of a JSON array of raw
// instance records. Callers that already have []json.RawMessage should
// prefer this over decoding twice.
func MigrateAll(rawArray []byte) ([]InstanceData, error) {
	if len(rawArray) == 0 {
		return nil, nil
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(rawArray, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal instance array: %w", err)
	}
	out := make([]InstanceData, 0, len(raw))
	for i, r := range raw {
		d, err := Migrate(r)
		if err != nil {
			return nil, fmt.Errorf("instance %d: %w", i, err)
		}
		out = append(out, d)
	}
	return out, nil
}
