package session

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestMigrate_V0Upgrades verifies a record without a schema_version
// field is treated as v0 and stamped with CurrentSchemaVersion.
func TestMigrate_V0Upgrades(t *testing.T) {
	raw := []byte(`{"title":"legacy","program":"claude","status":2,"is_workspace_terminal":false}`)

	data, err := Migrate(raw)
	assert.NoError(t, err)
	assert.Equal(t, CurrentSchemaVersion, data.SchemaVersion)
	assert.Equal(t, "legacy", data.Title)
}

// TestMigrate_V1UpgradesDropsAutoYes verifies a v1 record carrying the
// now-removed auto_yes field migrates cleanly to CurrentSchemaVersion —
// encoding/json silently drops the unknown key on unmarshal, so there's
// no explicit field to clear, only the version stamp to advance.
func TestMigrate_V1UpgradesDropsAutoYes(t *testing.T) {
	raw := []byte(`{"schema_version":1,"title":"legacy","program":"claude","auto_yes":true}`)

	data, err := Migrate(raw)
	assert.NoError(t, err)
	assert.Equal(t, CurrentSchemaVersion, data.SchemaVersion)
	assert.Equal(t, "legacy", data.Title)
}

// TestMigrate_Idempotent verifies records already at CurrentSchemaVersion
// migrate cleanly and stay stable.
func TestMigrate_Idempotent(t *testing.T) {
	original := InstanceData{
		SchemaVersion: CurrentSchemaVersion,
		Title:         "v1",
		Program:       "claude",
	}
	raw, err := json.Marshal(original)
	assert.NoError(t, err)

	migrated, err := Migrate(raw)
	assert.NoError(t, err)
	assert.Equal(t, original, migrated)

	raw2, err := json.Marshal(migrated)
	assert.NoError(t, err)
	migrated2, err := Migrate(raw2)
	assert.NoError(t, err)
	assert.Equal(t, migrated, migrated2)
}

// TestMigrate_FutureVersionRejected verifies an unknown future schema
// version is rejected — better than silently using a record whose shape
// we don't understand.
func TestMigrate_FutureVersionRejected(t *testing.T) {
	raw := []byte(`{"schema_version":99,"title":"future"}`)
	_, err := Migrate(raw)
	assert.Error(t, err)
}

// TestMigrateAll_EmptyInput returns nil/nil, matching the prior
// "empty payload" behaviour of the decode paths.
func TestMigrateAll_EmptyInput(t *testing.T) {
	out, err := MigrateAll(nil)
	assert.NoError(t, err)
	assert.Nil(t, out)

	out, err = MigrateAll([]byte{})
	assert.NoError(t, err)
	assert.Nil(t, out)
}

// TestMigrateAll_MixedSchemaVersions round-trips a heterogeneous array
// where some records carry schema_version and others don't.
func TestMigrateAll_MixedSchemaVersions(t *testing.T) {
	raw := []byte(`[{"title":"legacy"},{"schema_version":1,"title":"current"}]`)
	out, err := MigrateAll(raw)
	assert.NoError(t, err)
	assert.Len(t, out, 2)
	assert.Equal(t, CurrentSchemaVersion, out[0].SchemaVersion)
	assert.Equal(t, CurrentSchemaVersion, out[1].SchemaVersion)
	assert.Equal(t, "legacy", out[0].Title)
	assert.Equal(t, "current", out[1].Title)
}
