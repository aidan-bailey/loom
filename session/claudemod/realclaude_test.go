package claudemod

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The mod's whole reach on $: the spec's security section allows these and
// nothing else (no $.process, no $.fs, no UI beyond the debug log).
var allowedCalls = map[string]bool{
	"$.clock.sleep": true, "$.env.get": true, "$.http.fetch": true, "$.prompt.submit": true,
	"$.session.id": true, "$.session.version": true, "$.ui.log": true,
}

// viaCallers is the " (via pump, report)" the validator puts after a call
// made inside a helper.
var viaCallers = regexp.MustCompile(` \([^)]*\)`)

// TestRealClaude_ValidatesTheMod runs the installed Claude's own validator on
// the embedded mod, and pins what it hooks and calls. It only reads, but needs
// claude, so it runs only with LOOM_TEST_REAL_CLAUDE=1.
func TestRealClaude_ValidatesTheMod(t *testing.T) {
	if os.Getenv("LOOM_TEST_REAL_CLAUDE") != "1" {
		t.Skip("set LOOM_TEST_REAL_CLAUDE=1 to run against real Claude")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not installed")
	}
	dir := filepath.Join(t.TempDir(), "mod")
	require.NoError(t, WriteTo(dir))

	out, err := exec.Command("claude", "plugin", "validate", dir).CombinedOutput()

	require.NoError(t, err, string(out))
	s := string(out)
	assert.Contains(t, s, "Validation passed")
	assert.Contains(t, s, "hooks: session.start, turn.start, turn.complete")
	// The validator lists them as "./register.ts calls: $.env.get,
	// $.ui.log (via pump, report), …": drop each "(via …)" before splitting,
	// since its callers are comma-separated too.
	var calls []string
	for _, line := range strings.Split(s, "\n") {
		if _, rest, ok := strings.Cut(line, " calls: "); ok {
			for _, c := range strings.Split(viaCallers.ReplaceAllString(rest, ""), ", ") {
				calls = append(calls, strings.TrimSpace(c))
			}
		}
	}
	require.NotEmpty(t, calls, "the validator lists the module's calls:\n%s", s)
	for _, c := range calls {
		assert.True(t, allowedCalls[c], "the mod calls %s, outside its allowed reach", c)
	}
}
