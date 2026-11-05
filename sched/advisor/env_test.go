package advisor

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAllowedEnv(t *testing.T) {
	env := []string{
		"PATH=/bin", "HOME=/home/x", "GITHUB_TOKEN=ghp_secret", "AWS_SECRET_ACCESS_KEY=s3cr3t",
		"CARGO_INCREMENTAL=0", "CARGO_PROFILE_RELEASE_LTO=thin", "CARGO_PROFILE_X_TOKEN=leak",
		"RUSTFLAGS=-C target-cpu=native", "CGO_ENABLED=0", "GOFLAGS=" + strings.Repeat("x", 600),
	}
	got := AllowedEnv(env)
	assert.Contains(t, got, "CARGO_INCREMENTAL=0")
	assert.Contains(t, got, "CARGO_PROFILE_RELEASE_LTO=thin")
	assert.Contains(t, got, "RUSTFLAGS=-C target-cpu=native")
	assert.Contains(t, got, "CGO_ENABLED=0")
	assert.Contains(t, got, "GOFLAGS="+strings.Repeat("x", 512), "cut to 512 bytes")
	joined := strings.Join(got, "\n")
	for _, secret := range []string{"ghp_secret", "s3cr3t", "leak", "PATH", "HOME"} {
		assert.NotContains(t, joined, secret)
	}
}
