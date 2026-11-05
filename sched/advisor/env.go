package advisor

import (
	"slices"
	"strings"
)

// allowedEnvNames are the variables of a job's environment the advisor may
// see (spec §5.6): the ones that change how a build or test runs. Names
// beginning allowedEnvPrefix are allowed too.
var allowedEnvNames = map[string]bool{
	"CARGO_BUILD_JOBS": true, "CARGO_INCREMENTAL": true, "CARGO_TARGET_DIR": true, "CARGO_BUILD_TARGET": true,
	"RUSTFLAGS": true, "RUSTDOCFLAGS": true, "RUST_TEST_THREADS": true, "RUSTC_WRAPPER": true,
	"GOFLAGS": true, "GOMAXPROCS": true, "GOMEMLIMIT": true, "GOGC": true, "GOOS": true, "GOARCH": true, "CGO_ENABLED": true,
	"MAKEFLAGS": true, "NIX_BUILD_CORES": true, "CMAKE_BUILD_PARALLEL_LEVEL": true,
	"CC": true, "CXX": true, "CFLAGS": true, "CXXFLAGS": true, "LDFLAGS": true, "NODE_OPTIONS": true,
}

const allowedEnvPrefix = "CARGO_PROFILE_"

// secretWords drop a variable even when it is allowed.
var secretWords = []string{"TOKEN", "SECRET", "PASSW", "KEY", "CRED", "AUTH"}

const maxEnvValue = 512

// AllowedEnv is the part of env the advisor may see: the allowed names,
// less any whose name holds a secret word in any case, each value cut to
// 512 bytes. No other name or value is kept.
func AllowedEnv(env []string) []string {
	var out []string
	for _, kv := range env {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || !(allowedEnvNames[name] || strings.HasPrefix(name, allowedEnvPrefix)) {
			continue
		}
		upper := strings.ToUpper(name)
		if slices.ContainsFunc(secretWords, func(w string) bool { return strings.Contains(upper, w) }) {
			continue
		}
		if len(value) > maxEnvValue {
			value = strings.ToValidUTF8(value[:maxEnvValue], "")
		}
		out = append(out, name+"="+value)
	}
	return out
}
