package rpc

import (
	"os"
	"reflect"
	"testing"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/core/rpc/internal/gen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGenerated_IsFresh fails when methods_gen.go no longer matches
// core/iface.go: run `go generate ./core/rpc` after changing core.Core.
func TestGenerated_IsFresh(t *testing.T) {
	src, err := os.ReadFile("../iface.go")
	require.NoError(t, err)
	want, err := gen.Generate(src)
	require.NoError(t, err)
	got, err := os.ReadFile("methods_gen.go")
	require.NoError(t, err)
	assert.Equal(t, string(want), string(got), "methods_gen.go is stale: run go generate ./core/rpc")
}

// TestMethods_CoverCore: the wire carries every core.Core method but Sync,
// which a client answers itself.
func TestMethods_CoverCore(t *testing.T) {
	iface := reflect.TypeOf((*core.Core)(nil)).Elem()
	var want []string
	for i := 0; i < iface.NumMethod(); i++ {
		if name := iface.Method(i).Name; name != "Sync" {
			want = append(want, name)
		}
	}
	var got []string
	for _, m := range methods {
		got = append(got, m.Name)
	}
	assert.ElementsMatch(t, want, got)
}
