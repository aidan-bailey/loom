package advisor

import _ "embed"

// SystemPrompt is the advisor's system prompt (spec §5.6).
//
//go:embed prompt.md
var SystemPrompt string
