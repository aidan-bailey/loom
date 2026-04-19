package cmd

import (
	"encoding/json"
	"github.com/aidan-bailey/loom/session"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMigrationInstance_MirrorsInstanceData_JSON serves as a drift guard:
// it verifies a hand-crafted JSON document that matches session.InstanceData's
// shape round-trips through migrationInstance without losing fields.
// If session adds a field, update both the struct and this fixture.
func TestMigrationInstance_MirrorsInstanceData_JSON(t *testing.T) {
	src := `{
		"schema_version": 1,
		"title": "t",
		"path": "/p",
		"branch": "b",
		"status": 2,
		"height": 10,
		"width": 20,
		"created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-02T00:00:00Z",
		"auto_yes": true,
		"program": "claude",
		"worktree": {
			"repo_path": "/r",
			"worktree_path": "/wt",
			"session_name": "t",
			"branch_name": "b",
			"base_commit_sha": "abc",
			"is_existing_branch": false
		},
		"diff_stats": {
			"added": 1,
			"removed": 2,
			"content": "x"
		},
		"is_workspace_terminal": false
	}`

	var mi migrationInstance
	assert.NoError(t, json.Unmarshal([]byte(src), &mi))

	out, err := json.Marshal(mi)
	assert.NoError(t, err)

	// Re-decode both sides into a map and compare — tolerates whitespace
	// and key-order differences between the fixture and our output.
	var srcMap, outMap map[string]any
	assert.NoError(t, json.Unmarshal([]byte(src), &srcMap))
	assert.NoError(t, json.Unmarshal(out, &outMap))
	assert.Equal(t, srcMap, outMap)
}

// TestMigrationInstance_TypeDriftGuard catches field-type drift that
// the JSON-shape test above cannot see. The JSON round-trip test will
// happily pass if InstanceData.Title becomes a `[]byte` (both emit a
// JSON string) or if Status changes from int to float64 (JSON number
// either way). Those divergences silently corrupt the migration.
//
// This test reflects over session.InstanceData and its hand-mirrored
// cmd.migrationInstance, asserting the set of JSON tags and the
// primitive Kind of each field match. Go type *Name* is allowed to
// differ (session.Status vs plain int is intentional — see the
// migrationInstance doc comment on why the mirror uses primitives),
// so we compare Kind() rather than Name().
func TestMigrationInstance_TypeDriftGuard(t *testing.T) {
	assertStructsMirror(t,
		reflect.TypeOf(session.InstanceData{}),
		reflect.TypeOf(migrationInstance{}),
		"session.InstanceData",
		"cmd.migrationInstance",
	)
	assertStructsMirror(t,
		reflect.TypeOf(session.GitWorktreeData{}),
		reflect.TypeOf(migrationWorktreeData{}),
		"session.GitWorktreeData",
		"cmd.migrationWorktreeData",
	)
	assertStructsMirror(t,
		reflect.TypeOf(session.DiffStatsData{}),
		reflect.TypeOf(migrationDiffStats{}),
		"session.DiffStatsData",
		"cmd.migrationDiffStats",
	)
}

func assertStructsMirror(t *testing.T, src, mirror reflect.Type, srcName, mirrorName string) {
	t.Helper()
	require.Equal(t, reflect.Struct, src.Kind(), "%s must be a struct", srcName)
	require.Equal(t, reflect.Struct, mirror.Kind(), "%s must be a struct", mirrorName)

	srcFields := fieldsByJSONTag(src)
	mirFields := fieldsByJSONTag(mirror)

	for name, srcField := range srcFields {
		mirField, ok := mirFields[name]
		if !assert.Truef(t, ok, "%s has JSON field %q but %s does not", srcName, name, mirrorName) {
			continue
		}
		assert.Equalf(t, srcField.Type.Kind(), mirField.Type.Kind(),
			"JSON field %q: kind drift — %s.%s=%s vs %s.%s=%s",
			name,
			srcName, srcField.Name, srcField.Type.Kind(),
			mirrorName, mirField.Name, mirField.Type.Kind(),
		)
		assert.Equalf(t, string(srcField.Tag), string(mirField.Tag),
			"JSON field %q: tag drift — %s.%s tag=%q vs %s.%s tag=%q",
			name,
			srcName, srcField.Name, string(srcField.Tag),
			mirrorName, mirField.Name, string(mirField.Tag),
		)
	}
	for name := range mirFields {
		if _, ok := srcFields[name]; !ok {
			assert.Failf(t, "mirror has extra field",
				"%s has JSON field %q that %s does not — mirror must be a strict subset of the source",
				mirrorName, name, srcName)
		}
	}
}

// fieldsByJSONTag indexes a struct's fields by their JSON tag name
// (the portion before any comma). Fields with no JSON tag are skipped
// — they are not part of the serialization contract the mirror must
// preserve.
func fieldsByJSONTag(t reflect.Type) map[string]reflect.StructField {
	out := make(map[string]reflect.StructField, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "" {
			continue
		}
		name := tag
		if idx := strings.IndexByte(tag, ','); idx >= 0 {
			name = tag[:idx]
		}
		out[name] = f
	}
	return out
}
