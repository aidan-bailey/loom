package advisor

// The JSON schemas of the advisor's answers, passed to --json-schema.
const (
	PriorSchema = `{"type":"object","additionalProperties":false,"required":["like","cpus","memory_gb","io","duration_seconds","note"],` +
		`"properties":{"like":{"type":"string"},"cpus":{"type":"integer"},"memory_gb":{"type":"integer"},"io":{"type":"boolean"},` +
		`"duration_seconds":{"type":"integer"},"note":{"type":"string"}}}`
	NoteSchema = `{"type":"object","additionalProperties":false,"required":["note"],"properties":{"note":{"type":"string"}}}`
)
