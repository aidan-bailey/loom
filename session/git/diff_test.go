package git

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseShortStat(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		added   int
		removed int
	}{
		{"both", " 3 files changed, 10 insertions(+), 5 deletions(-)\n", 10, 5},
		{"insertions only", " 1 file changed, 3 insertions(+)\n", 3, 0},
		{"deletions only", " 1 file changed, 2 deletions(-)\n", 0, 2},
		{"empty", "", 0, 0},
		{"whitespace", "  \n", 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			added, removed := parseShortStat(tt.input)
			assert.Equal(t, tt.added, added)
			assert.Equal(t, tt.removed, removed)
		})
	}
}
