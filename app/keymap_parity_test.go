package app

import (
	"testing"

	"github.com/aidan-bailey/loom/keys"
	"github.com/aidan-bailey/loom/script"

	"github.com/stretchr/testify/assert"
)

// TestKeymapParity is the guard keys.GlobalkeyBindings' doc comment
// promises but that, until this test, nothing enforced: dispatch is owned
// by the embedded script/defaults.lua (see app/app_scripts.go's
// initScriptsIn/buildReservedKeys for how the engine is built), and
// GlobalkeyBindings only drives the help panel and menu-bar highlighter —
// so a change to one that isn't mirrored in the other silently breaks the
// UI's advertised keys without failing any test.
//
// For every KeyName in GlobalkeyBindings except KeySubmitName (submit is
// overlay-only dispatch — text-input submission never reaches the
// default-state script engine) and the display-only Recoverable-selection
// aliases KeyRecover/KeyDiscard (same physical keys as KeyResume/KeyKill,
// just relabeled for that context, so they don't get their own
// defaults.lua entry):
//   - every physical key in binding.Keys() must be bound by defaults.lua
//     (engine.HasAction(k));
//   - if defaults.lua gives the binding's primary key (Keys()[0]) a help
//     string, it must equal binding.Help().Desc.
func TestKeymapParity(t *testing.T) {
	engine := script.NewEngine(buildReservedKeys())
	engine.LoadDefaults()

	help := make(map[string]string, len(engine.Registrations()))
	for _, r := range engine.Registrations() {
		help[r.Key] = r.Help
	}

	excluded := map[keys.KeyName]bool{
		keys.KeySubmitName: true,
		keys.KeyRecover:    true,
		keys.KeyDiscard:    true,
	}

	for name, binding := range keys.GlobalkeyBindings {
		if excluded[name] {
			continue
		}
		keyStrings := binding.Keys()
		if len(keyStrings) == 0 {
			continue
		}
		binding := binding
		t.Run(keyStrings[0], func(t *testing.T) {
			for _, k := range keyStrings {
				assert.True(t, engine.HasAction(k),
					"key %q must be bound by script/defaults.lua", k)
			}
			if got, ok := help[keyStrings[0]]; ok && got != "" {
				assert.Equal(t, binding.Help().Desc, got,
					"keys.GlobalkeyBindings help for %q must match defaults.lua's", keyStrings[0])
			}
		})
	}
}
